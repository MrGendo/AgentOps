# AgentOps 阶段 B 设计规格（Go 核心执行平台）
- 日期：2026-10-26
- 依据：`requirements.md` §6、§11.1、§13、§16.1-16.3、§17-B；上游设计 docs/architecture.md、database.md、state-machine.md
- 状态：已批准并执行完成

## 1. 目标
交付可独立验收的 Go 核心执行平台：任务管理 API、状态机、PG 持久化、Worker 调度、租约与 fencing、幂等与重试、工具网关（四内置工具）、事件审计，并以 R1-R10 自动化测试证明可靠性。验收口径（需求 §17-B）：**无需真实模型即可运行 Mock 任务，并验证核心可靠性行为**。

## 2. 范围与非范围
**范围**：platform/ 全部业务模块 + 迁移 000002 + /api/v1 中任务相关 8 条路由、/tools、/tool-calls、/health（已有）。
**非范围**：Python agent-run 调用（阶段 C）、评测/诊断/数据集路由（阶段 D）、前端页面（阶段 E）。

## 3. 模块划分（platform/internal/）
```
task/        任务领域：Service（创建幂等/查询/取消/重试）+ Store（SQL）
statemachine/ 纯函数：转换合法性裁决、竞争分支选择（无 IO，L1 全覆盖）
worker/      领取循环、租约心跳、过期回收、任务超时、取消检查点
retry/       错误分类枚举 + 退避计算（纯函数）
gateway/     工具注册表、Schema 校验、权限、超时执行、审计落库
events/      事件追加写（与状态变更同事务）
httpapi/     路由 + 统一错误包络 + request_id 中间件
store/       pgx 池（已有，扩展事务助手）
```

## 4. 数据与迁移
- `migrations/000002_core_tables.up.sql`：按 docs/database.md §2 一次性建 6 张核心表（tasks / task_steps / step_attempts / tool_calls / task_events / agent_runs）。
  - 决策 D-B4：agent_runs 虽在阶段 C 才有写入方，本阶段先建表以稳定迁移基线（表结构已定稿）。
- tool_calls.agent_run_id 暂不加 FK（阶段 C 建），其余外键按文档。
- 大对象仍只存引用；Mock 执行器输出均为小结果，直接内联 result 字段。

## 5. 状态机实现要点
- statemachine 包暴露 `CanTransition(from, to) bool` 与 `ResolveCompletionRace(current, attempt) Outcome`，纯函数可穷举测试全矩阵（state-machine.md §2）。
- 所有状态写入走 Store 的条件 UPDATE（带 status / lease_version），0 行命中即进入竞争分支处理（state-machine.md §3.1 的三条路径）。
- 取消检查点：Worker 在「工具调用返回后」「执行器步骤间」两处检查 CANCEL_REQUESTED，主动落 CANCELLED。

## 6. Worker 与租约设计
- 单进程内 N 个领取 goroutine（cfg.WorkerConcurrency）+ 每任务心跳续租（间隔 = TTL/3）+ 独立 reaper 协程（扫 `RUNNING AND lease_expires_at<now()`，按 state-machine.md §4 处置）。
- 领取 SQL：database.md §3.3（FOR UPDATE SKIP LOCKED + 同事务置 RUNNING/lease_version+1 + task.claimed 事件）。
- 任务超时与租约超时分离判定（R4）：超时错误码 TASK_TIMEOUT（业务上限，直接 FAILED，不自动重试该 attempt 的判定路径）vs LEASE_EXPIRED（活性信号，走恢复）。
- 恢复语义：lease 过期且 attempt_count<max_attempts → 重排 PENDING（scheduled_at=now()）；否则 FAILED(lease_expired)。reaper 幂等：条件更新防双收。

## 7. 重试与错误分类（retry/）
- 枚举：ARGUMENT_INVALID / PERMISSION_DENIED / BUSINESS_REJECTED / MODEL_RATE_LIMITED / TRANSIENT_NETWORK / TOOL_FAILED_CLEAN / LEASE_EXPIRED / TIMEOUT_UNKNOWN / CANCELLED / INTERNAL。
- `Retryable(class)` 与 `Backoff(attempt, base, max, rand)` 纯函数；默认 base=2s max=60s max_attempts=3（.env 可调）。
- TIMEOUT_UNKNOWN 一律不自动重试（R7），任务终态 FAILED 但 finish_reason=timeout_unknown，等阶段 D 的确认流程。

