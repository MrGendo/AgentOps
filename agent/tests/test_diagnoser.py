"""T8 诊断器：分类全表、置信度、证据引用与零副作用。"""

from app.diagnosis.diagnoser import diagnose


def ev(task=None, steps=None, calls=None, events=None, runs=None):
    return {"task": task or {"id": "tk", "status": "FAILED"},
            "steps": steps or [], "tool_calls": calls or [],
            "events": events or [], "agent_runs": runs or []}


def test_model_error_from_agent_run():
    r = diagnose(ev(runs=[{"id": "ar1", "error": {"code": "RATE_LIMITED", "message": ""}}]))
    assert r["fault_code"] == "MODEL_ERROR" and r["confidence"] == "HIGH"
    assert r["evidence"][0]["ref"] == "ar1"  # 引用真实运行 id


def test_planning_error():
    r = diagnose(ev(runs=[{"id": "ar1", "plan_valid": False}]))
    assert r["fault_code"] == "PLANNING_ERROR"


def test_policy_violation():
    r = diagnose(ev(calls=[{"id": "tc1", "tool_name": "shell", "status": "REJECTED",
                            "error_code": "PERMISSION_DENIED"}]))
    assert r["fault_code"] == "POLICY_VIOLATION"
    assert any("授权" in s for s in r["suggestions"])


def test_tool_argument_error():
    r = diagnose(ev(calls=[{"id": "tc1", "tool_name": "calculator", "status": "REJECTED",
                            "error_code": "ARGUMENT_INVALID"}]))
    assert r["fault_code"] == "TOOL_ARGUMENT_ERROR"


def test_timeout_unknown_needs_human():
    r = diagnose(ev(calls=[{"id": "tc1", "tool_name": "mock_http", "status": "TIMEOUT_UNKNOWN"}]))
    assert r["fault_code"] == "TIMEOUT_OR_UNKNOWN"
    assert r["need_human"] is True
    assert any("确认" in s for s in r["suggestions"])


def test_tool_execution_error():
    r = diagnose(ev(calls=[{"id": "tc1", "tool_name": "mock_http", "status": "FAILED"}]))
    assert r["fault_code"] == "TOOL_EXECUTION_ERROR"


def test_orchestration_error_from_lease_revoked():
    r = diagnose(ev(events=[{"event_type": "lease.revoked"}]))
    assert r["fault_code"] == "ORCHESTRATION_ERROR"


def test_task_level_argument_invalid_medium():
    r = diagnose(ev(task={"id": "tk", "status": "FAILED", "error_code": "ARGUMENT_INVALID"}))
    assert r["fault_code"] == "TOOL_ARGUMENT_ERROR" and r["confidence"] == "MEDIUM"


def test_insufficient_evidence_is_low_and_honest():
    r = diagnose(ev())
    assert r["fault_code"] == "CONTEXT_ERROR"
    assert r["confidence"] == "LOW"
    assert "证据不足" in r["root_cause_hypothesis"]  # 不编造根因
    assert r["need_human"] is True


def test_multiple_signals_downgrade_confidence():
    r = diagnose(ev(calls=[{"id": "tc1", "tool_name": "x", "status": "FAILED"}],
                    events=[{"event_type": "lease.revoked"}]))
    assert r["confidence"] == "MEDIUM"  # 双信号并存降级


def test_zero_side_effects():
    """诊断器必须是纯函数：同输入两次调用结果一致且不触碰外部状态。"""
    e = ev(calls=[{"id": "tc1", "tool_name": "x", "status": "FAILED"}])
    a, b = diagnose(e), diagnose(e)
    assert a == b
    # 证据包未被篡改
    assert e["tool_calls"][0]["status"] == "FAILED"
