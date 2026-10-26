"""分项指标聚合（§8.3）。每个指标自带 caliber 口径说明，不提供综合得分。"""


def aggregate(results: list[dict], pricing: dict | None = None) -> dict:
    """results 为 runner 记录的逐样例结果。"""
    n = len(results) or 1  # 空集分母按 1 防 0 除，指标全 0
    tasks_ok = sum(1 for r in results if r["task_status"] == "SUCCEEDED")
    correct = sum(1 for r in results if r["all_rules_pass"])
    retried = sum(1 for r in results if r.get("attempt_count", 0) > 1)

    total_calls = 0
    good_calls = 0
    total_steps = 0
    ok_steps = 0
    total_tokens = {"prompt_tokens": 0, "completion_tokens": 0, "total_tokens": 0}
    durations = []
    for r in results:
        for c in r.get("tool_calls") or []:
            total_calls += 1
            if c.get("status") == "SUCCEEDED" and c.get("allowed"):
                good_calls += 1
        for s in r.get("steps") or []:
            total_steps += 1
            if s.get("status") == "SUCCEEDED":
                ok_steps += 1
        u = r.get("token_usage") or {}
        for k in total_tokens:
            total_tokens[k] += u.get(k, 0) or 0
        if r.get("duration_ms") is not None:
            durations.append(r["duration_ms"])

    pricing = pricing or {}
    cost = (total_tokens["prompt_tokens"] / 1000.0 * float(pricing.get("input_per_1k", 0))
            + total_tokens["completion_tokens"] / 1000.0 * float(pricing.get("output_per_1k", 0)))

    return {
        "case_count": len(results),
        "task_success_rate": {"value": round(tasks_ok / n, 4), "caliber": "per-task：终态 SUCCEEDED 的样例占比"},
        "result_correctness": {"value": round(correct / n, 4), "caliber": "per-task：全部评分规则通过的样例占比"},
        "tool_call_accuracy": {"value": round(good_calls / total_calls, 4) if total_calls else None,
                               "caliber": "per-call：SUCCEEDED 且工具∈allowed 的调用占比；无调用时为 null"},
        "step_completion_rate": {"value": round(ok_steps / total_steps, 4) if total_steps else None,
                                 "caliber": "per-step：SUCCEEDED 步骤占比；无步骤时为 null"},
        "avg_duration_ms": {"value": round(sum(durations) / len(durations)) if durations else None,
                            "caliber": "per-task：任务开始到结束的均值"},
        "avg_tool_calls": {"value": round(total_calls / n, 4), "caliber": "per-task：总调用数/样例数"},
        "retry_rate": {"value": round(retried / n, 4), "caliber": "per-task：attempt_count>1 的样例占比"},
        "token_usage": {**total_tokens, "caliber": "汇总自 agent_runs（Mock 任务为 0）"},
        "cost_estimate": {"value": round(cost, 6),
                          "caliber": "run_config.pricing × token 用量；未配置计价为 0"},
    }
