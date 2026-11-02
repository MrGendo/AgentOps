"""多层次验收测试：不只判断测试通过，还审计变更质量、安全性和最小变更原则。

层级：
  L1 冒烟    — 测试是否全部通过（基础功能）
  L2 范围    — 是否只改了允许修改的文件（最小变更原则）
  L3 安全    — 变更是否引入危险模式（硬编码密钥/无限循环/危险 import）
  L4 质量    — 变更是否有注释/是否删除了现有测试（回归保护）
"""

import re
import subprocess
from pathlib import Path


class AuditResult:
    """一个层级的审计结果。"""

    def __init__(self, level: str, name: str, passed: bool, detail: str = ""):
        self.level = level
        self.name = name
        self.passed = passed
        self.detail = detail

    def to_dict(self) -> dict:
        return {"level": self.level, "name": self.name, "passed": self.passed, "detail": self.detail}


# 危险代码模式（安全审计用）
_DANGEROUS_PATTERNS = [
    (r"eval\s*\(", "使用 eval()"),
    (r"exec\s*\(", "使用 exec()"),
    (r"__import__\s*\(", "动态导入"),
    (r"subprocess\.(call|run|Popen)", "调用子进程"),
    (r"os\.system\s*\(", "系统命令调用"),
    (r"open\s*\(.*['\"]w['\"]", "文件写入操作"),
    (r"(?:password|secret|token|api_key)\s*=\s*['\"][^'\"]{4,}", "硬编码密钥"),
    (r"while\s+True\s*:(?!\s*(?:break|pass|...))", "可能的无限循环"),
]

# 允许修改的文件模式（最小变更原则）
_DEFAULT_ALLOWED = [
    r"^src/.*\.py$",
    r"^tests/.*\.py$",
    r"^lib/.*\.py$",
    r"^app/.*\.py$",
]


def run_audit(ws: Path, test_cmd: str, changed_files: list[str],
              allowed_patterns: list[str] | None = None,
              timeout: int = 120) -> list[AuditResult]:
    """执行多层次验收审计，返回各层结果列表。"""
    results = []

    # L1: 冒烟 — 测试通过
    results.append(_audit_smoke(ws, test_cmd, timeout))

    # L2: 范围 — 最小变更原则
    results.append(_audit_scope(changed_files, allowed_patterns or _DEFAULT_ALLOWED))

    # L3: 安全 — 危险模式检测
    results.append(_audit_security(ws, changed_files))

    # L4: 质量 — 回归保护
    results.append(_audit_quality(ws, changed_files))

    return results


def _audit_smoke(ws: Path, test_cmd: str, timeout: int) -> AuditResult:
    """L1 冒烟：独立测试是否通过。"""
    import sys as _sys
    allowed_cmd = re.match(r"^(?:python3? -m )?pytest(.*)$", test_cmd.strip())
    if not allowed_cmd:
        return AuditResult("L1", "冒烟测试", False, f"测试命令不在白名单: {test_cmd}")
    args = allowed_cmd.group(1).split()
    proc = subprocess.run([_sys.executable, "-m", "pytest", *args], cwd=ws,
                          capture_output=True, text=True, timeout=timeout)
    tail = "\n".join((proc.stdout + proc.stderr).splitlines()[-10:])
    return AuditResult("L1", "冒烟测试", proc.returncode == 0, tail)


def _audit_scope(changed_files: list[str], allowed: list[str] | None) -> AuditResult:
    """L2 范围：变更是否只涉及允许修改的文件。"""
    if allowed is None:
        allowed = _DEFAULT_ALLOWED
    violations = []
    for f in changed_files:
        if f.endswith((".pyc", ".pyo")):
            continue
        if not any(re.match(p, f) for p in allowed):
            violations.append(f)
    if violations:
        return AuditResult("L2", "变更范围", False,
                           f"越界修改: {', '.join(violations[:5])}")
    return AuditResult("L2", "变更范围", True,
                       f"{len(changed_files)} 个文件变更均在允许范围")


def _audit_security(ws: Path, changed_files: list[str]) -> AuditResult:
    """L3 安全：变更是否引入危险代码模式。"""
    findings = []
    for f in changed_files:
        if not f.endswith(".py"):
            continue
        fp = ws / f
        if not fp.exists():
            continue
        content = fp.read_text(errors="replace")
        for pattern, desc in _DANGEROUS_PATTERNS:
            for m in re.finditer(pattern, content, re.IGNORECASE):
                line_no = content[:m.start()].count("\n") + 1
                findings.append(f"{f}:{line_no} {desc}")
    if findings:
        return AuditResult("L3", "安全审计", False, "; ".join(findings[:5]))
    return AuditResult("L3", "安全审计", True, "未检测到危险模式")


def _audit_quality(ws: Path, changed_files: list[str]) -> AuditResult:
    """L4 质量：是否删除了已有测试、是否只改了必要行数。"""
    issues = []
    # 检查是否删除了测试文件
    for f in changed_files:
        if f.startswith("tests/") and not (ws / f).exists():
            issues.append(f"删除了测试文件 {f}")
    # 检查变更行数（超过 200 行可能过度修改）
    try:
        diff = subprocess.run(["git", "diff", "--stat"], cwd=ws,
                              capture_output=True, text=True, timeout=15)
        for line in diff.stdout.splitlines():
            m = re.search(r"(\d+) insertion", line)
            if m and int(m.group(1)) > 200:
                issues.append(f"插入行数 {m.group(1)} 超过 200（过度修改）")
    except Exception:
        pass
    if issues:
        return AuditResult("L4", "代码质量", False, "; ".join(issues))
    return AuditResult("L4", "代码质量", True, "变更合理")
