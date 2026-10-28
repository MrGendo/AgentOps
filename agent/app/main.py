"""FastAPI 应用：/internal/v1 内部接口（契约 docs/openapi/internal-v1.yaml）。"""

import threading

from fastapi import Depends, FastAPI, Header, HTTPException
from pydantic import BaseModel, ConfigDict, Field

from app.__version__ import __version__
from app.config import settings
from app.diagnosis.diagnoser import diagnose as run_diagnosis
from app.adapters import registry as agent_registry
from app.eval.runner import execute_evaluation
from app.eval.store import eval_store

_diag_store: dict[str, dict] = {}
from app.gwclient import GatewayClient
from app.models.openai_compat import build_model
from app.platform_api import PlatformClient
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
            if req.goal.get("task_type") == "swe.fix" or req.goal.get("fixture"):
                _execute_swe(run, req.goal)  # 内部经注册表选 Adapter
            else:
                execute_run(run, build_model(settings), gw)
        finally:
            gw.close()

    def _execute_swe(run, goal: dict) -> None:
        """SWE 任务：按 goal.agent 经注册表选 Adapter 执行（默认 miniswe）。"""
        adapter = agent_registry.get_adapter(goal.get("agent") or "miniswe") or \
            agent_registry.get_adapter("miniswe")
        out = adapter.execute(run.task_id, goal)
        run.status = out["status"]
        run.finish_reason = out["finish_reason"]
        run.final_output = out.get("final_output")
        u = out.get("token_usage") or {}
        run.token_usage.prompt_tokens = 0
        run.token_usage.completion_tokens = 0
        run.token_usage.total_tokens = int(u.get("model_calls", 0))
        run.trajectory.model_calls = int(u.get("model_calls", 0))
        for ev in out.get("trajectory") or []:
            run.trajectory.add(ev.get("kind", "note"), ev.get("kind", "note"),
                               "INFO", {"source": ev.get("source"), **(ev.get("data") or {})})

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


class EvaluationRequest(BaseModel):
    eval_run_id: str = Field(min_length=1)
    agent_version: str = "default"
    run_config: dict | None = None
    cases: list[dict] = []


@app.post("/internal/v1/evaluations", status_code=202, dependencies=[Depends(require_token)])
async def create_evaluation(req: EvaluationRequest) -> dict:
    """受理批量评测：逐样例创建真实任务并评分，状态经 GET 轮询。"""
    state = eval_store.create(req.eval_run_id, req.agent_version, req.run_config, req.cases)

    def _run() -> None:
        platform = PlatformClient(settings.platform_url,
                                  timeout=settings.gateway_timeout_seconds)
        try:
            execute_evaluation(state, platform)
        except Exception as e:  # 评测引擎自身异常（非样例失败）
            state.status = "FAILED"
            state.error = {"code": "EVALUATION_ERROR", "message": str(e)}
        finally:
            platform.close()

    threading.Thread(target=_run, daemon=True,
                     name=f"eval-{req.eval_run_id[:8]}").start()
    return {"data": {"id": req.eval_run_id, "status": state.status}}


@app.get("/internal/v1/evaluations/{eval_run_id}", dependencies=[Depends(require_token)])
async def get_evaluation(eval_run_id: str) -> dict:
    """查询评测进度与分项指标（口径见 metrics.*.caliber）。"""
    state = eval_store.get(eval_run_id)
    if state is None:
        raise HTTPException(status_code=404, detail={
            "code": "EVALUATION_NOT_FOUND", "message": "评测运行不存在（服务可能已重启）",
            "retryable": True})
    return {"data": state.digest()}


class DiagnosisRequest(BaseModel):
    task_id: str
    evidence: dict


@app.post("/internal/v1/diagnoses", status_code=202, dependencies=[Depends(require_token)])
async def create_diagnosis(req: DiagnosisRequest) -> dict:
    """受理诊断：纯函数分析证据包，立即完成（保留异步模式以统一契约）。"""
    report = run_diagnosis(req.evidence)
    report["task_id"] = req.task_id
    _diag_store[req.task_id] = report
    return {"data": {"id": req.task_id, "status": "SUCCEEDED"}}


@app.get("/internal/v1/diagnoses/{diag_id}", dependencies=[Depends(require_token)])
async def get_diagnosis(diag_id: str) -> dict:
    """取回诊断报告。"""
    report = _diag_store.get(diag_id)
    if report is None:
        raise HTTPException(status_code=404, detail={
            "code": "DIAGNOSIS_NOT_FOUND", "message": "诊断不存在（服务可能已重启）",
            "retryable": True})
    return {"data": report}


@app.get("/internal/v1/agents", dependencies=[Depends(require_token)])
async def list_agents() -> dict:
    """注册表视图（可用性/配置态/运行统计严格分离；统计由 Go 注入）。"""
    from app.adapters.registry import describe
    return {"data": describe()}
