"""T3 计划校验：结构、步骤上限、重复命名。"""

import pytest

from app.models.base import InvalidOutputError
from app.runtime.planner import parse_action, validate_plan


def test_parse_action_rejects_non_json():
    with pytest.raises(InvalidOutputError):
        parse_action("不是 JSON")


def test_parse_action_rejects_missing_type():
    with pytest.raises(InvalidOutputError):
        parse_action('{"steps": []}')


def test_validate_plan_ok():
    plan = validate_plan(
        {"type": "plan", "steps": [{"name": "a", "tool": "calculator"}]}, max_steps=8)
    assert plan.steps[0].name == "a"


def test_validate_plan_step_limit():
    steps = [{"name": f"s{i}"} for i in range(9)]
    with pytest.raises(InvalidOutputError, match="上限"):
        validate_plan({"type": "plan", "steps": steps}, max_steps=8)


def test_validate_plan_duplicate_names():
    with pytest.raises(InvalidOutputError, match="重复"):
        validate_plan({"type": "plan", "steps": [{"name": "a"}, {"name": "a"}]}, max_steps=8)


def test_validate_plan_requires_plan_type():
    with pytest.raises(InvalidOutputError):
        validate_plan({"type": "final"}, max_steps=8)
