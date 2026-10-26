# AgentOps 阶段 F 任务计划
- 日期：2026-10-26
- 规格：`docs/superpowers/specs/2026-10-26-agentops-phase-f-design.md`
- 状态：待审批

## 任务清单
- [x] T1 `scripts/demo.sh` 全链路演示脚本 + 实跑输出留存
- [x] T2 `scripts/faults/{kill_worker.sh, expire_lease.sql, timeout_task.sh}` + README 复现章节
- [x] T3 `scripts/bench.sh` 并发压测与延迟统计 + 实跑
- [x] T4 `scripts/metrics.sh`（make metrics）§14.2 全项 + 实跑
- [x] T5 `docs/security-checklist.md` 安全检查清单（逐项证据）+ 密钥扫描脚本
- [x] T6 Docker Compose 全栈终验（重建+起栈+全链路一条任务+控制台可达）
- [x] T7 全量测试总跑（四类一次全绿，输出记录）
- [x] T8 `docs/final-acceptance-report.md` 最终验收报告（§16 逐条证据/已知限制/演进建议）
- [x] T9 README 终审对齐 + 任务日志 + git 提交

## 验收对照
- 需求 §17-F 六项交付 → T1-T8
- §16.7 工程验收八条 → T6-T8 证据
