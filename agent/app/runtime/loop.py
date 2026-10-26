"""Agent 执行循环：计划 → 工具动作 → 终止，§7.2 限制全表 enforcement。"""

import json
import time

from app.config import RunLimits
from app.gwclient import GatewayClient, GatewayError
from app.models.base import (
    BaseLLM, InvalidOutputError, ModelError, ModelUsage,
)
from app.runtime.planner import parse_action, validate_plan
from app.runtime.trajectory import Trajectory

# 终止原因 → 运行状态
_TERMINAL = {
    "COMPLETED": "SUCCEEDED",
    "MAX_ITERATIONS": "FAILED",
    "MAX_TOOL_CALLS": "FAILED",
    "MAX_CONTEXT": "FAILED",
    "TIMEOUT": "FAILED",
    "MODEL_ERROR": "FAILED",
    "INVALID_PLAN": "FAILED",
    "CANCELLED": "CANCELLED",
}


class AgentRun:
    """一次运行的可查询状态（由 RunStore 持有）。"""

    def __init__(self, run_id: str, task_id: str, trace_id: str, goal: dict,
                 allowed_tools: list[str], limits: RunLimits, model_config: dict,
                 agent_version: str):
        self.id = run_id
        self.task_id = task_id
        self.trace_id = trace_id
        self.goal = goal
        self.allowed_tools = allowed_tools or []
        self.limits = limits
        self.model_config = model_config or {}
        self.agent_version = agent_version
        self.status = "PENDING"
        self.plan: list[dict] | None = None
        self.plan_valid: bool | None = None
        self.final_output: dict | None = None
        self.finish_reason: str | None = None
        self.error: dict | None = None
        self.token_usage = ModelUsage()
        self.trajectory = Trajectory()
        self.cancelled = False

    def digest(self) -> dict:
        return {
            "id": self.id, "task_id": self.task_id, "status": self.status,
            "finish_reason": self.finish_reason,
            "final_output": self.final_output,
            "plan": self.plan, "plan_valid": self.plan_valid,
            "trajectory_digest": self.trajectory.digest(),
            "token_usage": {
                "prompt_tokens": self.token_usage.prompt_tokens,
                "completion_tokens": self.token_usage.completion_tokens,
                "total_tokens": self.token_usage.total_tokens,
            },
            "error": self.error,
        }


def _finish(run: AgentRun, reason: str, error: dict | None = None) -> None:
    run.status = _TERMINAL[reason]
    run.finish_reason = reason
    if error and run.status == "FAILED":
        run.error = error


