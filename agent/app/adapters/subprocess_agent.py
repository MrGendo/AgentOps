"""通用子进程 Agent 适配器：黑盒模式。

给 Agent 一个干净的工作区，让它以独立进程运行，
结束后取 git diff 并执行四层审计。不关心 Agent 内部实现。
"""

import os
import subprocess
import sys
import time
from pathlib import Path

from app.adapters.base import finish
from app.audit.multi_level import run_audit
from app.audit.report import AuditReport
from app.swea import workspace as wsmod


class SubprocessAgentAdapter:
    """以子进程方式运行任意 Agent，实现进程级隔离。"""

    def __init__(self, agent_id: str, display_name: str, description: str,
                 script_path: str, version: str = "unknown"):
        self.agent_id = agent_id
        self.display_name = display_name
        self.description = description
        self.script_path = script_path
        self._version = version

    def version(self) -> str:
        return self._version

    def availability(self) -> dict:
        if not Path(self.script_path).exists():
            return {"available": False, "configuration_status": "missing_dependency",
                    "reason": f"脚本不存在: {self.script_path}"}
        key = os.getenv("AGENTOPS_SWE_API_KEY", "")
        if not key:
            return {"available": False, "configuration_status": "incomplete",
                    "reason": "缺少 AGENTOPS_SWE_API_KEY"}
        return {"available": True, "configuration_status": "complete",
                "reason": f"模型 {os.getenv('AGENTOPS_SWE_MODEL', 'N/A')}"}

    def validate(self, goal: dict) -> str | None:
        if not goal.get("problem") or not goal.get("fixture"):
            return "缺少 problem 或 fixture"
        return None

    def execute(self, task_id: str, goal: dict, on_event=None) -> dict:
        events: list[dict] = []

        def emit(kind, data, source="adapter"):
            ev = {"kind": kind, "source": source, "data": data}
            events.append(ev)
            if on_event:
                on_event(ev)

        av = self.availability()
        if not av["available"]:
            return finish("FAILED", "AGENT_UNAVAILABLE",
                          final_output={"error": av["reason"], "agent_id": self.agent_id},
                          trajectory=events, token_usage={}, agent_id=self.agent_id)

        emit("stage", {"stage": "preparing_workspace"})
        try:
            ws = wsmod.prepare_workspace(task_id, goal["fixture"])
        except Exception as e:
            return finish("FAILED", "INVALID_INPUT", final_output={"error": str(e)},
                          trajectory=events, token_usage={}, agent_id=self.agent_id)

        t0 = time.monotonic()
        emit("agent_start", {"exec": "subprocess"}, source=self.agent_id)

        # 子进程运行 Agent（完全隔离，我们不管内部）
        args = [str(ws), goal["problem"],
                os.getenv("AGENTOPS_SWE_MODEL", ""),
                os.getenv("AGENTOPS_SWE_API_KEY", ""),
                os.getenv("AGENTOPS_SWE_API_BASE", ""),
                str(goal.get("time_limit_s", 600))]
        if self.script_path.endswith(".py"):
            cmd = [sys.executable, self.script_path] + args
        else:
            cmd = ["bash", self.script_path] + args
        try:
            proc = subprocess.run(
                cmd, capture_output=True, text=True,
                timeout=goal.get("time_limit_s", 600),
                cwd=str(ws),  # 让子进程以工作区为 cwd
            )
            exit_code = proc.returncode
            stderr_tail = proc.stderr[-500:] if proc.stderr else ""
            emit("agent_exit", {"exit_code": exit_code, "stderr": stderr_tail},
                 source=self.agent_id)
        except subprocess.TimeoutExpired:
            emit("crash", {"error": "子进程超时"}, source=self.agent_id)
            return finish("FAILED", "TIMEOUT",
                          final_output={"error": "Agent 子进程超时"},
                          trajectory=events, token_usage={}, agent_id=self.agent_id)
        except Exception as e:
            emit("crash", {"error": str(e)}, source=self.agent_id)
            return finish("FAILED", "AGENT_CRASHED",
                          final_output={"error": str(e)},
                          trajectory=events, token_usage={}, agent_id=self.agent_id)

        # Agent 子进程已退出 → 我们接管工作区 → 四层审计
        emit("stage", {"stage": "running_audit"})
        changed = [f for f in wsmod.changed_files(ws) if not f.endswith((".pyc", ".pyo"))]
        try:
            audit_results = run_audit(
                ws, goal.get("test_cmd", "python -m pytest -q"), changed)
        except Exception as e:
            return finish("FAILED", "AUDIT_ERROR", final_output={"error": str(e)},
                          trajectory=events, token_usage={}, agent_id=self.agent_id)
        for ar in audit_results:
            emit("audit", ar.to_dict(), source="platform")

        all_passed = all(a.passed for a in audit_results)
        patch = wsmod.extract_patch(ws)[:20000]

        report = AuditReport(
            task_id=task_id, agent_id=self.agent_id,
            model=os.getenv("AGENTOPS_SWE_MODEL", ""),
            started_at=t0, finished_at=time.time(),
            audit_levels=[a.to_dict() for a in audit_results],
            changed_files=changed,
            patch_summary=patch[:200],
            agent_submission=stderr_tail,
        )

        wsmod.cleanup_workspace(ws, keep=not all_passed)
        return finish(
            "SUCCEEDED" if all_passed else "FAILED",
            "ACCEPTANCE_PASSED" if all_passed else "ACCEPTANCE_FAILED",
            final_output={
                "agent_exit_status": "submitted" if exit_code == 0 else f"exit_{exit_code}",
                "acceptance": {"passed": all_passed, "output_tail": audit_results[0].detail},
                "audit_levels": report.audit_levels,
                "audit_summary": report.summary_line(),
                "changed_files": changed,
                "patch": patch,
                "model": report.model,
                "mode": "subprocess",
            },
            trajectory=events, token_usage={},
            duration_ms=report.duration_ms, agent_id=self.agent_id,
        )
