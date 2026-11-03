"""SWE 适配器测试：配置校验/工作区隔离/验收白名单/Mock 端到端/配置错误不降级。"""

import pytest

from app.swea import acceptance as acc
from app.swea import workspace as wsmod
from app.swea.config import ConfigError, validate_real_mode
from app.swea.runner import execute_swe_task


def test_acceptance_whitelist():
    with pytest.raises(acc.UnsafeTestCommand):
        acc.run_acceptance(None, "rm -rf /")
    with pytest.raises(acc.UnsafeTestCommand):
        acc.run_acceptance(None, "bash -c 'pytest'")


def test_workspace_isolation(tmp_path, monkeypatch):
    """两个任务的工作区必须彼此独立。"""
    monkeypatch.setattr(wsmod, "WORKSPACES_ROOT", tmp_path)
    w1 = wsmod.prepare_workspace("t1", "boundary_bug")
    w2 = wsmod.prepare_workspace("t2", "boundary_bug")
    assert w1 != w2 and w1.is_dir() and w2.is_dir()
    (w1 / "src/calc.py").write_text("x=1")
    assert "x=1" not in (w2 / "src/calc.py").read_text()


def test_workspace_uuid_unique(tmp_path, monkeypatch):
    """UUID 后缀保证每次执行独立工作区（重试不冲突）。"""
    monkeypatch.setattr(wsmod, "WORKSPACES_ROOT", tmp_path)
    w1 = wsmod.prepare_workspace("dup", "boundary_bug")
    w2 = wsmod.prepare_workspace("dup", "boundary_bug")
    assert w1 != w2, "UUID 后缀应使两次创建得到不同路径"


def test_patch_extraction(tmp_path, monkeypatch):
    monkeypatch.setattr(wsmod, "WORKSPACES_ROOT", tmp_path)
    w = wsmod.prepare_workspace("p1", "boundary_bug")
    (w / "src/calc.py").write_text("def clamp(v, lo, hi):\n    return v\n")
    patch = wsmod.extract_patch(w)
    assert "diff --git" in patch and "calc.py" in patch
    assert wsmod.changed_files(w) == ["src/calc.py"]


def test_workspace_auto_init_baseline(tmp_path, monkeypatch):
    """fixture 平铺入库（无 .git，等价于 GitHub 克隆产物）时自动建基线，diff/审计照常可用。"""
    fx = tmp_path / "fixtures" / "flat_fixture" / "repo"
    (fx / "src").mkdir(parents=True)
    (fx / "src" / "calc.py").write_text("def clamp(v, lo, hi):\n    return v\n")
    monkeypatch.setattr(wsmod, "FIXTURES_DIR", tmp_path / "fixtures")
    monkeypatch.setattr(wsmod, "WORKSPACES_ROOT", tmp_path / "ws")
    w = wsmod.prepare_workspace("flat1", "flat_fixture")
    assert (w / ".git").is_dir(), "无 .git 的 fixture 应自动初始化基线仓库"
    (w / "src" / "calc.py").write_text("def clamp(v, lo, hi):\n    return hi\n")
    assert wsmod.changed_files(w) == ["src/calc.py"]
    assert "diff --git" in wsmod.extract_patch(w)


def test_real_mode_requires_key(monkeypatch):
    """真实模式缺密钥必须明确报错，绝不静默降级 Mock。"""
    monkeypatch.setenv("AGENTOPS_SWE_MODE", "real")
    monkeypatch.delenv("AGENTOPS_SWE_MODEL", raising=False)
    monkeypatch.delenv("AGENTOPS_SWE_API_KEY", raising=False)
    from app.swea import config as cfg
    cfg.settings = cfg.SweaSettings()
    with pytest.raises(ConfigError, match="MODEL"):
        validate_real_mode()


def test_real_mode_local_requires_optin(monkeypatch):
    monkeypatch.setenv("AGENTOPS_SWE_MODE", "real")
    monkeypatch.setenv("AGENTOPS_SWE_MODEL", "openai/gpt-4o-mini")
    monkeypatch.setenv("AGENTOPS_SWE_API_KEY", "sk-test")
    monkeypatch.setenv("AGENTOPS_SWE_EXEC", "local")
    monkeypatch.delenv("AGENTOPS_SWE_ALLOW_LOCAL", raising=False)
    from app.swea import config as cfg
    cfg.settings = cfg.SweaSettings()
    with pytest.raises(ConfigError, match="ALLOW_LOCAL"):
        validate_real_mode()


def test_mock_e2e_fix_passes_acceptance(tmp_path, monkeypatch):
    """Mock 模式端到端：四层审计全过 → SUCCEEDED。"""
    monkeypatch.setattr(wsmod, "WORKSPACES_ROOT", tmp_path)
    out = execute_swe_task("e2e-1", {
        "problem": "修边界", "fixture": "boundary_bug",
        "test_cmd": "python -m pytest -q"})
    assert out["status"] == "SUCCEEDED", f"finish={out.get('finish_reason')} levels={out['final_output'].get('audit_levels')}"
    fo = out["final_output"]
    assert fo["audit_levels"] and all(l["passed"] for l in fo["audit_levels"]), \
        f"audit: {[(l['level'], l['passed']) for l in fo.get('audit_levels', [])]}"
    assert "src/calc.py" in fo["changed_files"]
    assert "[PASS]" in fo["audit_summary"]


def test_mock_no_fix_fails_acceptance(tmp_path, monkeypatch):
    """Agent 提交但验收失败（边界场景 10）：明确 FAILED 而非伪报成功。"""
    import shutil
    from pathlib import Path
    # 构造无预置修复的 fixture（复制 boundary_bug 后删除 fix.json）
    fx = tmp_path / "fixtures" / "nofix" / "repo"
    shutil.copytree("/Users/Admin/Downloads/project/AgentOps/agent/evaldata/swe_tasks/boundary_bug/repo", fx)
    (fx / "fix.json").unlink()
    monkeypatch.setattr(wsmod, "FIXTURES_DIR", tmp_path / "fixtures")
    monkeypatch.setattr(wsmod, "WORKSPACES_ROOT", tmp_path / "ws")
    out = execute_swe_task("e2e-nofix", {
        "problem": "无修复", "fixture": "nofix", "test_cmd": "pytest -q"})
    assert out["status"] == "FAILED"
    assert out["finish_reason"] == "ACCEPTANCE_FAILED"


def test_invalid_fixture_rejected(tmp_path, monkeypatch):
    monkeypatch.setattr(wsmod, "WORKSPACES_ROOT", tmp_path)
    out = execute_swe_task("e2e-4", {"problem": "x", "fixture": "nope"})
    assert out["finish_reason"] == "INVALID_INPUT"
