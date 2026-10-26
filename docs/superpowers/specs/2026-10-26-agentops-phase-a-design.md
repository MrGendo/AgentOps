# AgentOps 阶段 A 设计规格（需求分析与项目骨架）

- 日期：2026-10-26
- 依据：`requirements.md` V1.0（§17 阶段 A）
- 状态：已批准并执行完成

## 1. 目标

完成阶段 A 交付：五份设计文档（架构、数据库、API 契约、状态机、测试策略）+ Go/Python/前端三端骨架 + Docker Compose 配置 + 基础健康检查，使「目录、依赖、接口和实施计划明确，基础环境能够运行」。

## 2. 范围与非范围

**范围**：设计文档、目录骨架、健康检查端点、迁移框架接入（仅 baseline）、compose 与 .env.example、本地 PG 初始化、README。

**非范围**：任何业务逻辑代码（任务 CRUD、状态机实现、Worker、工具网关、Agent 循环、评测、诊断、前端页面）——这些属于阶段 B-E，每次开工前另行走 SDD 审批。

## 3. 总体架构（本阶段定稿的设计决策）

### 3.1 进程拓扑（模块化阶段）

```
用户/前端 ──HTTP──▶ Go Server (Gin, :8080)  ──SQL──▶ PostgreSQL (:5432)
                      │  ▲
                      │  └──HTTP──▶ Python FastAPI (:8000, /internal/v1)
                      │                （Agent 运行 / 评测 / 诊断）
Go Worker（与 Server 同二进制，--role=worker 或同进程内嵌，阶段 B 定）
```

- Go 是任务状态唯一权威；Python 不直连数据库（阶段 A 起就按此约束，评测/诊断数据也经 Go 提供的接口写入，Python 侧仅缓存计算中间态）。
- 约束修正点：需求 §10 表中评测/诊断表也建在同一个 PG 里，但写入路径收敛到 Go 内部 API（`/internal/v1` 反向：Python 调 Go，而非 Go 调 Python 评测表直写）。阶段 D 细化。

### 3.2 关键技术决策

| 决策点 | 选择 | 理由 |
|---|---|---|
| 仓库形态 | 单仓库：`platform/` `agent/` `console/` `deploy/` `docs/` | 先模块化后服务化（§4.3） |
| Go HTTP | Gin | 需求指定 |
| Go DB 访问 | pgx（pool）+ 手写 SQL | 需求指定；不引入 ORM，保留对锁与条件更新的精确控制 |
| 迁移 | golang-migrate + `platform/migrations/` | 版本化迁移（§10.6） |
| Python Web | FastAPI + Pydantic v2 + uvicorn | 需求指定 |
| Python 环境 | `python3 -m venv` + pip（本机 3.14.7） | 不额外装 uv；若依赖兼容出问题降级 3.12 |
| 前端 | Vite + React 18 + TypeScript | 需求指定 React+TS |
| 契约 | OpenAPI 3.1（`docs/openapi/`）双端引用 + 契约测试（阶段 C） | 防漂移（§11.3） |
| 事件排序 | PG `bigserial` 序列号为主序，时间戳仅展示 | §6.7 不依赖墙上时钟 |
| 任务领取 | `FOR UPDATE SKIP LOCKED` + `lease_version` 条件更新 | §6.4 并发保护（阶段 B 实现） |

### 3.3 数据库设计要点（11 表，详见 docs/database.md）

- `tasks.idempotency_key` 唯一索引；`status` + `lease_version` + `lease_expires_at` 参与乐观并发；状态变更一律 `UPDATE ... WHERE id=? AND lease_version=? AND status=?`。
- `task_events.seq bigserial` 全序；事件追加写，不更新。
- 大对象（模型原始输入输出、产物）存文件目录 `data/`，表内只存引用，可配置保留策略（§10.8）。

### 3.4 状态机（详见 docs/state-machine.md）

- 六状态：PENDING / RUNNING / CANCEL_REQUESTED / SUCCEEDED / FAILED / CANCELLED。
- 竞争规则：RUNNING→SUCCEEDED 与 CANCEL_REQUESTED 的竞争，以数据库条件更新原子裁决——先到先得，后到方转为「完成但带取消标记」事件，不回滚已完成状态；取消请求只允许在非终态提出。
- 重试：终态任务经显式 retry 接口生成新 attempt（同一 task_id 或新 task_id 关联 `retried_from`，阶段 B 定稿）。

### 3.5 API 契约要点（详见 docs/api-contract.md + openapi yaml）

- Go 对外 12 端点（§11.1），Python 内部 6 端点（§11.2）。
- 统一错误体：`{code, message, retryable, request_id, details}`；长任务（agent-run/评测/诊断）一律异步：POST 返回 `202 + id`，GET 轮询状态。
- 内部 API：超时、请求体 ≤1MB、响应体限制、Python 不可用时 Go 返回 `503 + 明确错误码`。

## 4. 骨架代码方案（本阶段产出）

| 端 | 最小内容 | 验证方式 |
|---|---|---|
| Go `platform/` | go.mod、cmd/server、internal/config、internal/httpapi（仅 `/api/v1/health` 含 PG 连通检查）、internal/store（仅 ping）、migrations/baseline | `go build ./...` + `go test ./...` 通过 |
| Python `agent/` | pyproject.toml、app/main.py（FastAPI，`/internal/v1/health`）、tests/test_health.py | `pytest` 通过 |
| 前端 `console/` | Vite 脚手架 + 空布局 + 健康页调 `/api/v1/health` | `npm run build` 成功 |
| deploy/ | docker-compose.yml（pg/go/python/console + healthchecks）、.env.example | 交付不运行（本机无 Docker），README 注明 |

## 5. 本地环境方案（方案 A：原生进程）

- PostgreSQL 17（brew）：建库 `agentops_dev`、`agentops_test`；连接串进 `.env`（不提交）。
- Go server：`go run ./cmd/server`；Python：`uvicorn app.main:app`；前端：`npm run dev`。
- compose 文件作为「有 Docker 环境」的等价启动方式交付。

## 6. 验收标准（阶段 A）

1. 五份文档齐全且互相一致（状态机与 DB 字段、API 与状态机转换对齐）。
2. 三端骨架各自的健康检查真实可跑：Go 检 PG 连通、Python 返回版本、前端调通 Go health。
3. `go test ./...`、`pytest`、`npm run build` 全绿；迁移命令 `make migrate-up` 对 dev 库可执行。
4. README 含环境准备、启动、迁移、测试命令，全部实测过。

## 7. 风险与对策

| 风险 | 对策 |
|---|---|
| Python 3.14 与 FastAPI/pydantic 兼容 | 骨架阶段即验证；不行则 brew 装 3.12 |
| 路径已改名为 AgentOps，但会话工作区仍指向旧目录 | 全部命令用绝对路径；旧空目录由用户手动删 |
| golang-migrate 依赖下载需网络 | go module proxy 直连；失败再切 GOPROXY 镜像 |
| 评测/诊断数据写入路径（Python 不直连库）与需求 §10 存在解释空间 | 在架构文档中明确「经 Go 内部写接口」，阶段 D 前与需求方再确认 |
