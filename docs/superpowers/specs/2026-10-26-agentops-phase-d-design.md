# AgentOps 阶段 D 设计规格（自动评测与失败诊断）
- 日期：2026-10-26
- 依据：`requirements.md` §8、§9、§11、§16.5、§17-D；架构 D-1（评测/诊断计算在 Python、落库一律经 Go，Python 不直连库）
- 状态：已批准并执行完成

## 1. 目标
交付数据集管理、单样例/批量评测与分项指标、版本对比数据、故障分类与证据引用式诊断报告。验收（§16.5）：JSONL 可导入、单样例与批量评测可执行、分项指标可计算、不同 Agent 版本可对比、诊断关联真实事件、证据不足明确标注不确定、诊断不绕过权限执行修复。

## 2. 数据流与职责（延续拉取模型，与阶段 C 一致）
```
用户 POST /api/v1/datasets（JSON 或 JSONL）──▶ Go 解析并落 evaluation_datasets/cases
用户 POST /api/v1/evaluations ──▶ Go 建 evaluation_runs(PENDING) ──▶ 协调器调 Python
   Python 逐样例：POST /api/v1/tasks（幂等键 eval:{run}:{seq}）→ 轮询终态 → 确定性规则评分 → 聚合指标
Go 协调器轮询 GET /internal/v1/evaluations/{id} ──▶ 终态后由 Go 写 evaluation_results + metrics + 状态
诊断：GET /api/v1/tasks/{id}/diagnosis ──▶ 无报告且任务已终态时：Go 组证据包 → POST /internal/v1/diagnoses → 轮询 → Go 落 diagnosis_reports
```
- **全部数据库写入仍只发生在 Go**（D-1 落地，无新增对外写接口）。
- Python 进程内评测状态与 agent_runs 同样易失：丢失时 Go 将 evaluation_runs 置 FAILED(EVALUATION_ERROR)，可重建重跑（样例任务带幂等键，重跑不产生重复任务）。

## 3. 迁移 000003：五张表（蓝本 docs/database.md §2.7-2.11）
evaluation_datasets / evaluation_cases / evaluation_runs / evaluation_results / diagnosis_reports。

## 4. 数据集（Go 实现）
- POST /api/v1/datasets：支持 `application/json`（{name,version?,cases[]}）与 `application/x-ndjson`（每行一个 case，name 从首行 meta 或查询参数）；name+version 唯一，冲突 409 DATASET_VERSION_EXISTS。
- 校验：input_task 必填且含 task_type；scoring_rules 为规则数组；case_count 落库。
- 预置种子：`agent/evaldata/demo-mixed.jsonl`（mock.echo 成功 / mock.fail 可重试 / mock.tools 多工具 / mock.fail 参数错失败）、`agent/evaldata/ops-diagnosis.jsonl`（agent.ops_diagnosis 双版本演示用）。

## 5. 评分引擎（Python，确定性规则优先 §8.4）
规则类型（case.scoring_rules[]）：
| 规则 | 语义 |
|---|---|
| field_present | 终态输出 JSON 指定路径存在 |
| contains_text | 执行输出含指定关键词（Agent 终态输出或任一成功步骤输出，证据可出现在任一环节） |
| numeric_range | 指定数值处于 [min,max] |
| tools_used | 期望集合 ⊆ 实际（subset：该用的都用了）或集合完全一致（exact） |
| finished_normally | finish_reason == COMPLETED |
每规则输出 {name, pass, evidence}；**结果正确率 = 全规则通过**。LLM-as-a-Judge 本阶段不实现（接口位留 in run_config.judge，默认关）。

## 6. 指标聚合（口径显式，§8.3）
| 指标 | 口径 |
|---|---|
| task_success_rate | per-task：终态 SUCCEEDED 任务数/样例数 |
| result_correctness | per-task：全规则通过样例/样例数 |
| tool_call_accuracy | per-call：合格调用/总调用；合格=状态 SUCCEEDED 且工具 ∈ allowed |
| step_completion_rate | per-step：SUCCEEDED 步骤/总步骤（全部样例任务聚合） |
| avg_duration_ms | per-task：mean(finished_at-started_at) |
| avg_tool_calls | per-task：总调用数/样例数 |
| retry_rate | per-task：attempt_count>1 的任务/样例数 |
| token_usage | 汇总 agent_runs 的 prompt/completion/total |
| cost_estimate | run_config.pricing{input_per_1k,output_per_1k}×用量，默认 0 |
综合得分不提供（§8.3 禁止单一得分掩盖分项）。

