"""评测执行器：逐样例创建真实任务、等待终态、评分并聚合（§8.2 批量模式）。

样例任务带幂等键 eval:{eval_run_id}:{seq}——评测中断重跑不会产生重复任务。
"""

import time

from app.eval.metrics import aggregate
from app.eval.rules import apply_rules
from app.platform_api import PlatformClient, PlatformError


class EvaluationState:
    """一次评测运行的可查询状态（进程内存；丢失由 Go 侧置 FAILED 可重跑）。"""

    def __init__(self, eval_run_id: str, agent_version: str, run_config: dict,
                 cases: list[dict]):
        self.eval_run_id = eval_run_id
        self.agent_version = agent_version
        self.run_config = run_config or {}
        self.cases = cases
        self.status = "PENDING"
        self.results: list[dict] = []
        self.metrics: dict | None = None
        self.error: dict | None = None

    def digest(self) -> dict:
        return {
            "eval_run_id": self.eval_run_id, "status": self.status,
            "metrics": self.metrics, "results": self.results, "error": self.error,
        }


_TERMINAL = ("SUCCEEDED", "FAILED", "CANCELLED")


def execute_evaluation(state: EvaluationState, platform: PlatformClient) -> EvaluationState:
    """同步执行整轮评测；任何样例异常记 ERROR 不阻塞其余样例。"""
    state.status = "RUNNING"
    case_timeout = int(state.run_config.get("case_timeout_seconds", 120))
    poll = 0.3
    allowed_default = None

    for case in state.cases:
        seq = case.get("seq", len(state.results) + 1)
        input_task = case.get("input_task") or {}
        allowed = case.get("allowed_tools") or input_task.get("allowed_tools") or []
        started = time.monotonic()
        try:
            task = platform.create_task(
                input_task, idempotency_key=f"eval:{state.eval_run_id}:{seq}")
            # 轮询至终态（样例级超时保护，超时记 ERROR）
            deadline = time.monotonic() + case_timeout
            while time.monotonic() < deadline:
                task = platform.get_task(task["id"])
                if task.get("status") in _TERMINAL:
                    break
                time.sleep(poll)
            else:
                state.results.append({
                    "case_seq": seq, "case_id": case.get("case_id"),
                    "task_id": task.get("id"), "task_status": "ERROR",
                    "status": "ERROR", "all_rules_pass": False, "scores": [],
                    "error_code": "CASE_TIMEOUT", "duration_ms": None,
                    "tool_calls": [], "steps": [], "attempt_count": task.get("attempt_count", 0),
                    "token_usage": {},
                })
                continue

            trace = platform.get_trace(task["id"])
            ctx = {"task": task, **trace}
            scores = apply_rules(case.get("scoring_rules") or [], ctx)
            state.results.append(_record(seq, case, task, trace, scores))
        except PlatformError as e:
            state.results.append({
                "case_seq": seq, "case_id": case.get("case_id"), "task_id": None,
                "task_status": "ERROR", "status": "ERROR", "all_rules_pass": False,
                "scores": [], "error_code": e.code, "duration_ms": None,
                "tool_calls": [], "steps": [], "attempt_count": 0, "token_usage": {},
            })

    state.metrics = aggregate(state.results, state.run_config.get("pricing"))
    state.status = "SUCCEEDED"
    return state


def _record(seq, case, task, trace, scores) -> dict:
    """把任务终态 + 轨迹 + 规则结果整理为一条样例结果。"""
    allowed = set(case.get("allowed_tools") or (task.get("allowed_tools") or []))
    calls = [
        {"tool": c.get("tool_name"), "status": c.get("status"),
         "allowed": c.get("tool_name") in allowed}
        for c in trace.get("tool_calls") or []
    ]
    steps = [{"status": s.get("status")} for s in trace.get("steps") or []]
    usage = {}
    for run in trace.get("agent_runs") or []:
        u = run.get("token_usage") or {}
        for k in ("prompt_tokens", "completion_tokens", "total_tokens"):
            usage[k] = usage.get(k, 0) + (u.get(k) or 0)

    duration = None
    if task.get("started_at") and task.get("finished_at"):
        from datetime import datetime

        def _p(ts):
            return datetime.fromisoformat(ts.replace("Z", "+00:00"))
        duration = int((_p(task["finished_at"]) - _p(task["started_at"])).total_seconds() * 1000)

    return {
        "case_seq": seq, "case_id": case.get("case_id"), "task_id": task.get("id"),
        "task_status": task.get("status"),
        "status": "SUCCEEDED" if task.get("status") == "SUCCEEDED" else "FAILED",
        "all_rules_pass": bool(scores) and all(s["pass"] for s in scores),
        "scores": scores, "duration_ms": duration,
        "tool_calls": calls, "steps": steps,
        "attempt_count": task.get("attempt_count", 0), "token_usage": usage,
    }
