"""mini-SWE-agent 适配器：委托既有 swea 执行器（Mock/真实双模式、隔离工作区、独立验收）。"""

from app.swea import runner as swea

AGENT_ID = "miniswe"


class MiniSweAgentAdapter:
    agent_id = AGENT_ID
    display_name = "mini-SWE-agent"
    description = "极简 SWE Agent（上游 v2.4.6）：单步 bash 循环，轻量、快"

    def version(self) -> str:
        return "2.4.6"

    def availability(self) -> dict:
        # mock 模式恒可用；real 模式依赖模型配置（沿用 swea 安全门语义）
        mode = swea.settings.mode
        if mode == "mock":
            return {"available": True, "configuration_status": "mock",
                    "reason": "Mock 模式：无需 API Key"}
        try:
            swea.validate_real_mode()
            return {"available": True, "configuration_status": "complete",
                    "reason": f"模型 {swea.settings.model_name}"}
        except Exception as e:
            return {"available": True, "configuration_status": "incomplete",
                    "reason": str(e)}

    def validate(self, goal: dict) -> str | None:
        if not goal.get("problem") or not goal.get("fixture"):
            return "缺少 problem 或 fixture"
        return None

    def execute(self, task_id, goal, on_event=None):
        out = swea.execute_swe_task(task_id, goal, on_event)
        out["agent_id"] = AGENT_ID
        return out
