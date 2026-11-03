"""编程任务工作区：每个任务独立目录，从内置 fixture 复制基准版本（git 仓库）。

清理策略：成功且验收通过 → 保留补丁后可清理；失败/验收未过 → 保留工作区作为证据。
"""

import shutil
import subprocess
from pathlib import Path

FIXTURES_DIR = Path(__file__).resolve().parents[2] / "evaldata" / "swe_tasks"
WORKSPACES_ROOT = Path(__file__).resolve().parents[3] / "data" / "swe_workspaces"


def prepare_workspace(task_id: str, fixture: str) -> Path:
    """复制 fixture 基准仓库到独立工作区（data/swe_workspaces/<task_id>）。"""
    src = FIXTURES_DIR / fixture / "repo"
    if not src.is_dir():
        raise FileNotFoundError(f"编程任务 fixture 不存在: {fixture}")
    # 每次执行独立工作区（含尝试号隔离），杜绝重试竞态
    import uuid
    ws = WORKSPACES_ROOT / f"{task_id[:8]}-{uuid.uuid4().hex[:6]}"
    shutil.copytree(src, ws)
    # fixture 以普通文件入库（嵌套 git 仓库在克隆后是空目录），
    # 克隆环境无 .git 可复制 → 此处自动建基线提交，保证 diff/审计可用
    if not (ws / ".git").exists():
        _init_baseline(ws)
    return ws


def _init_baseline(ws: Path) -> None:
    """为无 git 历史的工作区建立基准提交（changed_files/extract_patch 依赖它）。"""
    # 显式指定提交者，避免裸环境缺全局 git config 时 commit 失败
    base = ["git", "-c", "user.name=agentops", "-c", "user.email=agentops@local"]
    for args in (["init", "-q"], ["add", "-A"], ["commit", "-q", "-m", "fixture baseline"]):
        proc = subprocess.run(base + args, cwd=ws, capture_output=True, text=True, timeout=30)
        if proc.returncode != 0:
            raise RuntimeError(f"工作区基线初始化失败: {proc.stderr.strip()}")


def changed_files(ws: Path) -> list[str]:
    """git status 提取变更文件列表（fixture 为 git 仓库）。"""
    out = subprocess.run(["git", "status", "--porcelain"], cwd=ws,
                         capture_output=True, text=True, timeout=15)
    return [line[3:].strip() for line in out.stdout.splitlines() if line.strip()]


def extract_patch(ws: Path) -> str:
    """以 git diff 生成补丁（相对基准提交）。"""
    out = subprocess.run(["git", "diff"], cwd=ws, capture_output=True, text=True, timeout=15)
    return out.stdout


def cleanup_workspace(ws: Path, keep: bool) -> None:
    """按清理策略处理工作区：失败任务保留证据，成功任务删除。"""
    if not keep and ws.exists():
        shutil.rmtree(ws, ignore_errors=True)
