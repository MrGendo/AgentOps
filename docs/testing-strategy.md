# AgentOps 测试策略
- 版本：1.0（阶段 A 定稿）
- 依据：`requirements.md` §16（验收标准）、§6-§9（各模块行为）
- 原则：验收只认真实执行结果；未执行的测试必须标记为未执行（§16.7）。

## 1. 测试分层
| 层 | 范围 | 工具 | 运行条件 |
|---|---|---|---|
| L1 单元测试 | 纯函数：状态机裁决、退避计算、参数校验、指标统计口径、脱敏 | Go testing / pytest | 无外部依赖 |
| L2 集成测试 | 真实 PG 上的 store 层：领取原子性、幂等、fencing、事件全序、迁移 | Go testing（build tag `integration`）/ pytest | 本地 PG，`agentops_test` 库 |
| L3 跨语言契约测试 | Go 与 Python 实现对 OpenAPI 契约的一致性（样例请求/响应双向校验） | Go testing + pytest | 无外部依赖（用契约样例） |
| L4 端到端 | 提交任务→调度→Agent→工具→评测→诊断 全链路（Mock 模型） | Go 集成测试 + shell smoke 脚本 | PG + 双进程 |
| L5 故障注入 | Worker kill -9、租约过期、PG 重启、工具超时/响应丢失、Python 不可用 | 脚本化注入 + 自动断言 | 同 L4 |

## 2. 关键可靠性场景（§16.2 → 自动化测试映射）
| # | 验收要求 | 测试手法 | 断言 |
|---|---|---|---|
| R1 | 多 Worker 并发不重复领取 | 8 goroutine 并发 claim 同一批任务（L2） | 每任务恰好 1 个领取者；无互等死锁 |
| R2 | Worker 强杀后可恢复 | 起真实 Worker 领取后 `kill -9`，推进时钟（缩短 ttl），跑恢复器（L5） | 任务回到 PENDING 并被新 Worker 接管，事件含 lease.revoked |
| R3 | 旧租约 Worker 无法覆盖新状态 | 两个连接分别持 v 与 v+1，旧者再写（L2） | 旧者 0 行命中返回 LEASE_STOLEN，新者数据不变 |
| R4 | 任务超时 vs 租约超时区分 | 分别构造 timeout_seconds 极小 / lease ttl 极小（L2） | 错误码分别为 TASK_TIMEOUT / LEASE_EXPIRED，语义不同 |
| R5 | 可重试错误遵守次数与退避 | 注入 TRANSIENT_NETWORK，max_attempts=3（L2） | attempt 恰好 3 次，间隔服从退避+抖动上界，最终 FAILED |
| R6 | 不可重试错误不无限重试 | 注入 ARGUMENT_INVALID（L2） | 1 次尝试即 FAILED |
| R7 | 超时未知结果不宣称副作用未发生 | mock_http 工具设 timeout 模式（L5） | tool_calls.status=TIMEOUT_UNKNOWN，无自动重试，事件留痕 |
| R8 | DB 事务失败不谎报成功 | 注入约束冲突/连接中断（L2） | 返回明确错误，无部分落库 |
| R9 | 幂等键重复提交 | 并发同 idempotency_key 创建（L2） | 仅 1 条任务，重复请求返回同一 id |
| R10 | 取消与完成竞争 | 并发触发 cancel 与 complete（L2） | 恰一个终态；finish_reason 标记竞争；事件可重建时序 |

## 3. 各阶段验收映射（§16 → 交付测试）
| 验收块 | 覆盖阶段 | 主要测试位置 |
|---|---|---|
| 16.1 任务管理 | B | platform/internal/task/*_test.go（L1/L2） |
| 16.2 执行可靠性 | B | platform/internal/lease、worker（L2/L5），见 §2 表 |
| 16.3 工具网关 | B | platform/internal/gateway（L1/L2 + mock_http 故障模式） |
| 16.4 Agent | C | agent/tests/runtime（L1/L3/L4） |
| 16.5 评测与诊断 | D | agent/tests/eval、diagnosis（L1，含「证据不足标不确定」） |
| 16.6 前端 | E | console 构建冒烟 + 真实 API 联调（手动清单 + 构建） |
| 16.7 工程 | A-F | Makefile：make test-go / test-py / build-console / compose-up |

## 4. 测试环境与数据
- Go 集成测试连 `agentops_test`；每个测试用例独立 TRUNCATE 核心表（并行测试用独立 schema：`platform_test_<seq>`，测后清理）。
- Python 测试不依赖 PG（数据经接口 mock）；契约测试直接读 `docs/openapi/*.yaml`。
- 故障注入手段固化为 `scripts/faults/`：kill_worker.sh、expire_lease.sql、tool_fault_config.json、stop_python.sh。

## 5. 性能与指标验证（阶段 F）
- 基准脚本（非硬性 SLA）：并发 20 Worker 领取 1000 任务的最长等待、事件写入吞吐、任务详情 API P95。
- 所有指标数据必须来自真实运行记录（§14.2），禁止演示用假数据。

## 6. 阶段 A 本阶段可验证项（当前已具备）
| 项 | 命令 | 判定 |
|---|---|---|
| Go 骨架构建+测试 | `cd platform && go build ./... && go test ./...` | 通过 |
| Python 骨架测试 | `cd agent && .venv/bin/pytest` | 通过 |
| 前端构建 | `cd console && npm run build` | 通过 |
| 迁移可执行 | `make migrate-up`（agentops_dev） | schema_migrations 就位 |
| 三端健康 | curl Go/Python health + 前端调 Go health | 200 + 真实依赖状态 |