## 8. 工具网关设计（gateway/）
### 8.1 执行管线
```
请求 → 服务令牌校验 → 工具存在性 → 任务 allowed_tools 权限 → JSON Schema 参数校验
     → 幂等键查重 → 超时包裹执行 → 结果落库 tool_calls → 返回
```
拒绝路径返回明确错误码（REJECTED 状态落库留审计）。每步事件：tool.call.started / tool.call.completed。

### 8.2 内置工具规格
| 工具 | 参数 Schema 要点 | 行为 | 风险 | 幂等 |
|---|---|---|---|---|
| calculator | {expression: string}（仅数字与 + - * / ( ) 空格） | 自研递归下降求值（**不使用任何 eval**，决策 D-B2） | LOW | 是（同参同果） |
| knowledge_search | {query: string, top_k?: int<=10} | 本地文件知识库（data/knowledge/*.md，启动加载）关键词条目打分，返回段落+来源 id | LOW | 是 |
| task_status | {task_id: uuid} | 只读查询 tasks 概要（受限工具：仅暴露状态与时间，不暴露 input） | LOW | 是 |
| mock_http | {mode: success\|error\|timeout\|lost_response, delay_ms?: int<=5000, body?: object} | 进程内模拟外部调用四类故障；timeout/lost 产生 TIMEOUT_UNKNOWN | MEDIUM | 按调用 |

- Schema 校验引入 `santhosh-tekuri/jsonschema/v6`（决策 D-B3，唯一新依赖）；或如编译受阻降级为手写四工具校验器（在计划中留分支）。
- knowledge_search 随仓库预置 ops 诊断示例知识库（决策 D-B5），支撑 §3.2 演示场景。

## 9. Mock 执行器（决策 D-B1）
Worker 的 Executor 接口首个实现为内置 mock（Go 进程内，不调 Python）：
| 任务类型 | 行为 | 用途 |
|---|---|---|
| mock.echo | 单步成功，回显 input | 冒烟/演示 |
| mock.fail | 按 params 注入错误类别与次数（如 fail_times, error_class） | R5/R6 重试测试 |
| mock.sleep | sleep params.seconds 后成功 | R4 任务超时测试 |
| mock.tools | 依次调用 calculator + knowledge_search + task_status + mock_http(success) 各一次 | 网关联动与 trace 验收 |
执行器写 steps/attempts/tool_calls 全链路记录，供 /trace 与阶段 C 对齐。

## 10. API 实现清单（阶段 B 内）
POST/GET /tasks、GET /tasks/{id}、POST cancel、POST retry、GET events、GET trace、GET /tools、POST /tool-calls。
- 统一错误包络 + request_id（api-contract.md §1、§4 错误码表全量实现）。
- 列表游标分页（created_at+id 复合键）。

## 11. 测试计划（testing-strategy.md §2 映射）
- L1：statemachine 全矩阵、Backoff 边界、calculator 求值（含非法输入拒绝）、四工具 Schema 校验、错误分类表。
- L2（agentops_test，build tag integration）：R1 并发领取、R2 过期恢复（直接 UPDATE lease_expires_at 模拟崩溃）、R3 fencing、R4 双超时区分、R5/R6 重试边界、R7 TIMEOUT_UNKNOWN、R8 事务失败（约束冲突注入）、R9 幂等并发、R10 取消/完成竞争。
- L4 冒烟：mock.tools 任务全链路（创建→领取→工具→SUCCEEDED→trace 可查）。

## 12. 风险与对策
| 风险 | 对策 |
|---|---|
| jsonschema 依赖在受限网络编译失败 | goproxy.cn 已通；仍失败则切手写校验器（D-B3 分支） |
| SKIP LOCKED 在高并发下的公平性 | MVP 无 SLA 要求；测试只断言正确性不断言公平性 |
| reaper 与在跑 Worker 的竞争 | reaper 夺回也走条件 UPDATE（lease_version），天然 fencing |
| 事件表写入放大 | 每任务事件量级 <100，MVP 不做分区；文档已留演进 |

## 13. 验收标准（对照 §16.1/16.2/16.3）
1. 16.1 六项全过：幂等创建、非法转换拒绝、终态不可覆盖、取消竞争有裁决、手动重试留痕。
2. 16.2 八项（R1-R8）+ R9/R10 全部有自动化测试且真实通过。
3. 16.3 工具网关七项：未注册拒绝、参数错拒绝、无权限拒绝、超时留痕、结果关联、幂等、风险约束。
4. `make test`（含 integration）全绿；README 命令更新并实测。
