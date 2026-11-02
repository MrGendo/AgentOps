"""SWE 任务执行器：装配工作区 → 运行 Agent（mock/real）→ 独立验收 → 汇总轨迹。

事件诚实标注来源：source=adapter（阶段推断）/ upstream（上游真实返回）。
真实模式经 minisweagent 程序化 API（DefaultAgent + LitellmModel + 隔离 Environment）。
"""

import json
import time
from pathlib import Path

from app.swea import acceptance as acc
from app.audit.multi_level import run_audit
from app.audit.report import AuditReport
from app.swea import workspace as wsmod
from app.swea.config import ConfigError, settings, validate_real_mode


def _finish(status: str, reason: str, **extra):
    return {"status": status, "finish_reason": reason, **extra}


def execute_swe_task(task_id: str, goal: dict, on_event=None) -> dict:
    """执行一个 swe.fix 任务。goal 契约：
    {problem, fixture, test_cmd, step_limit?, time_limit_s?}
    返回 digest 兼容结构（status/finish_reason/final_output/trajectory/token_usage）。
    """
    events: list[dict] = []

    def emit(kind: str, source: str, data: dict):
        ev = {"kind": kind, "source": source, "data": data}
        events.append(ev)
        if on_event:
            on_event(ev)

    problem = str(goal.get("problem", "")).strip()
    fixture = str(goal.get("fixture", "")).strip()
    test_cmd = str(goal.get("test_cmd", "python -m pytest -q")).strip()
    if not problem or not fixture:
        return _finish("FAILED", "INVALID_INPUT",
                       final_output={"error": "缺少 problem 或 fixture"},
                       trajectory=events, token_usage={})

    t0 = time.monotonic()
    emit("stage", "adapter", {"stage": "preparing_workspace"})
    try:
        ws = wsmod.prepare_workspace(task_id, fixture)
    except (FileNotFoundError, FileExistsError) as e:
        return _finish("FAILED", "INVALID_INPUT", final_output={"error": str(e)},
                       trajectory=events, token_usage={})

    try:
        if settings.mode == "real":
            result = _run_real(ws, problem, goal, emit)
        else:
            result = _run_mock(ws, fixture, problem, emit)
    except ConfigError as e:
        # 配置错误如实失败，绝不静默降级 Mock
        return _finish("FAILED", "CONFIG_ERROR", final_output={"error": str(e)},
                       trajectory=events, token_usage={})
    except TimeoutError:
        return _finish("FAILED", "TIMEOUT",
                       final_output={**result_preview(ws), "error": "执行超时"},
                       trajectory=events, token_usage={})
    except Exception as e:  # Agent 崩溃：保留工作区与补丁证据
        emit("crash", "adapter", {"error": f"{type(e).__name__}: {e}"})
        patch = wsmod.extract_patch(ws)
        return _finish("FAILED", "AGENT_CRASHED",
                       final_output={"error": str(e), "patch": patch,
                                     "changed_files": wsmod.changed_files(ws)},
                       trajectory=events, token_usage={},
                       keep_workspace=True)

    # ---- 多层次验收审计（L1 冒烟/L2 范围/L3 安全/L4 质量）----
    emit("stage", "adapter", {"stage": "running_audit"})
    changed = wsmod.changed_files(ws)
    try:
        audit_results = run_audit(ws, test_cmd, changed, timeout=settings.cmd_timeout)
    except Exception as e:
        return _finish("FAILED", "AUDIT_ERROR", final_output={"error": str(e)},
                       trajectory=events, token_usage=result.get("token_usage", {}))
    for ar in audit_results:
        emit("audit", "platform", ar.to_dict())
    acc_res = {"passed": all(a.passed for a in audit_results),
               "exit_code": 0 if audit_results[0].passed else 1,
               "output_tail": audit_results[0].detail,
               "command": test_cmd}
    # 构建结构化审计报告
    audit_report = AuditReport(
        task_id=task_id, agent_id="miniswe",
        model="mock" if settings.mode == "mock" else settings.model_name,
        started_at=t0, finished_at=time.time(),
        audit_levels=[a.to_dict() for a in audit_results],
        changed_files=[f for f in changed if not f.endswith(".pyc")],
        model_calls=result.get("token_usage", {}).get("model_calls", 0),
        token_usage=result.get("token_usage", {}),
        agent_submission=result.get("submission", ""),
    )

    patch = wsmod.extract_patch(ws)
    changed = wsmod.changed_files(ws)
    duration_ms = int((time.monotonic() - t0) * 1000)
    final = {
        "agent_exit_status": result.get("exit_status", ""),
        "agent_submission": result.get("submission", ""),
        "acceptance": acc_res,
        "audit_levels": audit_report.audit_levels,
        "audit_summary": audit_report.summary_line(),
        "changed_files": [f for f in changed if not f.endswith(".pyc")],
        "patch": patch[:20000],
        "model": settings.model_name if settings.mode == "real" else "mock",
        "mode": settings.mode,
    }
    ok = acc_res["passed"]
    wsmod.cleanup_workspace(ws, keep=not ok)
    return _finish("SUCCEEDED" if ok else "FAILED",
                   "ACCEPTANCE_PASSED" if ok else "ACCEPTANCE_FAILED",
                   final_output=final, trajectory=events,
                   token_usage=result.get("token_usage", {}),
                   duration_ms=duration_ms)


