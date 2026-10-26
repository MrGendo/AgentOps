"""运行配置：全部来自环境变量；密钥只在内存中流转，严禁写日志或轨迹。"""

import os
from dataclasses import dataclass, field


def _int(key: str, default: int) -> int:
    try:
        return int(os.getenv(key, str(default)))
    except ValueError:
        return default


@dataclass
class Settings:
    model: str = os.getenv("AGENTOPS_MODEL", "mock")
    model_api_key: str = os.getenv("AGENTOPS_MODEL_API_KEY", "")
    model_base_url: str = os.getenv("AGENTOPS_MODEL_BASE_URL", "")
    model_name: str = os.getenv("AGENTOPS_MODEL_NAME", "mock-ops-agent")
    platform_url: str = os.getenv("AGENTOPS_PLATFORM_URL", "http://localhost:8080")
    service_token: str = os.getenv("AGENTOPS_SERVICE_TOKEN", "dev-service-token")
    agent_version: str = "0.1.0"
    # 执行限制默认值（§7.2）；单次运行可被 Go 下发的 run_limits 覆盖
    max_iterations: int = _int("AGENTOPS_MAX_ITERATIONS", 10)
    max_tool_calls: int = _int("AGENTOPS_MAX_TOOL_CALLS", 20)
    max_duration_seconds: int = _int("AGENTOPS_MAX_DURATION_SECONDS", 300)
    max_plan_steps: int = _int("AGENTOPS_MAX_PLAN_STEPS", 8)
    model_timeout_seconds: int = _int("AGENTOPS_MODEL_TIMEOUT_SECONDS", 60)
    context_max_bytes: int = _int("AGENTOPS_CONTEXT_MAX_BYTES", 32768)
    gateway_timeout_seconds: float = float(_int("AGENTOPS_GATEWAY_TIMEOUT_SECONDS", 10))


@dataclass
class RunLimits:
    """单次运行的限制；未设置的字段回退到全局默认。"""

    max_iterations: int | None = None
    max_tool_calls: int | None = None
    max_duration_seconds: int | None = None
    max_plan_steps: int | None = None
    model_timeout_seconds: int | None = None
    context_max_bytes: int | None = None

    @classmethod
    def from_dict(cls, d: dict | None) -> "RunLimits":
        if not d:
            return cls()
        allowed = {f for f in cls.__dataclass_fields__}
        return cls(**{k: v for k, v in d.items() if k in allowed and isinstance(v, int)})

    def resolved(self, s: Settings) -> "RunLimits":
        # 显式区分 None（未设置→回退默认）与 0（合法的立即生效值，如测试用超时 0）
        def pick(f: str):
            v = getattr(self, f)
            return getattr(s, f) if v is None else v

        return RunLimits(**{f: pick(f) for f in RunLimits.__dataclass_fields__})


settings = Settings()
