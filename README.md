# AgentOps

智能体**可靠执行、工具治理、自动评测与失败诊断**平台。Go 承担核心执行平台（任务状态机、Worker 调度、租约与恢复、工具网关、事件审计），Python 承担智能与质量模块（Agent Runtime、模型适配、评测、诊断），PostgreSQL 是唯一必需的外部服务。完整需求见 [requirements.md](requirements.md)。

## 系统架构

```
浏览器 ──HTTP──▶ Go Server (Gin :8080) ──SQL──▶ PostgreSQL (:5432)
                    │  ▲
       下发 agent-run│  │工具调用（经网关）
                    ▼  │
               Python FastAPI (:8000 /internal/v1)
               Agent 运行 · 评测 · 诊断 · 模型适配
```

- **Go 是任务状态的唯一权威**：领取、租约、fencing、幂等、事件全部以数据库为协调点。
- **Python 不直连数据库、不绕过工具网关**：跨语言协作走版本化 HTTP 契约。
- 设计文档：[架构](docs/architecture.md) · [数据库](docs/database.md) · [状态机](docs/state-machine.md) · [API 契约](docs/api-contract.md) · [测试策略](docs/testing-strategy.md)

## 目录结构

```
platform/   Go 核心执行平台（server / migrate / store / httpapi / migrations）
agent/      Python 智能与质量模块（FastAPI，阶段 C 起承载 Agent Runtime）
console/    React + TypeScript 管理控制台（Vite）
deploy/     docker-compose.yml
docs/       设计文档、OpenAPI 契约、superpowers 设计流程、任务日志
data/       大对象产物（模型 IO、轨迹、报告），表内只存引用（git 忽略）
```

## 技术选型

Go 1.27 · Gin · pgx · golang-migrate ｜ Python 3.12+ · FastAPI · Pydantic v2 · pytest ｜ React 19 + TypeScript + Vite ｜ PostgreSQL 17 · Docker Compose

## 环境准备

```bash
# PostgreSQL 17（brew）
brew install postgresql@17
brew services start postgresql@17
createdb agentops_dev && createdb agentops_test

cp .env.example .env        # 按需修改连接串

# Python 虚拟环境（需 Python ≥3.12；本仓库以 3.14 验证通过）
cd agent && python3 -m venv .venv && .venv/bin/pip install -e ".[dev]" && cd ..

# 前端依赖
cd console && npm install && cd ..
```

> 网络受限环境请设置 Go 模块镜像：`go env -w GOPROXY=https://goproxy.cn,direct`

## 启动方式

```bash
# 方式一：原生进程（日常开发）
make migrate-up                          # 数据库迁移
cd platform && go run ./cmd/server &     # Go 平台 :8080
cd agent && .venv/bin/uvicorn app.main:app --port 8000 &   # Python :8000
cd console && npm run dev                # 控制台 :5173（/api 代理到 8080）

# 方式二：Docker Compose（需 Docker，本仓库已在 colima + compose v5.6 验证）
cd deploy && docker compose up -d
```

## 数据库迁移

```bash
make migrate-up        # 升级到最新
make migrate-down      # 回滚 1 个版本
make migrate-status    # 查看当前版本
```

## 测试命令

```bash
make test              # Go 单元测试（状态机/重试/校验器/求值器/健康检查）
make test-integration  # Go 集成测试（真实 PG / agentops_test，串行执行包）
make test-py           # Python 测试（当前：健康检查契约）
make build-console     # 前端类型检查 + 构建
```

阶段 B 测试结果：单元 + 集成全绿，可靠性场景 R1-R10 全部有自动化测试（并发领取唯一、租约过期恢复、fencing、双超时区分、退避重试、不可重试即败、超时未知留痕、事务失败无部分写入、并发幂等唯一、取消/完成竞争裁决）。测试矩阵见 [docs/testing-strategy.md](docs/testing-strategy.md)。