def execute_run(run: AgentRun, model: BaseLLM, gw: GatewayClient) -> AgentRun:
    """同步执行一次运行并驱动到终态；任何异常路径都必须落到明确 finish_reason。"""
    limits: RunLimits = run.limits
    run.status = "RUNNING"
    deadline = time.monotonic() + limits.max_duration_seconds
    seen_calls: set[tuple[str, str]] = set()   # 重复调用检测

    def _context_prompt(payload: dict) -> str:
        text = json.dumps(payload, ensure_ascii=False)
        if len(text.encode()) > limits.context_max_bytes:
            raise _ContextOverflow()
        return text

    try:
        # ---- 计划阶段 ----
        run.trajectory.add("plan", "generate_plan", "INFO", {"goal_keys": list(run.goal)[:8]})
        item = run.trajectory.add("model_call", "plan")
        try:
            resp = model.generate(
                _context_prompt({"phase": "plan", "goal": run.goal,
                                 "allowed_tools": run.allowed_tools}),
                timeout=limits.model_timeout_seconds)
        except _ContextOverflow:
            raise
        except ModelError as e:
            item.done("FAILED", {"kind": e.kind})
            run.trajectory.model_calls += 1
            _finish(run, "MODEL_ERROR", {"code": e.kind, "message": str(e)})
            return run
        run.trajectory.model_calls += 1
        run.token_usage.add(resp.usage)
        item.done("SUCCEEDED", {"request_id": resp.request_id, "model": resp.model})

        try:
            action = parse_action(resp.content)
            plan = validate_plan(action, limits.max_plan_steps)
        except InvalidOutputError as e:
            run.plan_valid = False
            run.trajectory.add("plan", "validate_plan", "FAILED", {"reason": str(e)})
            _finish(run, "INVALID_PLAN", {"code": "INVALID_PLAN", "message": str(e)})
            return run
        run.plan_valid = True
        run.plan = [s.model_dump() for s in plan.steps]
        run.trajectory.add("plan", "validate_plan", "SUCCEEDED", {"steps": len(run.plan)})

        # ---- 执行循环 ----
        for iteration in range(limits.max_iterations):
            if run.cancelled:
                _finish(run, "CANCELLED")
                return run
            if time.monotonic() > deadline:
                _finish(run, "TIMEOUT")
                return run

            payload = {
                "phase": "act", "iteration": iteration, "goal": run.goal,
                "plan": run.plan, "allowed_tools": run.allowed_tools,
                "tool_results": run.trajectory.tool_results,
            }
            item = run.trajectory.add("model_call", f"iter-{iteration}")
            try:
                resp = model.generate(_context_prompt(payload),
                                      timeout=limits.model_timeout_seconds)
            except _ContextOverflow:
                raise
            except ModelError as e:
                item.done("FAILED", {"kind": e.kind})
                run.trajectory.model_calls += 1
                _finish(run, "MODEL_ERROR", {"code": e.kind, "message": str(e)})
                return run
            run.trajectory.model_calls += 1
            run.token_usage.add(resp.usage)
            item.done("SUCCEEDED", {"request_id": resp.request_id})

            action = parse_action(resp.content)

            if action["type"] == "final":
                run.final_output = action.get("output") or {}
                run.trajectory.add("note", "final", "SUCCEEDED",
                                   {"summary": action.get("summary", "")})
                _finish(run, "COMPLETED")
                return run

            if action["type"] != "tool_call":
                item.done("FAILED", {"reason": "未知动作类型"})
                _finish(run, "MODEL_ERROR",
                        {"code": "INVALID_OUTPUT", "message": f"未知动作类型 {action['type']}"})
                return run

            tool, params = action.get("tool", ""), action.get("params") or {}
            # 重复调用检测：同 (tool, params) 只执行一次
            key = (tool, json.dumps(params, sort_keys=True, ensure_ascii=False))
            if key in seen_calls:
                run.trajectory.add("tool_call", tool, "SKIPPED", {"reason": "duplicate"})
                continue
            # 权限前置提示：未授权工具不发起网关调用（网关仍会独立校验）
            if tool not in run.allowed_tools:
                run.trajectory.add("tool_call", tool, "SKIPPED",
                                   {"reason": "not_allowed"})
                run.trajectory.record_tool_result(tool, "REJECTED", None, "PERMISSION_DENIED")
                continue
            if run.trajectory.tool_calls >= limits.max_tool_calls:
                _finish(run, "MAX_TOOL_CALLS")
                return run

            seen_calls.add(key)
            t_item = run.trajectory.add("tool_call", tool)
            try:
                result = gw.invoke(run.task_id, tool, params,
                                   idempotency_key=f"{run.task_id}:{run.id}:{len(seen_calls)}")
            except GatewayError as e:
                print(f"[loop] 网关调用失败 tool={tool} code={e.code} "
                      f"status={e.status} msg={e}", flush=True)
                t_item.done("FAILED", {"code": e.code})
                run.trajectory.record_tool_result(tool, "FAILED", None, e.code)
                # 网关拒绝/不可达按运行失败处理（AGENT 侧不擅自重试受治理调用）
                _finish(run, "MODEL_ERROR", {"code": e.code, "message": str(e)})
                return run
            status = result.get("status", "FAILED")
            t_item.done("SUCCEEDED" if status == "SUCCEEDED" else "FAILED",
                        {"call_id": result.get("call_id"), "status": status})
            run.trajectory.record_tool_result(
                tool, status, result.get("call_id"), result.get("error_code", ""))

        # 迭代耗尽仍未终止
        _finish(run, "MAX_ITERATIONS")
        return run

    except _ContextOverflow:
        _finish(run, "MAX_CONTEXT", {"code": "MAX_CONTEXT", "message": "上下文超过单次上限"})
        return run


class _ContextOverflow(Exception):
    """内部信号：prompt 超过上下文上限。"""
