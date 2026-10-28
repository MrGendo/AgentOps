"""OpenHands 适配器：经官方 software-agent-sdk 程序化驱动（LLM/Agent/Conversation/Tool）。

- SDK 未安装或模型未配置 → availability 明确 unavailable，绝不静默假跑
- 复用平台统一工作区（独立 git 目录）与独立验收，与 mini-SWE 同一套安全边界
- 事件映射：上游 Message/Action/Observation/Error → 统一类型，source=openhands
"""

import time

from app.adapters.base import finish
from app.swea import acceptance as acc
from app.swea import workspace as wsmod

AGENT_ID = "openhands"
_UPSTREAM_VERSION = "openhands-sdk 1.49.1（vendor 参考 46b1fa5）"


class OpenHandsAdapter:
    agent_id = AGENT_ID
    display_name = "OpenHands"
    description = "OpenHands（官方 SDK）：CodeAct 风格，内置终端/文件编辑工具"

    def _sdk(self):
        try:
            import openhands.sdk as s  # noqa
            return s
        except Exception as e:
            return e

    def version(self) -> str:
        return _UPSTREAM_VERSION

    def availability(self) -> dict:
        err = self._sdk()
        if isinstance(err, Exception):
            return {"available": False, "configuration_status": "missing_dependency",
                    "reason": f"openhands-sdk 未安装：{type(err).__name__}"}
        import os
        key = os.getenv("AGENTOPS_SWE_API_KEY", "")
        model = os.getenv("AGENTOPS_OH_MODEL", os.getenv("AGENTOPS_SWE_MODEL", ""))
        if not key or not model:
            return {"available": False, "configuration_status": "incomplete",
                    "reason": "缺少 AGENTOPS_SWE_API_KEY 或模型名"}
        return {"available": True, "configuration_status": "complete",
                "reason": f"模型 {model}"}

    def validate(self, goal: dict) -> str | None:
        if not goal.get("problem") or not goal.get("fixture"):
            return "缺少 problem 或 fixture"
        return None

    def execute(self, task_id, goal, on_event=None):
        events: list[dict] = []

        def emit(kind, data, source="adapter"):
            ev = {"kind": kind, "source": source, "data": data}
            events.append(ev)
            if on_event:
                on_event(ev)

        av = self.availability()
        if not av["available"]:
            # 如实失败：SDK 缺失/配置不全绝不伪跑
            return finish("FAILED", "AGENT_UNAVAILABLE",
                          final_output={"error": av["reason"], "agent_id": AGENT_ID},
                          trajectory=events, token_usage={}, agent_id=AGENT_ID)

        emit("stage", {"stage": "preparing_workspace"})
        try:
            ws = wsmod.prepare_workspace(task_id, goal["fixture"])
        except Exception as e:
            return finish("FAILED", "INVALID_INPUT", final_output={"error": str(e)},
                          trajectory=events, token_usage={}, agent_id=AGENT_ID)

        t0 = time.monotonic()
        try:
            out = self._run_sdk(ws, goal, emit)
        except TimeoutError:
            return finish("FAILED", "TIMEOUT",
                          final_output={"error": "执行超时", "patch": wsmod.extract_patch(ws)[:8000]},
                          trajectory=events, token_usage={}, keep_workspace=True, agent_id=AGENT_ID)
        except Exception as e:
            emit("crash", {"error": f"{type(e).__name__}: {e}"}, source="openhands")
            return finish("FAILED", "AGENT_CRASHED",
                          final_output={"error": str(e), "patch": wsmod.extract_patch(ws)[:8000],
                                        "changed_files": wsmod.changed_files(ws)},
                          trajectory=events, token_usage={}, keep_workspace=True, agent_id=AGENT_ID)

        # 独立验收（与 mini-SWE 同一判定器）
        emit("stage", {"stage": "running_acceptance"})
        try:
            acc_res = acc.run_acceptance(ws, goal.get("test_cmd", "python -m pytest -q"))
        except acc.UnsafeTestCommand as e:
            return finish("FAILED", "UNSAFE_TEST_CMD", final_output={"error": str(e)},
                          trajectory=events, token_usage=out.get("token_usage", {}), agent_id=AGENT_ID)
        emit("acceptance", {"passed": acc_res["passed"], "exit_code": acc_res["exit_code"]})

        ok = acc_res["passed"]
        wsmod.cleanup_workspace(ws, keep=not ok)
        return finish("SUCCEEDED" if ok else "FAILED",
                      "ACCEPTANCE_PASSED" if ok else "ACCEPTANCE_FAILED",
                      final_output={"agent_exit_status": out.get("exit_status", ""),
                                    "agent_submission": out.get("submission", ""),
                                    "acceptance": acc_res,
                                    "changed_files": wsmod.changed_files(ws),
                                    "patch": wsmod.extract_patch(ws)[:20000],
                                    "model": out.get("model", ""), "mode": "real"},
                      trajectory=events, token_usage=out.get("token_usage", {}),
                      duration_ms=int((time.monotonic() - t0) * 1000), agent_id=AGENT_ID)

    def _run_sdk(self, ws, goal, emit) -> dict:
        """官方 SDK 真实执行：LLM + Agent(Terminal/FileEditor) + Conversation(本地)。"""
        import os
        sdk = self._sdk()
        # v1.49.1 实测：工具为 Pydantic 规范模型，用 Agent 内置默认工具集注入

        model = os.getenv("AGENTOPS_OH_MODEL", os.getenv("AGENTOPS_SWE_MODEL", ""))
        base = os.getenv("AGENTOPS_OH_BASE", os.getenv("AGENTOPS_SWE_API_BASE", ""))
        key = os.getenv("AGENTOPS_SWE_API_KEY", "")
        # 官方 API（v1.49.1 实测）：LLM(model,api_key,base_url)；工具在工作区内执行
        llm = sdk.LLM(model=model, api_key=key, base_url=base or None)
                # litellm 兼容补丁：部分 provider 的 token wrapper 缺 cache 字段
        try:
            from litellm.litellm_core_utils.prompt_templates.common_utils import                 PromptTokensDetailsWrapper as _PTDW
            if not hasattr(_PTDW, "cache_creation_tokens"):
                _PTDW.cache_creation_tokens = 0
        except (ImportError, AttributeError):
            pass
        import openhands.tools.terminal  # noqa: F401 —— 导入即注册 —— 导入即注册工具到 SDK 注册表
        import openhands.tools.file_editor  # noqa: F401
        _tools = [sdk.Tool(name="terminal", params={}), sdk.Tool(name="file_editor", params={})]
        agent = sdk.Agent(llm=llm, tools=_tools)
        emit("agent_start", {"model": model, "exec": "local"}, source="openhands")
        conv = sdk.Conversation(agent=agent, workspace=str(ws))
        conv.send_message(goal["problem"])
        conv.run()
        # 事件映射：保留上游原始类型名与摘要，统一进 trajectory
        n_model = n_tool = 0
        final_text, exit_status = "", ""
        _evs = getattr(conv, "events", None)
        if _evs is None and hasattr(conv, "get_events"):
            _evs = conv.get_events()
        for ev in (_evs or []):
            t = type(ev).__name__
            d = {"upstream": t}
            if "Message" in t:
                n_model += 1
                d["text"] = str(getattr(ev, "text", ""))[:200]
                final_text = d["text"]
                emit("model_call", d, source="openhands")
            elif "Action" in t:
                n_tool += 1
                d["action"] = type(getattr(ev, "action", None)).__name__
                emit("tool_call", d, source="openhands")
            elif "Observation" in t:
                emit("observation", {"upstream": t,
                                     "text": str(getattr(ev, "text", ""))[:200]}, source="openhands")
            elif "Error" in t:
                exit_status = "error"
                emit("error", d, source="openhands")
        if not exit_status:
            exit_status = "submitted"

        usage = getattr(getattr(conv, "llm", None), "usage", None)
        tokens = {"model_calls": n_model}
        if usage:
            tokens["total_tokens"] = int(getattr(usage, "total_tokens", 0) or 0)
        emit("agent_exit", {"exit_status": exit_status}, source="openhands")
        return {"exit_status": exit_status, "submission": final_text[:2000],
                "token_usage": tokens, "model": model}