## Mock 任务与故障注入（阶段 B 已可用）

启动平台后（`cd platform && go run ./cmd/server`），无需任何模型即可演示完整执行链：

```bash
# 全链路：创建后自动调度，依次调用 calculator/knowledge_search/task_status/mock_http
curl -X POST localhost:8080/api/v1/tasks -H 'Content-Type: application/json' -d '{
  "task_type":"mock.tools","input":{"goal":"诊断演示"},
  "allowed_tools":["calculator","knowledge_search","task_status","mock_http"]}'

# 查看轨迹与时间线
curl localhost:8080/api/v1/tasks/<id>/trace
curl localhost:8080/api/v1/tasks/<id>/events

# 故障注入：注入两次可重试失败（第 3 次成功）→ 观察退避重试
curl -X POST localhost:8080/api/v1/tasks -H 'Content-Type: application/json' -d '{
  "task_type":"mock.fail","input":{"goal":"f"},
  "params":{"fail_times":2,"error_class":"TRANSIENT_NETWORK"}}'

# 故障注入：外部超时结果未知（TIMEOUT_UNKNOWN，不自动重试）
curl -X POST localhost:8080/api/v1/tool-calls -H 'X-Service-Token: dev-service-token' \
  -H 'Content-Type: application/json' \
  -d '{"task_id":"<id>","tool_name":"mock_http","params":{"mode":"timeout"}}'

# 任务超时（timeout_seconds=1，sleep 5s → TASK_TIMEOUT）
curl -X POST localhost:8080/api/v1/tasks -H 'Content-Type: application/json' -d '{
  "task_type":"mock.sleep","input":{"goal":"t"},"params":{"seconds":5},"timeout_seconds":1}'
```

Worker 崩溃恢复的自动化复现：见 `internal/worker/worker_integration_test.go` 的 R2/R3 用例（租约过期→reaper 重排→新 Worker 接管）。

## 常见问题排查

| 现象 | 处理 |
|---|---|
| Go 依赖下载超时 | `go env -w GOPROXY=https://goproxy.cn,direct` |
| server 启动报数据库连接失败 | 确认 `brew services list` 中 postgresql@17 为 started，且 `agentops_dev` 已创建 |
| 前端健康页 degraded | Go 平台未启动或 PG 不可达；看 Go 日志中的依赖状态 |
| compose 端口冲突 | 修改 `deploy/docker-compose.yml` 端口映射或停用本机同名服务 |
| colima 内镜像拉取失败（DNS 报错或超时） | ① `colima ssh -- sudo sh -c 'echo "nameserver 223.5.5.5" > /etc/resolv.conf'`；② daemon 配 registry-mirrors（docker.m.daocloud.io）；③ 重启 VM 内 docker 后重试（本项目实测有效） |

## 已知限制（阶段 B）

1. 取消请求在步骤/工具调用边界生效：长睡眠类步骤内部不会中断，需等待步骤结束或租约过期后由 reaper 确认 CANCELLED（语义与 §13.6 一致：不撤销已发出的外部副作用）。
2. Python Agent Runtime（阶段 C）、评测/诊断（阶段 D）、控制台页面（阶段 E）尚未实现；当前任务类型为内置 Mock（echo/fail/sleep/tools）。
3. 无用户认证与 RBAC（需求 §3.1 允许 MVP 阶段从简，接口预留扩展）。
4. 内部接口仅靠服务令牌 + 网络边界保护，不暴露公网。
5. 性能测试脚本与指标看板属阶段 F。Docker Compose 全栈已在本地 colima 环境实测，受限网络需按排查表配置镜像源。

## 后续演进

先模块化、后服务化：Worker/网关/Agent/评测/诊断可按 [docs/architecture.md](docs/architecture.md) §9 的路径拆分独立部署；引入 MQ 或工作流引擎前须按需求 §18 补齐投递幂等、重放与死信设计。
