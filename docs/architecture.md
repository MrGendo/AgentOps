# AgentOps 架构设计
- 版本：1.0（阶段 A 定稿）
- 日期：2026-10-26
- 依据：`requirements.md` V1.0（§4 架构、§4.2 职责边界、§4.3 先模块化后服务化）
- 关联文档：[database.md](database.md)、[state-machine.md](state-machine.md)、[api-contract.md](api-contract.md)、[testing-strategy.md](testing-strategy.md)

## 1. 总体分层

```
┌─────────────────────────────────────────────────────────┐
│ 交互层    REST API（/api/v1） + React 管理控制台          │
├─────────────────────────────────────────────────────────┤
│ 核心执行层（Go / platform）                              │
│   任务生命周期 · 状态机 · Worker 调度 · 租约与恢复        │
│   工具网关（注册/校验/权限/执行/审计） · 事件记录          │
├─────────────────────────────────────────────────────────┤
│ 智能与质量层（Python / agent）                           │
│   Agent Runtime（规划/工具循环/限制） · 模型适配           │
│   自动评测 · 失败诊断 · 数据集管理                        │
├─────────────────────────────────────────────────────────┤
│ 基础设施层   PostgreSQL（唯一必需外部服务） · Docker Compose │
└─────────────────────────────────────────────────────────┘
```

## 2. 进程拓扑（模块化阶段）

```
浏览器 ──HTTP──▶ Go Server (Gin, :8080) ──SQL(pgx)──▶ PostgreSQL (:5432)
                    │  ▲
       下发 agent-run│  │工具调用（经网关）
                    ▼  │
               Python FastAPI (:8000, /internal/v1)
               （Agent 运行 / 评测 / 诊断 / 模型适配）

Go Worker：与 Server 同二进制，`--role=all|server|worker`；
模块化阶段默认单进程内嵌 Worker goroutine，生产语义与独立进程完全一致
（领取/租约/fencing 全部以数据库为协调点，进程内无共享状态）。
```

要点：
1. **Go 是任务状态的唯一权威。** tasks / task_steps / step_attempts / tool_calls / task_events 只经 Go 写入；Python 不直连数据库。
2. **Python 通过 Go 提供的 HTTP 接口读数据、调工具**；评测与诊断的计算在 Python，落库经 Go 写接口（见 §4.2 决策 D-1）。
3. 两个本地进程即可跑通全链路，为阶段 F 服务化拆分保留边界。

## 3. 仓库与目录结构

```
AgentOps/
├── platform/          # Go 核心执行平台
│   ├── cmd/           # server（含 worker）、migrate
│   ├── internal/      # config / httpapi / store / task / lease / gateway / events
│   └── migrations/    # golang-migrate 版本化 SQL
├── agent/             # Python 智能与质量模块
│   ├── app/           # main（FastAPI）、runtime、models、eval、diagnosis
│   └── tests/
├── console/           # React + TS 管理控制台（Vite）
├── deploy/            # docker-compose.yml、.env.example
├── data/              # 大对象产物（模型原始 IO、轨迹、结果文件），表内只存引用
└── docs/              # 设计文档、openapi、superpowers、task-log
```

## 4. 职责边界（对齐需求 §4.2）

| 能力 | Go 核心平台 | Python 智能与质量模块 |
|---|---|---|
| 任务状态 | 唯一权威来源 | 只读取必要状态（经 Go API） |
| Worker 调度 / 租约 / 恢复 | 负责 | 不负责 |
| 工具权限校验 / 执行 | 网关统一执行、最终校验 | 只能提出调用请求 |
| 模型调用 / Agent 计划生成 | 不承担推理；校验并保存记录 | 负责 |
| 执行轨迹 | 持久化关联信息 | 提供 Agent 侧轨迹 |
| 自动评测 | 提供数据与任务管理接口 | 负责评测逻辑 |
| 失败诊断 | 提供事件、步骤、工具证据 | 分析并生成诊断 |
| 数据库写入 | 核心表唯一写入方 | 不直连库 |

## 5. 跨语言协作

### 5.1 调用方向
- **Go → Python（执行侧）**：`POST /internal/v1/agent-runs|evaluations|diagnoses`，全部异步模式（202 + id，GET 轮询）。
- **Python → Go（数据与工具侧）**：
  - `POST /api/v1/tool-calls`：工具网关调用（Python 在 agent-run 执行期内同步调用）；
  - `GET /api/v1/tasks/{id}` 等只读接口：获取任务上下文与证据数据。

### 5.2 契约治理
- OpenAPI 3.1：`docs/openapi/api-v1.yaml`（Go 对外）与 `internal-v1.yaml`（Python 内部），双端实现必须与 yaml 对齐。
- 阶段 C 增加契约测试：以同一份 yaml 生成/校验两侧的请求响应样例，防漂移。
- 内部调用带服务令牌（`SERVICE_TOKEN` 环境变量），网络层面仅绑定本机/内网。

