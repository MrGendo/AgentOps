"""结构化计划生成与校验：字段完备、步骤数上限、命名合法。"""

import json

from pydantic import BaseModel, Field, ValidationError

from app.models.base import InvalidOutputError


class PlanStep(BaseModel):
    name: str = Field(min_length=1, max_length=64)
    description: str = ""
    tool: str | None = None


class Plan(BaseModel):
    steps: list[PlanStep] = Field(min_length=1)


def parse_action(raw: str) -> dict:
    """把模型输出解析为动作对象；解析失败按 INVALID_OUTPUT 上抛。"""
    try:
        action = json.loads(raw)
    except json.JSONDecodeError as e:
        raise InvalidOutputError(f"模型输出不是合法 JSON: {e}") from e
    if not isinstance(action, dict) or "type" not in action:
        raise InvalidOutputError("动作缺少 type 字段")
    return action


def validate_plan(action: dict, max_steps: int) -> Plan:
    """校验计划动作并返回 Plan；非法计划按 INVALID_OUTPUT 上抛（含原因）。"""
    if action.get("type") != "plan":
        raise InvalidOutputError("首轮动作必须是 plan")
    try:
        plan = Plan.model_validate(action)
    except ValidationError as e:
        raise InvalidOutputError(f"计划结构非法: {e.error_count()} 处错误") from e
    if len(plan.steps) > max_steps:
        raise InvalidOutputError(f"计划步骤数 {len(plan.steps)} 超过上限 {max_steps}")
    names = [s.name for s in plan.steps]
    if len(names) != len(set(names)):
        raise InvalidOutputError("计划步骤名重复")
    return plan
