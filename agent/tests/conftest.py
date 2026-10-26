"""测试夹具：禁用令牌校验的客户端与通用工厂。"""

import pytest
from fastapi.testclient import TestClient

import app.config as config


@pytest.fixture()
def client():
    """令牌透传的测试客户端。"""
    from app.main import app

    config.settings.service_token = "test-token"
    return TestClient(app)


@pytest.fixture()
def no_token_client():
    """令牌置空的测试客户端（健康检查开放）。"""
    from app.main import app

    config.settings.service_token = ""
    return TestClient(app)
