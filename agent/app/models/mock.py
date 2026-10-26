"""脚本化 Mock 模型：无网络、无密钥、输出确定。

协议（与 runtime/loop 约定）：
  第 1 轮  → {"type":"plan","steps":[...]}
  中间轮  → {"type":"tool_call","tool":...,"params":...}
  终止轮  → {"type":"final","output":...,"summary":...}
按 goal 关键词选择场景脚本；每个实例只服务一次运行（内部含轮次游标）。
"""

import json
import uuid

from app.models.base import BaseLLM, ModelResponse, ModelUsage


def _ops_diagnosis_script(goal: dict) -> list[dict]:
    service = goal.get("service", "order-api")
    baseline = goal.get("baseline_ms", 80)
    current = goal.get("current_ms", 120)
    expr = f"({current}-{baseline})/{baseline}*100"
    return [
        {
            "type": "plan",
            "steps": [
                {"name": "检索知识库", "description": "定位响应变慢的常见原因", "tool": "knowledge_search"},
                {"name": "量化劣化幅度", "description": "计算延迟上升百分比", "tool": "calculator"},
                {"name": "探测下游依赖", "description": "确认下游服务是否可用", "tool": "mock_http"},
                {"name": "汇总诊断", "description": "输出含证据的诊断报告"},
            ],
        },
        {
            "type": "tool_call",
            "tool": "knowledge_search",
            "params": {"query": "服务响应变慢 原因 排查", "top_k": 2},
            "reason": "先查知识库缩小范围",
        },
        {
            "type": "tool_call",
            "tool": "calculator",
            "params": {"expression": expr},
            "reason": "量化延迟劣化幅度",
        },
        {
            "type": "tool_call",
            "tool": "mock_http",
            "params": {"mode": "success", "delay_ms": 5, "body": {"dependency": "billing", "reachable": True}},
            "reason": "确认下游计费服务可达",
        },
        {
            "type": "final",
            "output": {
                "report": {
                    "service": service,
                    "root_cause": "下游依赖响应劣化叠加慢查询",
                    "evidence": ["知识库：慢查询与连接池耗尽特征", "延迟劣化 50%", "下游 billing 可达"],
                    "suggestion": "对查询加语句超时并复核执行计划",
                }
            },
            "summary": f"{service} 响应变慢诊断完成",
        },
    ]


def _generic_script(goal: dict) -> list[dict]:
    query = str(goal.get("topic", "通用排查"))
    return [
        {"type": "plan", "steps": [
            {"name": "检索资料", "tool": "knowledge_search"},
            {"name": "总结", "description": "基于证据作答"},
        ]},
        {"type": "tool_call", "tool": "knowledge_search",
         "params": {"query": query, "top_k": 1}, "reason": "取证"},
        {"type": "final", "output": {"summary": f"围绕「{query}」的结论"}, "summary": "完成"},
    ]


class MockLLM(BaseLLM):
    """确定性脚本模型。"""

    name = "mock"

    def __init__(self) -> None:
        self._cursor = 0
        self._script: list[dict] | None = None

    def _pick_script(self, goal: dict) -> list[dict]:
        text = json.dumps(goal, ensure_ascii=False)
        if any(k in text for k in ("变慢", "诊断", "diagnos", "排查")):
            return _ops_diagnosis_script(goal)
        return _generic_script(goal)

    def generate(self, prompt: str, *, timeout: float | None = None) -> ModelResponse:
        # 脚本在首轮按 prompt 中的 goal 选择；之后按游标顺序吐出
        if self._script is None:
            goal = {}
            try:
                goal = json.loads(prompt).get("goal", {})
            except (json.JSONDecodeError, AttributeError):
                pass
            self._script = self._pick_script(goal if isinstance(goal, dict) else {})
        if self._cursor >= len(self._script):
            # 脚本耗尽仍未终止：抛结构化错误，驱动运行失败（不得无限输出）
            from app.models.base import InvalidOutputError

            raise InvalidOutputError("脚本耗尽：模型未给出终止动作")
        action = self._script[self._cursor]
        self._cursor += 1
        content = json.dumps(action, ensure_ascii=False)
        usage = ModelUsage(prompt_tokens=120 + 60 * self._cursor,
                           completion_tokens=40 + 30 * self._cursor)
        usage.total_tokens = usage.prompt_tokens + usage.completion_tokens
        return ModelResponse(content=content, usage=usage,
                             request_id=f"mock-{uuid.uuid4().hex[:8]}", model="mock")
