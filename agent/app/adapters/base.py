"""统一 Agent Adapter 接口：平台只认这个协议，不认识任何具体 Agent 的内部。

Adapter 只做协议适配：校验输入→在隔离工作区执行→产出统一结果结构。
核心任务状态永远归 Go 平台；事件按统一类型映射，保留 source 与原始摘要。
"""

from typing import Any, Callable, Protocol, runtime_checkable


@runtime_checkable
class AgentAdapter(Protocol):
    agent_id: str
    display_name: str
    description: str

    def version(self) -> str: ...
    def availability(self) -> dict: ...
    """返回 {available, configuration_status, reason}——可用性与配置态分离。"""

    def validate(self, goal: dict) -> str | None: ...
    """输入不合法时返回原因字符串；合法返回 None。"""

    def execute(self, task_id: str, goal: dict,
                on_event: Callable[[dict], None] | None = None) -> dict: ...
    """执行并返回统一结果：
    {status, finish_reason, final_output, trajectory(统一事件), token_usage, agent_id}
    """


def finish(status: str, reason: str, **extra) -> dict:
    """统一结果构造器。"""
    return {"status": status, "finish_reason": reason, **extra}