def result_preview(ws: Path) -> dict:
    try:
        return {"patch": wsmod.extract_patch(ws)[:8000],
                "changed_files": wsmod.changed_files(ws)}
    except Exception:
        return {}


def _run_real(ws: Path, problem: str, goal: dict, emit) -> dict:
    """真实模式：上游 DefaultAgent + LitellmModel + 隔离 Environment（程序化 API）。"""
    validate_real_mode()
    from minisweagent.agents.default import DefaultAgent
    from minisweagent.environments.local import LocalEnvironment
    from minisweagent.models.litellm_model import LitellmModel

    model_kwargs: dict = {"api_key": settings.api_key}
    if settings.api_base:
        model_kwargs["api_base"] = settings.api_base
    model = LitellmModel(model_name=settings.model_name, model_kwargs=model_kwargs)
    # 上游默认提示词/模板来自 default.yaml 的 agent 节（实测 v2.4.6 装配方式）
    import yaml as _yaml
    from minisweagent.config import builtin_config_dir as _cfgdir
    _agent_cfg = _yaml.safe_load((_cfgdir / "mini_textbased.yaml").read_text())["agent"]

    # 环境注入当前解释器 PATH：Agent 运行 python/pytest 时命中带依赖的 venv
    import os as _os
    import sys as _sys
    _path = _os.pathsep.join([_os.path.dirname(_sys.executable), _os.environ.get("PATH", "")])
    if settings.exec_env == "docker":
        from minisweagent.environments.docker import DockerEnvironment
        env = DockerEnvironment(image=settings.docker_image, cwd="/repo",
                                env={"HOME": "/root", "PATH": "/usr/local/bin:/usr/bin"},
                                timeout=settings.cmd_timeout)
    else:
        env = LocalEnvironment(cwd=str(ws), env={"PATH": _path}, timeout=settings.cmd_timeout)

    cfg = dict(_agent_cfg)
    cfg.update({
        "step_limit": int(goal.get("step_limit", settings.step_limit)),
        "cost_limit": settings.cost_limit,
        "wall_time_limit_seconds": int(goal.get("time_limit_s", settings.wall_time_limit)),
    })
    emit("agent_start", "adapter", {"model": settings.model_name, "exec_env": settings.exec_env,
                                    "step_limit": cfg["step_limit"]})
    agent = DefaultAgent(model, env, **cfg)
    out = agent.run(task=problem)
    emit("agent_exit", "upstream", out)
    return {
        "exit_status": out.get("exit_status", ""),
        "submission": out.get("submission", ""),
        "token_usage": {"cost_usd": getattr(agent, "cost", 0.0),
                        "model_calls": getattr(agent, "n_calls", 0)},
    }


def _run_mock(ws: Path, fixture: str, problem: str, emit) -> dict:
    """Mock 模式：确定性修复 fixture 预置 bug（用于测试与 CI，不依赖 API Key）。

    明确声明：此路径不经过上游 Agent，事件 source=mock。
    """
    fix = ws / "fix.json"
    if not fix.exists():
        emit("mock", "mock", {"note": "fixture 无预置修复，模拟 Agent 未提交修改"})
        return {"exit_status": "submitted", "submission": "（mock）未修改"}
    spec = json.loads(fix.read_text())
    for rel, content in spec["files"].items():
        (ws / rel).write_text(content)
    emit("mock_apply_fix", "mock", {"files": list(spec["files"])})
    return {"exit_status": "submitted", "submission": "（mock）已按预置修复应用变更",
            "token_usage": {"cost_usd": 0.0, "model_calls": 0}}
