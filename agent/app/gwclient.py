"""Go 工具网关客户端：服务令牌 + 超时 + 错误映射；TIMEOUT_UNKNOWN 原样透传。"""

import httpx


class GatewayError(Exception):
    """网关调用失败（连接层/非 2xx）；code 与 api-contract 错误码对齐。"""

    def __init__(self, code: str, message: str, status: int = 0):
        super().__init__(f"{code}: {message}")
        self.code = code
        self.status = status


class GatewayClient:
    def __init__(self, base_url: str, token: str, timeout: float = 10.0,
                 transport: httpx.BaseTransport | None = None):
        self._base = base_url.rstrip("/")
        # trust_env=False：内部平台流量绝不走任何环境/系统代理
        # （本机存在系统代理时，默认行为会把 127.0.0.1 请求送进代理导致 502）
        self._client = httpx.Client(
            headers={"X-Service-Token": token},
            timeout=timeout,
            transport=transport,
            trust_env=False,
        )

    def invoke(self, task_id: str, tool: str, params: dict,
               step_id: str | None = None, idempotency_key: str | None = None) -> dict:
        """调用一次工具，返回网关 ToolResult；网络/鉴权错误抛 GatewayError。"""
        body = {"task_id": task_id, "tool_name": tool, "params": params or {}}
        if step_id:
            body["step_id"] = step_id
        if idempotency_key:
            body["idempotency_key"] = idempotency_key
        try:
            resp = self._client.post(f"{self._base}/api/v1/tool-calls", json=body)
        except httpx.TimeoutException as e:
            raise GatewayError("GATEWAY_TIMEOUT", f"网关请求超时: {e}") from e
        except httpx.HTTPError as e:
            raise GatewayError("GATEWAY_UNREACHABLE", f"网关不可达: {e}") from e

        if resp.status_code in (200, 201):
            return resp.json().get("data", {})
        # 网关统一错误包络；非 JSON 响应也带上状态码与正文片段便于排障
        code = "GATEWAY_ERROR"
        message = f"HTTP {resp.status_code} {resp.text[:200]}"
        try:
            err = resp.json().get("error", {})
            if err:
                code, message = err.get("code", code), err.get("message", message)
        except ValueError:
            pass
        print(f"[gwclient] 调用失败 status={resp.status_code} body={resp.text[:300]!r}",
              flush=True)
        raise GatewayError(code, message, resp.status_code)

    def close(self) -> None:
        self._client.close()
