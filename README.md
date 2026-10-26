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
make test              # Go 单元测试（当前：config + httpapi 健康检查）
make test-py           # Python 测试（当前：健康检查契约）
make build-console     # 前端类型检查 + 构建
```

当前阶段（A）测试结果：Go `4 tests passed`；Python `1 test passed`；前端 `tsc + vite build` 通过。随阶段推进，测试矩阵见 [docs/testing-strategy.md](docs/testing-strategy.md)。

## Mock 模型与故障注入（阶段 B/C 起提供）

- Mock 模型：`AGENTOPS_MODEL=mock`（默认），无需付费 API 即可全链路验收。
- 内置工具 `mock_http` 支持成功/错误/超时/响应丢失四种故障模式，用于复现外部依赖异常。
- 故障注入脚本将固化在 `scripts/faults/`（kill_worker.sh、expire_lease.sql 等）。

## 常见问题排查

| 现象 | 处理 |
|---|---|
| Go 依赖下载超时 | `go env -w GOPROXY=https://goproxy.cn,direct` |
| server 启动报数据库连接失败 | 确认 `brew services list` 中 postgresql@17 为 started，且 `agentops_dev` 已创建 |
| 前端健康页 degraded | Go 平台未启动或 PG 不可达；看 Go 日志中的依赖状态 |
| compose 端口冲突 | 修改 `deploy/docker-compose.yml` 端口映射或停用本机同名服务 |
| colima 内镜像拉取失败（DNS 报错或超时） | ① `colima ssh -- sudo sh -c 'echo "nameserver 223.5.5.5" > /etc/resolv.conf'`；② daemon 配 registry-mirrors（docker.m.daocloud.io）；③ 重启 VM 内 docker 后重试（本项目实测有效） |

## 已知限制（阶段 A）

1. 业务功能（任务 CRUD、Worker、工具网关、Agent、评测、诊断、控制台页面）尚未实现，按阶段 B-F 推进。
2. 无用户认证与 RBAC（需求 §3.1 允许 MVP 阶段从简，接口预留扩展）。
3. 内部接口仅靠服务令牌 + 网络边界保护，不暴露公网。
4. 性能测试脚本与指标看板属阶段 F。Docker Compose 全栈（四服务）已在本地 colima 环境实测启动并通过探活，但受限网络需按排查表配置镜像源。

## 后续演进

先模块化、后服务化：Worker/网关/Agent/评测/诊断可按 [docs/architecture.md](docs/architecture.md) §9 的路径拆分独立部署；引入 MQ 或工作流引擎前须按需求 §18 补齐投递幂等、重放与死信设计。
