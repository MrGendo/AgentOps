"""确定性评分规则（§8.4：可验证结果优先，禁用模型自评充当正确率）。"""


def _walk(obj, path: str):
    """按 a.b.c 取嵌套字段；不存在返回哨兵。"""
    cur = obj
    for part in path.split("."):
        if isinstance(cur, dict) and part in cur:
            cur = cur[part]
        else:
            return _MISSING
    return cur


class _Missing:
    pass


_MISSING = _Missing()


def final_output(ctx: dict):
    """最终输出：优先 Agent 终态输出，否则取最后成功步骤的内联结果。"""
    for run in ctx.get("agent_runs") or []:
        if run.get("final_output"):
            return run["final_output"]
    for step in reversed(ctx.get("steps") or []):
        if step.get("status") == "SUCCEEDED" and step.get("output_ref"):
            try:
                import json
                return json.loads(step["output_ref"])
            except (ValueError, TypeError):
                return {"raw": str(step["output_ref"])}
    return None


def _tools_called(ctx: dict) -> list[str]:
    return [c.get("tool_name", "") for c in ctx.get("tool_calls") or []]


def apply_rule(rule: dict, ctx: dict) -> dict:
    """执行单条规则，返回 {rule, pass, evidence}。未知规则类型按失败处理。"""
    rtype = rule.get("type", "")
    task = ctx.get("task") or {}
    out = _out_name(rule)

    if rtype == "finished_normally":
        ok = task.get("status") == "SUCCEEDED"
        return {"rule": out, "pass": ok,
                "evidence": {"task_status": task.get("status"),
                             "finish_reason": task.get("finish_reason")}}

    if rtype == "contains_text":
        # 证据类规则：在 Agent 终态输出与全部成功步骤输出中检索（证据可出现在任一环节）
        import json
        blobs = []
        for run in ctx.get("agent_runs") or []:
            if run.get("final_output"):
                blobs.append(json.dumps(run["final_output"], ensure_ascii=False))
        for step in ctx.get("steps") or []:
            if step.get("status") == "SUCCEEDED" and step.get("output_ref"):
                blobs.append(str(step["output_ref"]))
        text = str(rule.get("text", ""))
        ok = text != "" and any(text in b for b in blobs)
        return {"rule": out, "pass": ok, "evidence": {"needle": text, "found": ok}}

    if rtype == "field_present":
        val = _walk(final_output(ctx) or {}, rule.get("path", ""))
        ok = val is not _MISSING and val is not None
        return {"rule": out, "pass": ok, "evidence": {"path": rule.get("path"), "present": ok}}

    if rtype == "numeric_range":
        val = _walk(final_output(ctx) or {}, rule.get("path", ""))
        ok = val is not _MISSING and isinstance(val, (int, float)) \
            and val >= rule.get("min", float("-inf")) \
            and val <= rule.get("max", float("inf"))
        return {"rule": out, "pass": ok,
                "evidence": {"path": rule.get("path"), "value": val if val is not _MISSING else None,
                             "range": [rule.get("min"), rule.get("max")]}}

    if rtype == "tools_used":
        want = set(rule.get("tools") or [])
        called = set(_tools_called(ctx))
        mode = rule.get("mode", "subset")
        # subset：期望 ⊆ 实际（该用的工具都用了，允许调用其他已授权工具）
        # exact：集合完全一致
        ok = (want <= called) if mode == "subset" else (called == want)
        return {"rule": out, "pass": ok,
                "evidence": {"called": sorted(called), "expected": sorted(want), "mode": mode}}

    return {"rule": out or rtype, "pass": False,
            "evidence": {"error": f"未知规则类型 {rtype}"}}


def _out_name(rule: dict) -> str:
    return rule.get("name") or rule.get("type", "unnamed")


def apply_rules(rules: list[dict], ctx: dict) -> list[dict]:
    """执行全部规则；结果正确率 = 全部通过（口径见 metrics）。"""
    return [apply_rule(r, ctx) for r in rules or []]
