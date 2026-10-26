"""FastAPI 应用入口：/internal/v1 内部接口（契约见 docs/openapi/internal-v1.yaml）。

阶段 A 仅提供健康检查；agent-runs / evaluations / diagnoses 在阶段 C、D 按契约扩展。
"""

from fastapi import FastAPI

from app.__version__ import __version__

app = FastAPI(
    title="AgentOps Internal API",
    version=__version__,
    description="Agent Runtime / 评测 / 诊断内部接口，仅限本机与内网调用",
)


@app.get("/internal/v1/health")
async def health() -> dict:
    """健康检查：报告自身状态与版本，供 Go 平台探活（§13.5 故障隔离）。"""
    return {"status": "ok", "version": __version__}
