"""OpenAI 兼容适配器：仅在配置了 API Key 时注册；密钥不进日志与轨迹。"""

import json
import uuid

import httpx

from app.models.base import (
    BaseLLM, InvalidOutputError, ModelResponse, ModelUsage,
    ModelTimeoutError, ProviderError, RateLimitedError,
)


class OpenAICompatLLM(BaseLLM):
    name = "openai-compatible"

    def __init__(self, base_url: str, api_key: str, model: str) -> None:
        self._base = base_url.rstrip("/")
        self._key = api_key
        self.model = model

    def generate(self, prompt: str, *, timeout: float | None = None) -> ModelResponse:
        try:
            resp = httpx.post(
                f"{self._base}/chat/completions",
                headers={"Authorization": f"Bearer {self._key}"},
                json={
                    "model": self.model,
                    "messages": [{"role": "user", "content": prompt}],
                    "response_format": {"type": "json_object"},
                },
                timeout=timeout or 60.0,
            )
        except httpx.TimeoutException as e:
            raise ModelTimeoutError(f"模型请求超时: {e}") from e
        except httpx.HTTPError as e:
            raise ProviderError(f"模型服务异常: {e}") from e

        if resp.status_code == 429:
            raise RateLimitedError("模型限流")
        if resp.status_code >= 500:
            raise ProviderError(f"模型服务 {resp.status_code}")
        if resp.status_code != 200:
            raise ProviderError(f"模型请求失败 {resp.status_code}: {resp.text[:200]}")

        try:
            body = resp.json()
            content = body["choices"][0]["message"]["content"]
            u = body.get("usage", {})
            usage = ModelUsage(u.get("prompt_tokens", 0), u.get("completion_tokens", 0),
                               u.get("total_tokens", 0))
            return ModelResponse(content=content, usage=usage,
                                 request_id=body.get("id", uuid.uuid4().hex[:8]), model=self.model)
        except (KeyError, ValueError, json.JSONDecodeError) as e:
            raise InvalidOutputError(f"模型返回不可解析: {e}") from e


def build_model(settings):
    """按配置装配模型实例；Mock 为默认，密钥缺失时强制回退 Mock。"""
    from app.models.mock import MockLLM

    if settings.model == "openai" and settings.model_api_key and settings.model_base_url:
        return OpenAICompatLLM(settings.model_base_url, settings.model_api_key, settings.model_name)
    return MockLLM()
