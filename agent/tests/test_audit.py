"""多层次验收审计测试：四层各自通过/失败路径 + 报告生成。"""

import pytest
from pathlib import Path

from app.audit.multi_level import (
    run_audit, _audit_scope, _audit_security, _audit_quality, _DANGEROUS_PATTERNS,
)
from app.audit.report import AuditReport
from app.swea import workspace as wsmod
from app.swea.runner import execute_swe_task


def test_audit_scope_ok():
    r = _audit_scope(["src/calc.py", "tests/test_calc.py"], None)
    assert r.passed


def test_audit_scope_violation():
    r = _audit_scope(["src/calc.py", ".env", "config/secret.yaml"], None)
    assert not r.passed and ".env" in r.detail


def test_audit_security_clean(tmp_path):
    (tmp_path / "src").mkdir()
    (tmp_path / "src/calc.py").write_text("def clamp(v, lo, hi): return v\n")
    r = _audit_security(tmp_path, ["src/calc.py"])
    assert r.passed


def test_audit_security_dangerous(tmp_path):
    (tmp_path / "src").mkdir()
    (tmp_path / "src/evil.py").write_text(
        "import os\nos.system('rm -rf /')\npassword = 'secret123'\n")
    r = _audit_security(tmp_path, ["src/evil.py"])
    assert not r.passed
    assert "系统命令" in r.detail or "硬编码密钥" in r.detail


def test_audit_report_markdown():
    report = AuditReport(
        task_id="t-123", agent_id="miniswe", model="mock",
        started_at=0, finished_at=1.5,
        audit_levels=[
            {"level": "L1", "name": "冒烟测试", "passed": True, "detail": "3 passed"},
            {"level": "L2", "name": "变更范围", "passed": True, "detail": "1 个文件"},
            {"level": "L3", "name": "安全审计", "passed": False, "detail": "发现 eval"},
            {"level": "L4", "name": "代码质量", "passed": True, "detail": "合理"},
        ],
        changed_files=["src/calc.py"], model_calls=5,
    )
    md = report.to_markdown()
    assert "❌" in md and "eval" in md and "src/calc.py" in md
    assert not report.all_passed
    assert "✗" in report.summary_line()


def test_mock_e2e_includes_audit(tmp_path, monkeypatch):
    """Mock 模式全链路：final_output 应包含 audit_levels 四层。"""
    monkeypatch.setattr(wsmod, "WORKSPACES_ROOT", tmp_path)
    out = execute_swe_task("audit-e2e", {
        "problem": "修边界", "fixture": "boundary_bug",
        "test_cmd": "python -m pytest -q"})
    fo = out["final_output"]
    assert "audit_levels" in fo
    assert len(fo["audit_levels"]) == 4
    assert all(l["passed"] for l in fo["audit_levels"])  # Mock 修复应该四层全过
    assert "audit_summary" in fo
    assert "[PASS]" in fo["audit_summary"]
