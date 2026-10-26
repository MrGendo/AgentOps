"""Go 平台公开 API 客户端：建任务 / 查任务 / 查轨迹。
内部调用禁走任何代理（trust_env=False，阶段 C 教训）。"""

import httpx


class PlatformError(Exception):
    def __init__(self, code: str, message: str, status: int = 0):
        super().__init__(f"{code}: {message}")
        self.code = code
        self.status = status


class PlatformClient:
    def __init__(self, base_url: str, timeout: float = 10.0,
                 transport: httpx.BaseTransport | None = None):
        self._client = httpx.Client(base_url=base_url.rstrip("/"), timeout=timeout,
                                    transport=transport, trust_env=False)

    def create_task(self, input_task: dict, idempotency_key: str | None = None) -> dict:
        body = dict(input_task)
        if idempotency_key:
            body["idempotency_key"] = idempotency_key
        resp = self._client.post("/api/v1/tasks", json=body)
        if resp.status_code not in (200, 201):
            raise PlatformError("TASK_CREATE_FAILED", resp.text[:200], resp.status_code)
        return resp.json()["data"]

    def get_task(self, task_id: str) -> dict:
        resp = self._client.get(f"/api/v1/tasks/{task_id}")
        if resp.status_code != 200:
            raise PlatformError("TASK_FETCH_FAILED", resp.text[:200], resp.status_code)
        return resp.json()["data"]

    def get_trace(self, task_id: str) -> dict:
        resp = self._client.get(f"/api/v1/tasks/{task_id}/trace")
        if resp.status_code != 200:
            raise PlatformError("TRACE_FETCH_FAILED", resp.text[:200], resp.status_code)
        return resp.json()["data"]

    def close(self) -> None:
        self._client.close()
