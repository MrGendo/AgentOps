# AgentOps 阶段 A 任务计划

- 日期：2026-10-26
- 规格：`docs/superpowers/specs/2026-10-26-agentops-phase-a-design.md`
- 状态：已完成（2026-10-26 交付并验证）

## 任务清单

### 环境准备
- [x] T1 启动本地 PostgreSQL（brew services start postgresql@17），创建 `agentops_dev` / `agentops_test` 库，验证连接
- [x] T2 验证 Go 1.27 工具链可用（go version / go build 冒烟）

### 设计文档（docs/）
- [x] T3 `docs/architecture.md` 架构设计：分层、进程拓扑、模块边界、语言间协作方式、服务化演进
- [x] T4 `docs/database.md` 数据库设计：11 表完整 DDL 级描述、索引、约束（幂等唯一、lease_version 条件更新、事件序列）、事务策略
- [x] T5 `docs/api-contract.md` + `docs/openapi/api-v1.yaml`、`internal-v1.yaml`：全部 18 端点、统一错误体、异步任务模式
- [x] T6 `docs/state-machine.md` 状态机：状态表、合法转换矩阵、取消/完成竞争规则、重试语义
- [x] T7 `docs/testing-strategy.md` 测试策略：单测/集成/故障注入分层，§16 验收项到测试的映射表

### 骨架代码
- [x] T8 `platform/` Go 骨架：go.mod、cmd/server、internal/config、internal/httpapi（/api/v1/health 含 PG ping）、internal/store、golang-migrate 接入 + baseline 迁移；`go build` + `go test` 通过
- [x] T9 `agent/` Python 骨架：pyproject.toml、venv、FastAPI app、/internal/v1/health、pytest 通过
- [x] T10 `console/` 前端骨架：Vite + React + TS，健康检查页调通 Go health，`npm run build` 成功
- [x] T11 `deploy/docker-compose.yml` + `.env.example`（pg/go/python/console + healthchecks）

### 收尾
- [x] T12 README：环境准备、启动、迁移、测试命令（全部实测）
- [x] T13 `docs/task-log/2026-10-26.md` 任务日志：目标/实现方式/修改明细（精确到行号）/验证结果
- [x] T14 三端健康检查端到端 smoke 实测 + git 初始提交

## 验收对照

- 需求 §17-A 验收「目录、依赖、接口和实施计划明确，基础环境能够运行」→ T8-T10 + T14
- 规格 §6 验收 4 条 → T8-T12 实测结果
