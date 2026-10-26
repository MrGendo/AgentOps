"""FastAPI 应用：/internal/v1 内部接口（契约 docs/openapi/internal-v1.yaml）。"""

import threading

from fastapi import Depends, FastAPI, Header, HTTPException
from pydantic import BaseModel, ConfigDict, Field

from app.__version__ import __version__
from app.config import settings
from app.gwclient import GatewayClient
from app.models.openai_compat import build_model
from app.runtime.loop import execute_run
from app.runs import store

app = FastAPI(
    title="AgentOps Internal API",
    version=__version__,
    description="Agent Runtime / 评测 / 诊断内部接口，仅限本机与内网调用",
)


def require_token(x_service_token: str = Header(default="")) -> None:
    """内部接口服务令牌校验；与 Go 平台共享同一令牌。"""
    if settings.service_token and x_service_token != settings.service_token:
        raise HTTPException(status_code=401, detail={
            "code": "UNAUTHORIZED", "message": "服务令牌缺失或不正确", "retryable": False})


@app.get("/internal/v1/health")
async def health() -> dict:
    """健康检查：报告自身状态与版本（§13.5 故障隔离）。"""
    return {"status": "ok", "version": __version__}


class AgentRunRequest(BaseModel):
    # pydantic v2 保留 model_config 作为配置属性，字段用别名承接线上同名键
    model_config = ConfigDict(populate_by_name=True)

    task_id: str = Field(min_length=36, max_length=36)
    trace_id: str | None = None
    goal: dict
    allowed_tools: list[str] = []
    run_limits: dict | None = None
    model_cfg: dict | None = Field(default=None, alias="model_config")


@app.post("/internal/v1/agent-runs", status_code=202, dependencies=[Depends(require_token)])
async def create_agent_run(req: AgentRunRequest) -> dict:
    """发起 Agent 运行：立即受理并返回 run_id，执行在后台线程。"""
    run = store.create(req.task_id, req.trace_id or "", req.goal,
                       req.allowed_tools, req.run_limits, req.model_cfg)

    def _run() -> None:
        gw = GatewayClient(settings.platform_url, settings.service_token,
                           timeout=settings.gateway_timeout_seconds)
        try:
            execute_run(run, build_model(settings), gw)
        finally:
            gw.close()

    threading.Thread(target=_run, daemon=True, name=f"agent-run-{run.id[:8]}").start()
    return {"data": {"id": run.id, "status": run.status}}


@app.get("/internal/v1/agent-runs/{run_id}", dependencies=[Depends(require_token)])
async def get_agent_run(run_id: str) -> dict:
    """查询运行状态与结果摘要；运行不存在返回 404 错误包络。"""
    run = store.get(run_id)
    if run is None:
        raise HTTPException(status_code=404, detail={
            "code": "RUN_NOT_FOUND", "message": "运行不存在（服务可能已重启）",
            "retryable": True})
    return {"data": run.digest()}


@app.post("/internal/v1/agent-runs/{run_id}/cancel", status_code=202,
          dependencies=[Depends(require_token)])
async def cancel_agent_run(run_id: str) -> dict:
    """请求取消：在下一轮检查点生效（§6.2 不强行中断已发出的调用）。"""
    ok = store.cancel(run_id)
    return {"data": {"id": run_id, "cancel_requested": ok}}
