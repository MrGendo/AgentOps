"""运行轨迹：逐步记录模型调用、工具调用与检查点，供评测与诊断消费。"""

import time
from dataclasses import dataclass, field


def _now() -> float:
    return time.time()


@dataclass
class TrajectoryItem:
    kind: str            # plan / model_call / tool_call / checkpoint / note
    name: str
    status: str          # SUCCEEDED / FAILED / SKIPPED / INFO
    started_at: float = field(default_factory=_now)
    finished_at: float | None = None
    detail: dict = field(default_factory=dict)

    def done(self, status: str, detail: dict | None = None) -> None:
        self.status = status
        self.finished_at = _now()
        if detail:
            self.detail.update(detail)

    def to_dict(self) -> dict:
        return {
            "kind": self.kind, "name": self.name, "status": self.status,
            "started_at": self.started_at, "finished_at": self.finished_at,
            "detail": self.detail,
        }


class Trajectory:
    """一次 Agent 运行的完整轨迹。"""

    def __init__(self) -> None:
        self.items: list[TrajectoryItem] = []
        self.model_calls = 0
        self.tool_calls = 0
        self.tool_results: list[dict] = []   # {tool, status, call_id, error_code}

    def add(self, kind: str, name: str, status: str = "INFO", detail: dict | None = None) -> TrajectoryItem:
        item = TrajectoryItem(kind=kind, name=name, status=status, detail=detail or {})
        self.items.append(item)
        return item

    def record_tool_result(self, tool: str, status: str, call_id: str | None, error_code: str = "") -> None:
        self.tool_calls += 1
        self.tool_results.append(
            {"tool": tool, "status": status, "call_id": call_id, "error_code": error_code})

    def digest(self) -> dict:
        """GET 端点返回的摘要（完整轨迹过长时可扩展分页）。"""
        return {
            "model_calls": self.model_calls,
            "tool_calls": self.tool_calls,
            "iterations": len([i for i in self.items if i.kind == "model_call"]),
            "items": [i.to_dict() for i in self.items],
            "tool_results": self.tool_results,
        }