### 5.3 对需求 §11.1 端点表的补充（显式声明）
需求表格未列出但链路必需的两个端点，本设计补充并在 [api-contract.md](api-contract.md) 定稿：
1. `POST /api/v1/tool-calls`——Python Agent 执行期经网关调用工具的唯一入口（§6.6「所有工具调用必须经过 Go 网关」的落地）；
2. `POST /api/v1/datasets`——JSONL 数据集导入（§8.1 的入口）。

### 5.4 一次任务的完整时序
```
用户 ──POST /api/v1/tasks──▶ Go：INSERT tasks(PENDING) + event(创建)
Go Worker：SELECT..FOR UPDATE SKIP LOCKED 领取 → RUNNING(lease v1) + event(领取)
Go ──POST /internal/v1/agent-runs(202)──▶ Python：模型生成结构化计划
Python ──POST /api/v1/tool-calls──▶ Go 网关：权限/Schema/超时校验 → 执行 → tool_calls 落库
Python：根据结果继续推理（循环，受限）→ 返回最终输出与轨迹
Go：持久化 agent_runs/轨迹引用 → 任务 SUCCEEDED/FAILED + event(结束)
（自动）Python 评测 / 失败诊断 → 结果经 Go 写接口落库
用户/前端：GET /api/v1/tasks/{id}/trace|events|diagnosis 查看全链路
```

## 6. 可靠性机制总览（阶段 B 实现细则见关联文档）

| 机制 | 方案 | 详见 |
|---|---|---|
| 并发领取 | `FOR UPDATE SKIP LOCKED` 原子领取 | database.md §事务策略 |
| 旧 Worker 写入保护 | `lease_version` fencing：更新条件携带领取时的版本号 | state-machine.md §5 |
| 幂等创建 | `idempotency_key` 唯一约束 + 冲突返回已存在任务 | database.md §2.1 |
| 取消/完成竞争 | 数据库条件更新原子裁决，先到先得 + 事件标记 | state-machine.md §4 |
| 外部超时未知结果 | `TIMEOUT_UNKNOWN` 独立状态，禁止自动重试 | state-machine.md §6 |
| 事件全序 | `task_events.seq bigserial`，时间戳仅展示 | database.md §2.6 |

## 7. 大对象与脱敏
- 模型原始输入输出、Agent 轨迹、最终报告等大文本/文件存 `data/`（按 task_id 分目录），表内只存 `*_ref` 相对引用。
- 保留策略可配置（保留天数、是否保留原始 IO）；日志输出经脱敏（密钥、Token 正则遮蔽）。
- API 响应默认返回摘要，完整内容经专用端点按需拉取。

## 8. 服务故障隔离（需求 §13.5）
- Python 不可用：Go 任务进入可重试失败（错误码 `AGENT_SERVICE_UNAVAILABLE`），查询/历史/取消不受影响。
- 模型不可用：只影响 agent-run 结果，已持久化状态不被破坏。
- PG 不可用：写操作如实返回 `DB_UNAVAILABLE` 错误，不谎报成功；读操作返回降级错误。

## 9. 服务化演进（需求 §18 映射）
| 模块 | 演进方向 | 触发条件 |
|---|---|---|
| Go Worker | Execution Service | Worker 需独立扩容 |
| 工具网关 | Tool Gateway Service | 工具规模/权限体系增长 |
| Agent Runtime | Agent Runtime Service | 模型资源需隔离 |
| 评测/诊断 | Evaluation/Diagnosis Service | 批量评测影响在线任务 |
| 查询 | Console Query API | 读写负载分离 |

拆分时保留 API 契约、任务 ID、事件结构与自动化测试；引入 MQ/工作流引擎前须按 §18 补齐投递幂等、重放、死信设计，不得以「换了调度方式」代替可靠性论证。

## 10. 已决策事项
| # | 决策 | 理由 |
|---|---|---|
| D-1 | 评测/诊断表与核心表同库，但写入一律经 Go 内部写接口，Python 不直连库 | 满足 §4.2「数据库核心状态写入由 Go 负责」与 §10.9 隔离性；阶段 D 前与需求方复核 |
| D-2 | Worker 与 Server 同二进制，角色由启动参数决定 | 模块化阶段最简；DB 是唯一协调点，拆进程零语义变化 |
| D-3 | 补充 tool-calls、datasets 两个端点 | §5.3；否则 §6.6/§8.1 无落地入口 |
| D-4 | 事件排序以 bigserial 为主序 | §6.7 不依赖跨机器墙上时钟 |
