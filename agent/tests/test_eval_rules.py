"""T3 规则引擎：各规则通过/失败与证据输出。"""

from app.eval.rules import apply_rules, final_output


def ctx(task=None, steps=None, calls=None, runs=None):
    return {"task": task or {"status": "SUCCEEDED"}, "steps": steps or [],
            "tool_calls": calls or [], "agent_runs": runs or []}


def test_finished_normally():
    r = apply_rules([{"type": "finished_normally"}], ctx({"status": "SUCCEEDED"}))
    assert r[0]["pass"] is True
    r = apply_rules([{"type": "finished_normally"}], ctx({"status": "FAILED"}))
    assert r[0]["pass"] is False and r[0]["evidence"]["task_status"] == "FAILED"


def test_contains_text_and_final_output_from_steps():
    steps = [{"status": "SUCCEEDED", "output_ref": '{"value": 14}'}]
    r = apply_rules([{"type": "contains_text", "text": "value"}], ctx(steps=steps))
    assert r[0]["pass"] is True
    r = apply_rules([{"type": "contains_text", "text": "不存在"}], ctx(steps=steps))
    assert r[0]["pass"] is False


def test_final_output_prefers_agent_run():
    runs = [{"final_output": {"report": {"root_cause": "x"}}}]
    assert final_output(ctx(runs=runs))["report"]["root_cause"] == "x"


def test_field_present_and_numeric_range():
    steps = [{"status": "SUCCEEDED", "output_ref": '{"report": {"ratio": 50.0}}'}]
    rules = [{"type": "field_present", "path": "report.ratio"},
             {"type": "numeric_range", "path": "report.ratio", "min": 0, "max": 100},
             {"type": "numeric_range", "path": "report.ratio", "min": 51, "max": 100},
             {"type": "field_present", "path": "report.missing"}]
    out = apply_rules(rules, ctx(steps=steps))
    assert [o["pass"] for o in out] == [True, True, False, False]


def test_tools_used_subset_and_exact():
    calls = [{"tool_name": "calculator"}, {"tool_name": "knowledge_search"}, {"tool_name": "mock_http"}]
    # subset：期望 ⊆ 实际（该用的都用了，多调已授权工具允许）
    r = apply_rules([{"type": "tools_used", "tools": ["calculator", "knowledge_search"],
                      "mode": "subset"}], ctx(calls=calls))
    assert r[0]["pass"] is True
    # 期望里有没被调用的工具 → 不通过
    r = apply_rules([{"type": "tools_used", "tools": ["calculator", "task_status"],
                      "mode": "subset"}], ctx(calls=calls))
    assert r[0]["pass"] is False
    # exact：集合必须完全一致
    r = apply_rules([{"type": "tools_used", "tools": ["calculator", "knowledge_search"],
                      "mode": "exact"}], ctx(calls=calls))
    assert r[0]["pass"] is False


def test_unknown_rule_fails():
    r = apply_rules([{"type": "magic"}], ctx())
    assert r[0]["pass"] is False and "未知规则类型" in r[0]["evidence"]["error"]
