"""结构化审计报告：汇聚多层次验收结果 + 变更摘要 + 成本统计，生成可导出报告。"""

import time
from dataclasses import dataclass, field


@dataclass
class AuditReport:
    """一次 Agent 执行的完整审计报告。"""
    task_id: str
    agent_id: str
    model: str
    started_at: float
    finished_at: float
    audit_levels: list[dict] = field(default_factory=list)
    changed_files: list[str] = field(default_factory=list)
    patch_summary: str = ""
    model_calls: int = 0
    token_usage: dict = field(default_factory=dict)
    acceptance_output: str = ""
    agent_submission: str = ""

    @property
    def all_passed(self) -> bool:
        return all(l["passed"] for l in self.audit_levels)

    @property
    def duration_ms(self) -> int:
        return int((self.finished_at - self.started_at) * 1000)

    def to_dict(self) -> dict:
        return {
            "task_id": self.task_id,
            "agent_id": self.agent_id,
            "model": self.model,
            "duration_ms": self.duration_ms,
            "all_passed": self.all_passed,
            "audit_levels": self.audit_levels,
            "changed_files": self.changed_files,
            "patch_summary": self.patch_summary[:500],
            "model_calls": self.model_calls,
            "token_usage": self.token_usage,
            "agent_submission": self.agent_submission[:300],
        }

    def summary_line(self) -> str:
        """单行摘要（控制台/日志用）。"""
        icons = {"L1": "🧪", "L2": "🎯", "L3": "🔒", "L4": "📋"}
        parts = []
        for l in self.audit_levels:
            mark = "✓" if l["passed"] else "✗"
            parts.append(f"{icons.get(l['level'], '')}{mark}")
        status = "PASS" if self.all_passed else "FAIL"
        return f"[{status}] {' '.join(parts)} | {len(self.changed_files)} 文件 | {self.duration_ms}ms"

    def to_markdown(self) -> str:
        """可导出的 Markdown 报告。"""
        lines = [
            f"# Agent 执行审计报告",
            f"",
            f"| 字段 | 值 |",
            f"|---|---|",
            f"| 任务 ID | `{self.task_id}` |",
            f"| Agent | {self.agent_id} |",
            f"| 模型 | {self.model} |",
            f"| 总耗时 | {self.duration_ms}ms |",
            f"| **结论** | **{'✅ 全部通过' if self.all_passed else '❌ 存在问题'}** |",
            f"",
            f"## 验收层级",
            f"",
            f"| 层级 | 名称 | 结果 | 详情 |",
            f"|---|---|---|---|",
        ]
        for l in self.audit_levels:
            mark = "✅" if l["passed"] else "❌"
            detail = l.get("detail", "").replace("|", "\\|")[:120]
            lines.append(f"| {l['level']} | {l['name']} | {mark} | {detail} |")
        lines.extend([
            f"",
            f"## 变更文件（{len(self.changed_files)}）",
            f"",
        ])
        for f in self.changed_files:
            lines.append(f"- `{f}`")
        lines.extend([
            f"",
            f"## 成本",
            f"",
            f"- 模型调用: {self.model_calls} 次",
            f"- Token: {self.token_usage.get('total_tokens', 'N/A')}",
            f"",
            f"## Agent 结论",
            f"",
            f"> {self.agent_submission[:500] or '（无）'}",
            f"",
            f"---",
            f"*报告生成时间: {time.strftime('%Y-%m-%d %H:%M:%S')}*",
        ])
        return "\n".join(lines)