## 7. 评测执行器（Python）
- 并发：样例间串行（MVP 足够，避免打满 Worker；run_config.concurrency 预留）。
- 每样例：建任务（带允许工具与限制）→ 轮询 GET /api/v1/tasks/{id} 至终态（上限 run_config.case_timeout_seconds 默认 120s）→ 评分（取 /trace 汇总步骤与调用）。
- 单样例评测 = cases 数为 1 的数据集，同一代码路径。

## 8. 诊断器（Python，规则分类）
- 输入：Go 组装的证据包（task 概要、steps/attempts 错误、tool_calls 状态、关键事件、agent_run 摘要）。
- 分类规则（优先级自上而下，命中即止；多信号并存时取最高优先级并降低置信度）：
| 证据 | 故障码 | 置信度 |
|---|---|---|
| agent_run.error.code ∈ {RATE_LIMITED,PROVIDER_ERROR,INVALID_OUTPUT,MODEL_TIMEOUT} | MODEL_ERROR | HIGH |
| plan_valid=false / finish_reason=INVALID_PLAN | PLANNING_ERROR | HIGH |
| tool_calls 存在 REJECTED 且 error_code=PERMISSION_DENIED | POLICY_VIOLATION | HIGH |
| tool_calls 存在 REJECTED 且 error_code=ARGUMENT_INVALID | TOOL_ARGUMENT_ERROR | HIGH |
| tool_calls 存在 TIMEOUT_UNKNOWN | TIMEOUT_OR_UNKNOWN | HIGH（need_human=true） |
| tool_calls 存在 FAILED | TOOL_EXECUTION_ERROR | HIGH |
| 步骤 error_class ∈ {TOOL_SELECTION...} / 调用了 allowed 之外工具 | TOOL_SELECTION_ERROR | MEDIUM |
| 事件 lease.revoked / error_code=LEASE_EXPIRED | ORCHESTRATION_ERROR | HIGH |
| 任务 error_code=ARGUMENT_INVALID（输入级） | TOOL_ARGUMENT_ERROR | MEDIUM |
| 评测执行异常 | EVALUATION_ERROR | HIGH |
| 无充分信号 | CONTEXT_ERROR | LOW（根因假设明确写"证据不足"） |
- 输出含：根因假设、建议清单（按故障码模板）、evidence[]（引用真实 step/call/event id）、diagnoser_version。
- 只分析不修复：不调用任何工具、不写任务状态（§9.3）。

## 9. Go 侧模块
- `internal/eval`：datasets 导入/查询、runs 创建/列表/详情、协调器（调 Python + 轮询 + 落库）。
- `internal/diagnosis`：证据包组装（复用 trace 查询）、触发与落库、GET 报告；无报告且终态时同步生成（上限 15s，Python Mock 诊断 <1s），超时返回现有空列表 + meta.generating。
- API 新增/启用：GET/POST /datasets、GET/POST /evaluations、GET /evaluations/{id}、GET /tasks/{id}/diagnosis。

## 10. 契约更新
internal-v1.yaml：evaluations POST/GET、diagnoses POST 字段补全（含 metrics 口径标注）；api-v1.yaml：datasets/evaluations/diagnosis 路由与错误码；api-contract.md 增补两处。契约测试同步。

## 11. 测试计划
- Python 单测：规则引擎（各规则通过/失败/证据）、指标口径（构造数据断言分子分母）、诊断分类全表 + 证据不足 LOW、评测执行器（MockTransport 桩 Go：建任务/查任务/查 trace）。
- Go 集成：迁移、数据集导入冲突、eval 协调器（桩 Python）落库、诊断（桩 Python）落库与 GET。
- L5 双进程：demo-mixed 数据集真实评测（含成功/重试/失败样例）→ 指标断言；对失败任务请求诊断 → 报告引用真实事件；同数据集双 agent_version 跑两次 → 版本对比数据可查。

## 12. 风险与对策
| 风险 | 对策 |
|---|---|
| 评测中样例任务悬挂拖长整体 | 每样例 case_timeout + 整体 deadline，超时样例记 ERROR 不阻塞 |
| Python 评测态丢失 | Go 置 FAILED(EVALUATION_ERROR)；幂等键保证重跑安全 |
| 指标口径被误读 | metrics JSON 内嵌 "caliber"（口径说明）字段 |

## 13. 验收标准（§16.5 七条逐条对应测试）
JSONL 导入（T2/T11）、单样例+批量（T4/T7/T11）、分项指标（T3/T11）、版本对比（T11）、诊断关联真实事件（T8/T9/T11）、证据不足标不确定（T8）、诊断不绕权限（T8 断言零副作用 + 代码审查说明）。
