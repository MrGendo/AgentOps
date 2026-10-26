"""T4 网关客户端：令牌透传、结果解析、错误映射（MockTransport，无真实网关）。"""

import httpx
import pytest

from app.gwclient import GatewayClient, GatewayError


def make_client(handler) -> GatewayClient:
    return GatewayClient("http://go.test", "test-token",
                         transport=httpx.MockTransport(handler))


def test_invoke_success_and_token_header():
    captured = {}

    def handler(request: httpx.Request) -> httpx.Response:
        captured["auth"] = request.headers.get("X-Service-Token")
        return httpx.Response(200, json={"data": {"call_id": "c1", "status": "SUCCEEDED",
                                                  "result": {"value": 1}}})

    gw = make_client(handler)
    res = gw.invoke("t" * 36, "calculator", {"expression": "1+1"})
    assert res["status"] == "SUCCEEDED" and captured["auth"] == "test-token"


def test_invoke_error_envelope():
    def handler(request: httpx.Request) -> httpx.Response:
        return httpx.Response(403, json={"error": {"code": "PERMISSION_DENIED",
                                                   "message": "未授权", "retryable": False}})

    gw = make_client(handler)
    with pytest.raises(GatewayError) as e:
        gw.invoke("t" * 36, "shell", {})
    assert e.value.code == "PERMISSION_DENIED" and e.value.status == 403


def test_invoke_unreachable():
    def handler(request: httpx.Request) -> httpx.Response:
        raise httpx.ConnectError("拒绝连接")

    gw = make_client(handler)
    with pytest.raises(GatewayError, match="GATEWAY_UNREACHABLE"):
        gw.invoke("t" * 36, "calculator", {})
