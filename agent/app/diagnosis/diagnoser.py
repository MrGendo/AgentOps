"""规则式故障诊断器（§9）：只读证据、只产出分析，绝不调用工具或修改状态。"""

DIAGNOSER_VERSION = "rule-v1"

# 建议模板：按故障码给出可执行的修复方向
_SUGGESTIONS = {
    "MODEL_ERROR": ["检查模型服务可用性与配额", "核对模型参数与超时配置", "复放失败请求验证输出格式"],
    "PLANNING_ERROR": ["收紧计划 schema 校验提示词", "降低计划步骤上限后重试", "检查目标描述是否含糊"],
    "TOOL_SELECTION_ERROR": ["在提示词中明确可用工具清单", "检查工具描述与任务目标的匹配度"],
    "TOOL_ARGUMENT_ERROR": ["修正工具参数模板", "为该工具补充参数示例"],
    "TOOL_EXECUTION_ERROR": ["检查工具下游日志", "验证工具版本与依赖"],
    "TIMEOUT_OR_UNKNOWN": ["先向下游确认请求是否已执行（幂等键/状态查询）", "确认前禁止盲目重试", "必要时人工介入"],
    "CONTEXT_ERROR": ["补充任务上下文后重试", "检查输入是否被截断"],
    "POLICY_VIOLATION": ["为任务追加工具授权", "检查权限配置与任务声明的 allowed_tools"],
    "ORCHESTRATION_ERROR": ["检查 Worker 心跳与租约 TTL 配置", "查看 lease.revoked 前后事件"],
    "EVALUATION_ERROR": ["检查评测数据集与运行配置", "重跑评测"],
}


def diagnose(evidence: dict) -> dict:
    """证据 → 诊断报告。证据不足时明确标注 LOW 与不确定假设，不编造根因。"""
    task = evidence.get("task") or {}
    steps = evidence.get("steps") or []
    calls = evidence.get("tool_calls") or []
    events = evidence.get("events") or []
    runs = evidence.get("agent_runs") or []
    ev: list[dict] = []

    def hit(kind, ref, detail):
        ev.append({"type": kind, "ref": ref, "detail": detail})

    # 优先级自上而下（多信号并存取最高优先级并降置信度，见 spec §8）
    matched = []
    for run in runs:
        err = run.get("error") or {}
        code = err.get("code") or run.get("error_code") or ""
        if code in ("RATE_LIMITED", "PROVIDER_ERROR", "INVALID_OUTPUT", "MODEL_TIMEOUT"):
            matched.append(("MODEL_ERROR", "HIGH"))
            hit("agent_run", run.get("id"), f"模型错误 {code}")
        if run.get("finish_reason") == "INVALID_PLAN" or run.get("plan_valid") is False:
            matched.append(("PLANNING_ERROR", "HIGH"))
            hit("agent_run", run.get("id"), "计划校验未通过")

    for c in calls:
        if c.get("status") == "REJECTED" and c.get("error_code") == "PERMISSION_DENIED":
            matched.append(("POLICY_VIOLATION", "HIGH"))
            hit("tool_call", c.get("id"), f"工具 {c.get('tool_name')} 未获授权")
        if c.get("status") == "REJECTED" and c.get("error_code") == "ARGUMENT_INVALID":
            matched.append(("TOOL_ARGUMENT_ERROR", "HIGH"))
            hit("tool_call", c.get("id"), f"工具 {c.get('tool_name')} 参数被拒")
        if c.get("status") == "TIMEOUT_UNKNOWN":
            matched.append(("TIMEOUT_OR_UNKNOWN", "HIGH"))
            hit("tool_call", c.get("id"), f"工具 {c.get('tool_name')} 超时且结果未知")
        if c.get("status") == "FAILED":
            matched.append(("TOOL_EXECUTION_ERROR", "HIGH"))
            hit("tool_call", c.get("id"), f"工具 {c.get('tool_name')} 执行失败")

    for s in steps:
        if s.get("status") == "FAILED" and (s.get("error_code") or "") in ("TOOL_SELECTION_ERROR",):
            matched.append(("TOOL_SELECTION_ERROR", "MEDIUM"))
            hit("step", s.get("id"), f"步骤 {s.get('name')} 工具选择不当")

    if any(e.get("event_type") == "lease.revoked" for e in events) \
            or task.get("error_code") == "LEASE_EXPIRED":
        matched.append(("ORCHESTRATION_ERROR", "HIGH"))
        hit("event", "lease.revoked", "租约失效或被回收")
    if task.get("error_code") == "ARGUMENT_INVALID":
        matched.append(("TOOL_ARGUMENT_ERROR", "MEDIUM"))
        hit("task", task.get("id"), "任务输入参数非法")

    if not matched:
        return {
            "fault_code": "CONTEXT_ERROR",
            "confidence": "LOW",
            "root_cause_hypothesis": "证据不足：现有事件、步骤与调用记录无法定位根因，需人工检查任务上下文",
            "evidence": ev if ev else [{"type": "task", "ref": task.get("id"), "detail": "无可定位失败的信号"}],
            "suggestions": _SUGGESTIONS["CONTEXT_ERROR"],
            "need_human": True,
            "diagnoser_version": DIAGNOSER_VERSION,
        }

    # 去重后取首个（最高优先级）；出现多类信号则降为 MEDIUM
    seen: list[str] = []
    for code, _ in matched:
        if code not in seen:
            seen.append(code)
    fault, confidence = matched[0]
    if len(seen) > 1:
        confidence = "MEDIUM" if confidence == "HIGH" else confidence

    hypothesis = f"依据 {len(ev)} 条证据判定为 {fault}"
    return {
        "fault_code": fault,
        "confidence": confidence,
        "root_cause_hypothesis": hypothesis,
        "evidence": ev,
        "suggestions": _SUGGESTIONS.get(fault, ["人工复核"]),
        "need_human": fault == "TIMEOUT_OR_UNKNOWN",
        "diagnoser_version": DIAGNOSER_VERSION,
    }
