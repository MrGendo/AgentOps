"""T5 执行循环：限制 enforcement 全表、重复调用检测、终止原因。"""

import json

import httpx
import pytest

from app.config import RunLimits, Settings
from app.gwclient import GatewayClient
from app.models.base import BaseLLM, ModelResponse, ModelUsage, InvalidOutputError
from app.runtime.loop import AgentRun, execute_run


class ScriptLLM(BaseLLM):
    """测试用脚本模型：按给定脚本顺序输出。"""

    name = "script"

    def __init__(self, script: list):
        self.script = script
        self.i = 0

    def generate(self, prompt, *, timeout=None):
        if self.i >= len(self.script):
            raise InvalidOutputError("脚本耗尽")
        out = self.script[self.i]
        self.i += 1
        if isinstance(out, Exception):
            raise out
        return ModelResponse(content=json.dumps(out), usage=ModelUsage(1, 1, 2),
                             request_id="r", model="script")


def make_run(limits: RunLimits | None = None, allowed=None) -> AgentRun:
    s = Settings()
    return AgentRun(run_id="run1", task_id="t" * 36, trace_id="tr",
                    goal={"topic": "通用"}, allowed_tools=allowed or ["calculator"],
                    limits=(limits or RunLimits()).resolved(s),
                    model_config={}, agent_version="test")


def ok_gateway(results=None):
    """总是成功的网关桩。"""

    def handler(request: httpx.Request) -> httpx.Response:
        return httpx.Response(200, json={"data": {"call_id": "c", "status": "SUCCEEDED",
                                                  "result": {}}})

    return GatewayClient("http://x", "t", transport=httpx.MockTransport(handler))


def test_completed_run_with_tools():
    script = [
        {"type": "plan", "steps": [{"name": "s1"}]},
        {"type": "tool_call", "tool": "calculator", "params": {"expression": "1+1"}},
        {"type": "final", "output": {"answer": 42}, "summary": "done"},
    ]
    run = make_run()
    execute_run(run, ScriptLLM(script), ok_gateway())
    assert run.status == "SUCCEEDED" and run.finish_reason == "COMPLETED"
    assert run.final_output == {"answer": 42}
    assert run.trajectory.tool_calls == 1
    assert run.token_usage.total_tokens == 6  # 3 次模型调用 × 2


def test_max_iterations_enforced():
    # 模型永远要调工具 → 迭代耗尽
    script = [{"type": "plan", "steps": [{"name": "s"}]}] + [
        {"type": "tool_call", "tool": "calculator", "params": {"expression": str(i)}}
        for i in range(20)
    ]
    run = make_run(RunLimits(max_iterations=3))
    execute_run(run, ScriptLLM(script), ok_gateway())
    assert run.status == "FAILED" and run.finish_reason == "MAX_ITERATIONS"


def test_max_tool_calls_enforced():
    script = [{"type": "plan", "steps": [{"name": "s"}]}] + [
        {"type": "tool_call", "tool": "calculator", "params": {"expression": str(i)}}
        for i in range(10)
    ]
    run = make_run(RunLimits(max_tool_calls=2))
    execute_run(run, ScriptLLM(script), ok_gateway())
    assert run.finish_reason == "MAX_TOOL_CALLS"


def test_duplicate_tool_call_skipped():
    script = [
        {"type": "plan", "steps": [{"name": "s"}]},
        {"type": "tool_call", "tool": "calculator", "params": {"expression": "1+1"}},
        {"type": "tool_call", "tool": "calculator", "params": {"expression": "1+1"}},  # 重复
        {"type": "final", "output": {}},
    ]
    run = make_run()
    execute_run(run, ScriptLLM(script), ok_gateway())
    assert run.status == "SUCCEEDED"
    assert run.trajectory.tool_calls == 1  # 重复调用被跳过，未到达网关
    skipped = [i for i in run.trajectory.items if i.status == "SKIPPED"]
    assert skipped and skipped[0].detail["reason"] == "duplicate"


def test_unauthorized_tool_not_forwarded():
    script = [
        {"type": "plan", "steps": [{"name": "s"}]},
        {"type": "tool_call", "tool": "mock_http", "params": {"mode": "success"}},  # 未授权
        {"type": "final", "output": {}},
    ]
    run = make_run(allowed=["calculator"])
    execute_run(run, ScriptLLM(script), ok_gateway())
    assert run.trajectory.tool_calls == 1  # 记为 REJECTED，但未发起调用
    assert run.trajectory.tool_results[0]["status"] == "REJECTED"


def test_model_error_terminates():
    from app.models.base import RateLimitedError

    script = [RateLimitedError("限流")]
    run = make_run()
    execute_run(run, ScriptLLM(script), ok_gateway())
    assert run.status == "FAILED" and run.error["code"] == "RATE_LIMITED"


def test_invalid_plan_terminates():
    script = [{"type": "plan", "steps": []}]  # 空计划非法
    run = make_run()
    execute_run(run, ScriptLLM(script), ok_gateway())
    assert run.finish_reason == "INVALID_PLAN" and run.plan_valid is False


def test_timeout_enforced():
    script = [{"type": "plan", "steps": [{"name": "s"}]}] + [
        {"type": "tool_call", "tool": "calculator", "params": {"expression": str(i)}}
        for i in range(5)
    ]
    run = make_run(RunLimits(max_duration_seconds=0))  # 立即超时
    execute_run(run, ScriptLLM(script), ok_gateway())
    assert run.finish_reason == "TIMEOUT"


def test_context_limit_enforced():
    big = "x" * 50000
    script = [{"type": "plan", "steps": [{"name": "s", "description": big}]}]
    run = make_run(RunLimits(context_max_bytes=1024))
    execute_run(run, ScriptLLM(script), ok_gateway())
    assert run.finish_reason == "MAX_CONTEXT"


def test_cancel_flag_stops_at_checkpoint():
    script = [{"type": "plan", "steps": [{"name": "s"}]}] + [
        {"type": "tool_call", "tool": "calculator", "params": {"expression": str(i)}}
        for i in range(5)
    ]
    run = make_run()
    run.cancelled = True  # 在首轮检查点前置位
    execute_run(run, ScriptLLM(script), ok_gateway())
    assert run.status == "CANCELLED" and run.finish_reason == "CANCELLED"
