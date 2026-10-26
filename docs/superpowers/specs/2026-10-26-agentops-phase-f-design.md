# AgentOps 阶段 F 设计规格（集成与最终验收）
- 日期：2026-10-26
- 依据：`requirements.md` §16.7、§17-F、§13、§14、§19
- 状态：已批准并执行完成

## 1. 目标
收官：端到端演示、故障注入复现、并发压测脚本、安全检查清单、观测指标脚本、Docker Compose 全栈终验、最终验收报告（§16.1-16.7 逐条证据 + 已知限制 + 服务化演进建议）。验收（§17-F）：**本地可启动、主要流程可演示、关键故障可复现、已知限制明确**。

## 2. 交付物明细
### 2.1 演示脚本 `scripts/demo.sh`
一键复现完整闭环（§19）：起双后端 → 创建 agent.ops_diagnosis 任务 → 轮询至 SUCCEEDED → 打印轨迹摘要 → 导入数据集并跑评测 → 打印指标 → 对失败样例取诊断 → 收尾清理。输出人类可读、可粘贴进报告。
### 2.2 故障注入复现 `scripts/faults/`
- `kill_worker.sh`：单独 Worker 进程（AGENTOPS_ROLE=worker）领取长任务后 kill -9 → 观察 reaper 恢复（另一 Worker 或重启后接管）。
- `expire_lease.sql`：手动将租约置过期（测试/演示复现入口）。
- `timeout_task.sh`：提交 mock_http timeout 模式任务演示 TIMEOUT_UNKNOWN 不自动重试。
配套自动化证据：R1-R10/L4/L5 已有测试名在报告中引用。
### 2.3 性能脚本 `scripts/bench.sh`
- 并发提交 N（默认 200）个 mock.echo 任务（可调并发度），统计：提交吞吐、全部完成耗时、任务耗时分布（P50/P95/P99，来自真实 tasks 表）。
- API 延迟：GET /tasks/{id} 与 /tasks 列表各打 100 次取 P95。
- 定位为**测量脚本**（非 SLA 承诺）；结果写入报告并注明环境。
### 2.4 观测指标 `scripts/metrics.sh`（make metrics）
§14.2 全项从真实库计算：任务总量/成功率/失败率/待执行/执行中/平均时长、工具调用成功率/失败率、重试次数、租约过期次数（lease.revoked 事件）、Agent 平均迭代、模型 Token（agent_runs）、评测数量与时长。
### 2.5 安全检查 `docs/security-checklist.md`
逐项给证据：服务令牌强制（现有测试）；密钥不进代码/日志（grep 扫描脚本 + 代码审查记录）；不可信输入处理（calculator pattern 拒绝注入、JSONL 逐行校验、检索内容不改权限——测试引用）；内部接口网络边界（compose 无公网映射说明）；高风险工具缺失（无 shell/exec 类工具声明）；CORS 未开放（gin 默认）。
### 2.6 Compose 全栈终验
重建三镜像 → `docker compose up -d` → 四服务健康 → 经 8080 走一条 agent 任务全链路 → 控制台 5173 可达 → down。此前仅在阶段 A 验证过，阶段 B-E 大量变更后需终验（预期需修 Dockerfile 细节：console 新依赖、agent 镜像内容等）。
### 2.7 最终验收报告 `docs/final-acceptance-report.md`
- §16.1-16.7 逐条：通过项 → 证据（测试名 + 关键输出数字）
- 全量测试总跑结果（四类：Go 单元/Go 集成/Python/console）
- 真实性能数据（bench 输出）与观测基线（metrics 输出）
- 已知限制（含：无浏览器自动化测试、评测串行、Python 运行态易失、无认证/RBAC、性能数据仅本机）
- 服务化演进建议（对齐架构文档 §9/需求 §18）
### 2.8 文档终审
README 命令逐条与实际核对（启动/迁移/测试/演示/故障复现）；补「故障复现方式」章节索引。

## 3. 测试与验收
- 全量总跑：`make test && make test-integration && make test-py && cd console && npm test && npm run build` 一次全绿，输出进报告。
- demo.sh / faults / bench / metrics 各实跑一次，输出进报告。
- compose 终验通过或如实记录阻塞（如镜像源限速）。

## 4. 风险
| 风险 | 对策 |
|---|---|
| compose 重建遇镜像源限速 | 已配 daocloud 镜像源；仍失败则记录为已知限制并给原生启动等价路径 |
| 本机性能数据波动 | 报告注明环境与单次运行，不承诺指标 |
| 演示脚本依赖运行中服务状态 | 脚本自起自清，幂等可重复 |
