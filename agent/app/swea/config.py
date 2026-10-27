"""SWE Agent 适配器配置：真实模型经上游 litellm 适配；Mock 与真实模式显式切换。

安全边界：
- local 执行环境会让模型生成 bash 命令，默认禁止，须 AGENTOPS_SWE_ALLOW_LOCAL=1 显式降级
- 密钥只从环境变量读取，任何事件/日志只记模型名与耗时，不记密钥
"""

import os
from dataclasses import dataclass


def _int(key: str, default: int) -> int:
    try:
        return int(os.getenv(key, str(default)))
    except ValueError:
        return default


@dataclass
class SweaSettings:
    # 字段默认值在 __post_init__ 里读环境（避免类定义期固化，便于测试注入）
    mode: str = "mock"
    model_name: str = ""
    api_key: str = ""
    api_base: str = ""
    exec_env: str = "local"
    docker_image: str = "python:3.12-slim"
    allow_local: bool = False
    step_limit: int = 20
    cost_limit: float = 3.0
    wall_time_limit: int = 600
    cmd_timeout: int = 120

    def __post_init__(self) -> None:
        self.mode = os.getenv("AGENTOPS_SWE_MODE", self.mode)
        self.model_name = os.getenv("AGENTOPS_SWE_MODEL", self.model_name)
        self.api_key = os.getenv("AGENTOPS_SWE_API_KEY", self.api_key)
        self.api_base = os.getenv("AGENTOPS_SWE_API_BASE", self.api_base)
        self.exec_env = os.getenv("AGENTOPS_SWE_EXEC", self.exec_env)
        self.docker_image = os.getenv("AGENTOPS_SWE_DOCKER_IMAGE", self.docker_image)
        self.allow_local = os.getenv("AGENTOPS_SWE_ALLOW_LOCAL", "") == "1" or self.allow_local
        self.step_limit = _int("AGENTOPS_SWE_STEP_LIMIT", self.step_limit)
        self.cost_limit = float(os.getenv("AGENTOPS_SWE_COST_LIMIT", str(self.cost_limit)))
        self.wall_time_limit = _int("AGENTOPS_SWE_WALL_TIME", self.wall_time_limit)
        self.cmd_timeout = _int("AGENTOPS_SWE_CMD_TIMEOUT", self.cmd_timeout)


settings = SweaSettings()


class ConfigError(Exception):
    """配置错误（如实上抛，绝不静默退化成 Mock）"""


def validate_real_mode() -> None:
    """真实模式前置校验：缺密钥/缺模型名/未开隔离降级开关 → 明确报错。"""
    if settings.mode != "real":
        return
    if not settings.model_name:
        raise ConfigError("真实模式缺少 AGENTOPS_SWE_MODEL（litellm 模型名，如 openai/gpt-4o-mini）")
    if not settings.api_key:
        raise ConfigError("真实模式缺少 AGENTOPS_SWE_API_KEY（密钥只经环境变量注入）")
    if settings.exec_env == "local" and not settings.allow_local:
        raise ConfigError(
            "local 执行环境会运行模型生成的 Shell 命令，默认禁止："
            "请设 AGENTOPS_SWE_EXEC=docker（推荐）或显式设 AGENTOPS_SWE_ALLOW_LOCAL=1 降级"
        )
