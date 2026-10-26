# AgentOps 阶段 D 任务计划
- 日期：2026-10-26
- 规格：`docs/superpowers/specs/2026-10-26-agentops-phase-d-design.md`
- 状态：待审批

## 任务清单
### 数据与数据集
- [x] T1 迁移 000003（evaluation/diagnosis 五表）+ dev/test 双库执行 + up/down 验证
- [x] T2 Go `internal/eval` 数据集：JSON/JSONL 双格式导入、name+version 冲突 409、列表 + 集成测试；预置 `agent/evaldata/*.jsonl`
### 评测
- [x] T3 Python 评分与指标：规则引擎 + 口径显式的指标聚合（含 caliber 字段）+ 单测
- [x] T4 Python 评测执行器：逐样例建任务（幂等键）、轮询终态、取 trace 评分、容错（case 超时记 ERROR）+ 桩 Go 单测
- [x] T5 Python `/internal/v1/evaluations` POST/GET 异步实现 + 单测
- [x] T6 Go 评测协调器：建 run → 调 Python → 轮询 → 写 evaluation_results/metrics + 桩 Python 集成测试
- [x] T7 Go `/api/v1/evaluations` 创建/列表/详情 + 集成测试
### 诊断
- [x] T8 Python 诊断器：分类全表、置信度、证据引用、建议模板、证据不足 LOW + 全表单测（断言零副作用）
- [x] T9 Go `internal/diagnosis`：证据包组装、同步触发（15s 上限）、落库、GET 端点 + 桩集成测试
### 集成与收尾
- [x] T10 契约更新（internal-v1/api-v1 yaml + api-contract.md）+ 双端契约测试更新
- [x] T11 L5 双进程端到端：demo-mixed 真实评测（成功/重试/失败样例）指标断言；失败任务诊断引用真实事件；双 agent_version 对比数据
- [x] T12 README（评测与诊断演示、指标口径说明）+ 任务日志 + git 提交

## 验收对照
- 需求 §17-D 与 §16.5 七条 → T2/T4/T7/T8/T9/T11 测试证据
