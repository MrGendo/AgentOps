"""T3 指标口径：分子分母逐项断言（§8.3 禁止口径混淆）。"""

from app.eval.metrics import aggregate


def mk(task_status="SUCCEEDED", all_pass=True, calls=None, steps=None,
       attempts=1, duration=100, tokens=None):
    return {"task_status": task_status, "all_rules_pass": all_pass,
            "tool_calls": calls or [], "steps": steps or [],
            "attempt_count": attempts, "duration_ms": duration,
            "token_usage": tokens or {}}


def test_calibers_explicit():
    m = aggregate([mk(), mk()])
    for k in ("task_success_rate", "result_correctness", "tool_call_accuracy",
              "step_completion_rate", "avg_tool_calls", "retry_rate"):
        assert "caliber" in m[k], f"{k} 缺口径说明"


def test_success_and_correctness_denominators():
    # 4 样例：2 个任务成功、其中仅 1 个规则全过——成功率与正确率口径不同
    m = aggregate([mk(), mk(task_status="FAILED", all_pass=False),
                   mk(all_pass=False), mk(task_status="FAILED", all_pass=False)])
    assert m["case_count"] == 4
    assert m["task_success_rate"]["value"] == 0.5      # per-task：2/4 任务成功
    assert m["result_correctness"]["value"] == 0.25    # per-task：1/4 规则全过


def test_tool_call_accuracy_per_call():
    calls = [{"status": "SUCCEEDED", "allowed": True},
             {"status": "SUCCEEDED", "allowed": True},
             {"status": "FAILED", "allowed": True},
             {"status": "SUCCEEDED", "allowed": False}]
    m = aggregate([mk(calls=calls), mk()])
    assert m["tool_call_accuracy"]["value"] == 0.5  # 2 合格 / 4 总调用
    assert m["avg_tool_calls"]["value"] == 2.0      # 4 调用 / 2 样例


def test_step_completion_null_when_no_steps():
    m = aggregate([mk()])
    assert m["step_completion_rate"]["value"] is None


def test_retry_rate():
    m = aggregate([mk(attempts=1), mk(attempts=3), mk(attempts=2)])
    assert m["retry_rate"]["value"] == round(2 / 3, 4)


def test_cost_estimate():
    tokens = {"prompt_tokens": 1000, "completion_tokens": 1000, "total_tokens": 2000}
    m = aggregate([mk(tokens=tokens)], pricing={"input_per_1k": 0.5, "output_per_1k": 1.5})
    assert m["cost_estimate"]["value"] == 2.0  # 0.5*1 + 1.5*1
