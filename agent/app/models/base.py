"""统一模型接口：文本/结构化生成、超时、错误分类、用量与请求标识。"""

from abc import ABC, abstractmethod
from dataclasses import dataclass


class ModelError(Exception):
    """模型错误基类；kind 与 Go retry 类别对齐（RATE_LIMITED 可退避重试）。"""

    kind = "PROVIDER_ERROR"


class RateLimitedError(ModelError):
    kind = "RATE_LIMITED"


class ModelTimeoutError(ModelError):
    kind = "MODEL_TIMEOUT"


class InvalidOutputError(ModelError):
    kind = "INVALID_OUTPUT"


class ProviderError(ModelError):
    kind = "PROVIDER_ERROR"


@dataclass
class ModelUsage:
    prompt_tokens: int = 0
    completion_tokens: int = 0
    total_tokens: int = 0

    def add(self, other: "ModelUsage") -> None:
        self.prompt_tokens += other.prompt_tokens
        self.completion_tokens += other.completion_tokens
        self.total_tokens += other.total_tokens


@dataclass
class ModelResponse:
    content: str
    usage: ModelUsage
    request_id: str
    model: str


class BaseLLM(ABC):
    """所有模型适配器必须实现的最小接口。"""

    name: str = "base"

    @abstractmethod
    def generate(self, prompt: str, *, timeout: float | None = None) -> ModelResponse:
        """生成文本；结构化输出由调用方解析并校验（InvalidOutputError 上抛）。"""
