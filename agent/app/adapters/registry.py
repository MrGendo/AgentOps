"""Agent 注册表：已注册 ≠ 可运行 ≠ 已测试，三者严格分离。

运行统计不由注册表编造——由 Go 侧按 agent_runs(agent_version=agent_id) 聚合注入。
"""

from app.adapters.mini_swe import MiniSweAgentAdapter
from app.adapters.subprocess_agent import SubprocessAgentAdapter
from pathlib import Path as _Path

_SCRIPTS = _Path(__file__).resolve().parents[2] / "scripts"

def _check_cmd(cmd: str) -> bool:
    import shutil as _sh
    return _sh.which(cmd) is not None

_ADAPTERS = {a.agent_id: a for a in [
    MiniSweAgentAdapter(),
    SubprocessAgentAdapter(
        agent_id="openhands",
        display_name="OpenHands",
        description="OpenHands（官方 SDK，子进程隔离）",
        script_path=str(_SCRIPTS / "run_openhands.py"),
        version="sdk-1.49.1/subprocess",
    ),
    SubprocessAgentAdapter(
        agent_id="cline",
        display_name="Cline",
        description="Cline CLI（自主编码 Agent，npm 包）",
        script_path=str(_SCRIPTS / "run_cline.sh"),
        version="3.0.74/cli",
    ),
    SubprocessAgentAdapter(
        agent_id="aider",
        display_name="Aider",
        description="Aider CLI（AI 结对编程，pip 包）",
        script_path=str(_SCRIPTS / "run_aider.sh"),
        version="chat/latest-cli",
    ),
]}


def get_adapter(agent_id: str):
    return _ADAPTERS.get(agent_id)


def list_adapters():
    return list(_ADAPTERS.values())


def describe(stats: dict | None = None) -> list[dict]:
    """注册表视图（stats: {agent_id: {total_runs, success_rate, last_run_status, last_used_at}}）。"""
    stats = stats or {}
    out = []
    for a in list_adapters():
        av = a.availability()
        s = stats.get(a.agent_id) or {}
        out.append({
            "agent_id": a.agent_id, "display_name": a.display_name,
            "adapter_type": type(a).__name__, "version": a.version(),
            "description": a.description, "capabilities": ["swe.fix"],
            "available": av["available"],
            "configuration_status": av["configuration_status"],
            "configuration_reason": av["reason"],
            # 已测试 = 有真实运行记录；无记录时 success_rate 为 None（不是 0）
            "total_runs": s.get("total_runs", 0),
            "success_rate": s.get("success_rate"),
            "last_run_status": s.get("last_run_status"),
            "last_used_at": s.get("last_used_at"),
        })
    return out
