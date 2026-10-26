"""运行状态仓：进程内存 + 锁；终态保留至进程生命周期（Go 拉取后可重复查询）。"""

import threading
import uuid

from app.config import RunLimits, settings
from app.runtime.loop import AgentRun


class RunStore:
    def __init__(self) -> None:
        self._runs: dict[str, AgentRun] = {}
        self._lock = threading.Lock()

    def create(self, task_id: str, trace_id: str, goal: dict,
               allowed_tools: list[str], run_limits: dict | None,
               model_config: dict | None) -> AgentRun:
        run = AgentRun(
            run_id=uuid.uuid4().hex, task_id=task_id, trace_id=trace_id,
            goal=goal, allowed_tools=allowed_tools,
            limits=RunLimits.from_dict(run_limits).resolved(settings),
            model_config=model_config or {}, agent_version=settings.agent_version,
        )
        with self._lock:
            self._runs[run.id] = run
        return run

    def get(self, run_id: str) -> AgentRun | None:
        with self._lock:
            return self._runs.get(run_id)

    def cancel(self, run_id: str) -> bool:
        with self._lock:
            run = self._runs.get(run_id)
            if run is None or run.status in ("SUCCEEDED", "FAILED", "CANCELLED"):
                return False
            run.cancelled = True
            return True


store = RunStore()
