# AgentOps API 契约
- 版本：1.0（阶段 A 定稿）
- 依据：`requirements.md` §11；机器可读契约见 [openapi/api-v1.yaml](openapi/api-v1.yaml)、[openapi/internal-v1.yaml](openapi/internal-v1.yaml)
- 约束：yaml 是唯一权威，代码与 yaml 漂移以 yaml 为准修代码（阶段 C 起由契约测试强制）。

## 1. 通用规范
1. Go 对外 `/api/v1`；Python 内部 `/internal/v1`（仅本机/内网 + `X-Service-Token`）。
2. 统一响应包络：成功 `{data, meta}`；错误 `{error: {code, message, retryable, request_id, details}}`（§11.3）。
3. 长任务（agent-run / 评测 / 诊断）一律异步：`POST` 返回 `202 {data:{id,status}}`，`GET .../{id}` 轮询，终态后不再变化。
4. 列表接口：游标分页 `?limit=20&cursor=...`，响应 `meta.next_cursor`；按状态/类型/时间过滤。
5. 请求体上限 1MB（内部接口同）；服务端统一 30s 读超时；幂等创建带 `Idempotency-Key` 头或 body 字段。
6. 时间戳 ISO-8601 UTC；ID 一律 UUID。

## 2. Go 对外端点（/api/v1，14 个）
| # | 方法 路径 | 语义 | 备注 |
|---|---|---|---|
| 1 | POST /tasks | 创建任务（幂等） | 冲突返回已存在任务（200+already_exists 标记或 409，见 yaml） |
| 2 | GET /tasks | 任务列表 | 过滤：status/type/created_from/created_to |
| 3 | GET /tasks/{id} | 任务详情 | 含 steps 概览 |
| 4 | POST /tasks/{id}/cancel | 请求取消 | 非终态可取消；终态返回 409 TASK_ALREADY_FINISHED |
| 5 | POST /tasks/{id}/retry | 手动重试 | 仅 FAILED/CANCELLED；记录操作事件 |
| 6 | GET /tasks/{id}/events | 事件时间线 | 按 seq 升序，游标分页 |
| 7 | GET /tasks/{id}/trace | 执行轨迹 | 汇聚 steps+attempts+tool_calls+agent_run 概览 |
| 8 | GET /tools | 工具清单 | 含权限要求、风险等级、超时、幂等声明 |
| 9 | POST /tool-calls | 工具调用（网关入口） | 补充端点（架构文档 D-3）；服务令牌 + 任务授权校验 |
| 10 | GET /evaluations | 评测列表 | |
| 11 | POST /evaluations | 创建评测任务 | 触发 Python 批量评测（异步） |
| 12 | GET /evaluations/{id} | 评测进度与结果 | 含分项指标 |
| 13 | GET /datasets | 数据集列表 | |
| 14 | POST /datasets | 导入 JSONL 数据集 | 补充端点（D-3）；name+version 唯一 |
| 15 | GET /tasks/{id}/diagnosis | 诊断报告 | |
| 16 | GET /health | 健康检查 | {status, dependencies:{postgres, agent_service}} |

> 健康检查端点计入后共 16 条路由；§11.1 表要求的 13 条全部覆盖，另补充 tool-calls / datasets-POST / health（health 在骨架阶段已实现）。

## 3. Python 内部端点（/internal/v1，6 个）
| # | 方法 路径 | 语义 |
|---|---|---|
| 1 | POST /agent-runs | 发起 Agent 运行（异步 202） |
| 2 | GET /agent-runs/{id} | 查询运行状态与结果（含轨迹摘要） |
| 3 | POST /evaluations | 执行评测（异步 202） |
| 4 | GET /evaluations/{id} | 查询评测结果 |
| 5 | POST /diagnoses | 分析任务轨迹并生成诊断（异步 202） |
| 6 | GET /health | 健康检查 {status, version} |

## 4. 错误码表（稳定值，客户端可编程依赖）
| code | HTTP | retryable | 场景 |
|---|---|---|---|
| VALIDATION_ERROR | 400 | false | 参数/Schema 校验失败 |
| TASK_NOT_FOUND | 404 | false | 任务不存在 |
| TASK_ALREADY_FINISHED | 409 | false | 终态任务取消/重试被拒 |
| IDEMPOTENCY_CONFLICT | 409 | false | 同键不同内容 |
| PERMISSION_DENIED | 403 | false | 工具/操作未授权 |
| LEASE_STOLEN | 409 | false | 旧租约写入被 fencing 拒绝 |
| RATE_LIMITED | 429 | true | 模型限流透传 |
| AGENT_SERVICE_UNAVAILABLE | 503 | true | Python 不可用 |
| DB_UNAVAILABLE | 503 | true | PG 不可用（不谎报成功） |
| TOOL_TIMEOUT_UNKNOWN | 504 | false | 工具超时、结果未知（禁止自动重试） |
| INTERNAL_ERROR | 500 | false | 兜底 |

## 5. 变更策略
- 破坏性变更必须升版本（/api/v2）；同版本内只加字段不删字段。
- 每次契约变更同步更新两份 yaml + 契约测试样例，随代码同 PR/同提交。
