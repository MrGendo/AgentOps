"""T4 评测执行器：桩 Go（MockTransport）验证建任务/轮询/评分全流程。"""

import json

import httpx
import pytest

from app.eval.runner import EvaluationState, execute_evaluation
from app.platform_api import PlatformClient


def stub_platform(handler) -> PlatformClient:
    return PlatformClient("http://go.test", transport=httpx.MockTransport(handler))


def test_full_evaluation_flow():
    """两个样例：一成一败；断言幂等键、评分与指标。"""
    seen_keys = []

    def handler(request: httpx.Request) -> httpx.Response:
        path = request.url.path
        if path == "/api/v1/tasks" and request.method == "POST":
            body = json.loads(request.content)
            seen_keys.append(body.get("idempotency_key"))
            return httpx.Response(201, json={"data": {"id": "t" + str(len(seen_keys)),
                                                      "status": "PENDING"}})
        if path.startswith("/api/v1/tasks/") and path.endswith("/trace"):
            return httpx.Response(200, json={"data": {
                "steps": [{"status": "SUCCEEDED", "output_ref": '{"value": 1}'}],
                "tool_calls": [], "agent_runs": []}})
        if path.startswith("/api/v1/tasks/"):
            n = int(path.split("/")[-1][1:])
            status = "SUCCEEDED" if n == 1 else "FAILED"
            return httpx.Response(200, json={"data": {
                "id": path.split("/")[-1], "status": status,
                "finish_reason": "succeeded" if n == 1 else "failed",
                "attempt_count": 1, "started_at": "2026-10-26T10:00:00Z",
                "finished_at": "2026-10-26T10:00:01Z"}})
        return httpx.Response(404)

    state = EvaluationState("run-x", "v1", {}, [
        {"case_id": "c1", "seq": 1,
         "input_task": {"task_type": "mock.echo", "input": {}},
         "scoring_rules": [{"type": "finished_normally"}]},
        {"case_id": "c2", "seq": 2,
         "input_task": {"task_type": "mock.fail", "input": {}},
         "scoring_rules": [{"type": "finished_normally"}]},
    ])
    execute_evaluation(state, stub_platform(handler))

    assert state.status == "SUCCEEDED"
    assert seen_keys == ["eval:run-x:1", "eval:run-x:2"]  # 幂等键稳定
    assert state.metrics["task_success_rate"]["value"] == 0.5
    assert state.results[0]["all_rules_pass"] is True
    assert state.results[1]["all_rules_pass"] is False


def test_case_platform_error_records_error():
    def handler(request: httpx.Request) -> httpx.Response:
        return httpx.Response(500, json={"error": {"code": "X", "message": "boom"}})

    state = EvaluationState("run-y", "v1", {}, [
        {"case_id": "c1", "seq": 1, "input_task": {"task_type": "mock.echo"},
         "scoring_rules": []}])
    execute_evaluation(state, stub_platform(handler))
    assert state.results[0]["status"] == "ERROR"
    assert state.results[0]["error_code"] == "TASK_CREATE_FAILED"
    assert state.status == "SUCCEEDED"  # 样例失败不阻塞评测完成
