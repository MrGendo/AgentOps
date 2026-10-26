"""T6 API：异步语义、令牌校验、404 包络、取消标记。"""

import time


def test_health_open(no_token_client):
    resp = no_token_client.get("/internal/v1/health")
    assert resp.status_code == 200 and resp.json()["status"] == "ok"


def test_agent_runs_require_token(client):
    resp = client.post("/internal/v1/agent-runs",
                       json={"task_id": "t" * 36, "goal": {"topic": "通用排查"}},
                       headers={"X-Service-Token": "wrong"})
    assert resp.status_code == 401


def test_agent_run_async_lifecycle(client):
    """202 受理 → 后台执行 → 终态可查询（Mock 模型 + 无网关失败路径）。"""
    # 注意：后台线程会尝试连真实网关（默认 localhost:8080）。
    # 未启动 Go 时工具调用会失败 —— 这里只验证异步语义与终态可见性。
    resp = client.post("/internal/v1/agent-runs",
                       json={"task_id": "t" * 36, "goal": {"topic": "通用排查"},
                             "allowed_tools": ["knowledge_search"]},
                       headers={"X-Service-Token": "test-token"})
    assert resp.status_code == 202
    run_id = resp.json()["data"]["id"]
    assert resp.json()["data"]["status"] in ("PENDING", "RUNNING")

    # 轮询至终态（Mock 脚本会先尝试工具调用，网关不可达时终态为 FAILED）
    for _ in range(50):
        got = client.get(f"/internal/v1/agent-runs/{run_id}",
                         headers={"X-Service-Token": "test-token"})
        assert got.status_code == 200
        body = got.json()["data"]
        if body["status"] in ("SUCCEEDED", "FAILED", "CANCELLED"):
            break
        time.sleep(0.1)
    assert body["status"] in ("SUCCEEDED", "FAILED")
    assert body["finish_reason"]


def test_agent_run_not_found(client):
    resp = client.get("/internal/v1/agent-runs/missing",
                      headers={"X-Service-Token": "test-token"})
    assert resp.status_code == 404
    assert resp.json()["detail"]["code"] == "RUN_NOT_FOUND"


def test_cancel_marks_run(client):
    resp = client.post("/internal/v1/agent-runs",
                       json={"task_id": "t" * 36, "goal": {"topic": "通用排查"}},
                       headers={"X-Service-Token": "test-token"})
    run_id = resp.json()["data"]["id"]
    resp = client.post(f"/internal/v1/agent-runs/{run_id}/cancel",
                       headers={"X-Service-Token": "test-token"})
    assert resp.status_code == 202 and resp.json()["data"]["cancel_requested"] is True
