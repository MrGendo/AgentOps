"""评测运行仓：进程内存 + 锁。"""

import threading
import uuid


class EvalStore:
    def __init__(self) -> None:
        self._runs: dict[str, "EvaluationState"] = {}
        self._lock = threading.Lock()

    def create(self, eval_run_id: str, agent_version: str, run_config: dict,
               cases: list[dict]) -> "EvaluationState":
        from app.eval.runner import EvaluationState

        state = EvaluationState(eval_run_id, agent_version, run_config, cases)
        with self._lock:
            self._runs[eval_run_id] = state
        return state

    def get(self, eval_run_id: str):
        with self._lock:
            return self._runs.get(eval_run_id)


eval_store = EvalStore()
