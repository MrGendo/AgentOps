# AgentOps

**黑盒 Agent 执行与审计平台** —— 让任意 Coding Agent 在受控环境里跑真实任务，用四层审计回答一个问题：*它改的代码，能信吗？*

[![Go](https://img.shields.io/badge/Go-1.27-00ADD8?logo=go&logoColor=white)](https://go.dev)
[![Python](https://img.shields.io/badge/Python-3.12+-3776AB?logo=python&logoColor=white)](https://python.org)
[![React](https://img.shields.io/badge/React-19-61DAFB?logo=react&logoColor=black)](https://react.dev)
[![PostgreSQL](https://img.shields.io/badge/PostgreSQL-17-4169E1?logo=postgresql&logoColor=white)](https://postgresql.org)
[![License](https://img.shields.io/badge/License-MIT-green)](#license)

## 它解决什么问题

Coding Agent（mini-SWE-agent、OpenHands、Cline、Aider……）迭代极快，每家的内置评估口径都不一样，横着比基本靠感觉。AgentOps 把它们拉到同一张考桌上：

- **统一考题**：带隐藏缺陷的代码仓库（fixture）+ 明确的验收测试
- **统一执行**：Agent 以子进程黑盒方式运行，平台只控制输入（隔离工作区）与输出（git diff）
- **统一审计**：不看 Agent 自报的"我成功了"，只看它留下的 diff —— 四层审计独立裁决
- **统一指标**：同一批任务跑多个引擎，成功率/审计通过率/Token 消耗横向对比

```
                         ┌──────────────────────────────────────┐
                         │        AgentOps 控制平面              │
   控制台(React) ──────▶  │  Go Server :8080                     │
                         │  任务状态机 · Worker 调度 · 租约恢复   │
                         │  工具网关 · 事件审计 · 评测 · 诊断      │
                         └───────┬───────────────────┬──────────┘
                                 │ HTTP 契约          │ SQL
                                 ▼                   ▼
                    Python FastAPI :8000        PostgreSQL :5432
                    Agent 注册表 · 四层审计
                    评测规则 · 故障诊断
                                 │
              ┌──────────────────┼──────────────────┐
              ▼                  ▼                  ▼
        ┌──────────┐      ┌───────────┐      ┌───────────┐
        │ mini-SWE │      │ OpenHands │      │ Cline     │  …各自以子进程运行，
        │ (SDK 模式)│      │ (子进程)   │      │ (CLI 子进程)│   互不感知，输出统一
        └──────────┘      └───────────┘      └───────────┘   汇聚为 git diff
```

**架构纪律**：Go 是任务状态的唯一权威（领取/租约/fencing/幂等全以数据库为协调点）；Python 不直连数据库、不绕过工具网关；黑盒 Agent 只通过"工作区进出"与平台交互，平台不介入 Agent 内部生命周期——这是与 OpenHands SDK 工作区管理冲突 17 次之后换来的设计结论。

## 四层验收审计

Agent 说"我修好了"不算数。平台对最终 diff 独立执行四层审计（[agent/app/audit/multi_level.py](agent/app/audit/multi_level.py)）：

| 层 | 名称 | 裁决内容 | 挡住的真实翻车 |
|---|---|---|---|
| L1 | 冒烟测试 | 验收测试（pytest 白名单）是否真的通过 | 改完更糟、测试根本没过 |
| L2 | 变更范围 | 是否只改了允许修改的文件 | 顺手"优化"了半个仓库 |
| L3 | 安全模式 | 是否引入硬编码密钥/无限循环/危险 import | 模型把 API Key 写进源码 |
| L4 | 变更质量 | 是否删除/绕过现有测试、是否堆砌无注释改动 | 为了过测试把测试删了 |

每层独立给出 PASS/FAIL 与证据明细，汇总为审计报告（Markdown），随任务归档。

## 支持的 Agent 引擎

| 引擎 | 接入方式 | 状态 |
|---|---|---|
| mini-SWE-agent | SDK 进程内（程序化 API） | ✅ 已实测：GLM-5.3 真实修 bug，3/3 任务通过 |
| OpenHands | 子进程隔离（官方 SDK 独立运行器） | ✅ 已实测：四层审计全过 |
| Cline | CLI 子进程（`cline --yolo`） | ✅ runner 就绪（v3.0.74） |
| Aider | CLI 子进程（`aider --message`） | ⚠️ runner 就绪，本机 Python 3.14 装 numpy 失败暂阻塞 |

引擎注册表遵循**三态分离**：已注册 ≠ 可运行 ≠ 已测试，运行统计由 Go 侧按 `agent_runs` 真实聚合注入，注册表从不编造数据。

新增一个黑盒引擎只需两步：写一个"给我工作区和问题、自己跑到退出"的脚本（`.sh` 或 `.py`），在 [registry.py](agent/app/adapters/registry.py) 注册 `SubprocessAgentAdapter`。

## 目录结构

```
platform/   Go 核心执行平台（server / worker / gateway / httpapi / migrations）
agent/      Python 智能与质量模块（Agent 注册表 / 四层审计 / 评测 / 诊断）
console/    React + TypeScript 管理控制台（Vite）
deploy/     docker-compose.yml（4 服务全栈）
docs/       设计文档、OpenAPI 契约、验收报告、任务日志
agent/evaldata/swe_tasks/   预置 SWE 考题（boundary_bug / logic_error / missing_validation）
```

设计文档：[架构](docs/architecture.md) · [数据库](docs/database.md) · [状态机](docs/state-machine.md) · [API 契约](docs/api-contract.md) · [测试策略](docs/testing-strategy.md) · [最终验收报告](docs/final-acceptance-report.md)

## 环境准备

```bash
# PostgreSQL 17
brew install postgresql@17
brew services start postgresql@17
createdb agentops_dev && createdb agentops_test

cp .env.example .env        # 按需修改连接串

# Python 虚拟环境（需 Python ≥3.12）
cd agent && python3 -m venv .venv && .venv/bin/pip install -e ".[dev]" && cd ..

# 前端依赖
cd console && npm install && cd ..
```

> 网络受限环境：`go env -w GOPROXY=https://goproxy.cn,direct`；Docker 镜像源配置见[常见问题](#常见问题排查)。

### 可选：接入真实 Agent 引擎（上游代码不入库）

平台自身零外部 Agent 依赖即可运行（Mock 模式）。要跑真实引擎，按需安装上游包（本仓库不含其代码）：

```bash
cd agent
.venv/bin/pip install mini-swe-agent          # mini-SWE 引擎（SDK 模式）
.venv/bin/pip install openhands-sdk           # OpenHands 引擎（子进程模式）
npm install -g cline                          # Cline CLI（Node 18+）
.venv/bin/pip install aider-chat              # Aider CLI（需 Python ≤3.13，numpy wheel 限制）
```

配置真实模型（密钥只从环境变量注入，严禁入库）：

```bash
export AGENTOPS_SWE_MODE=real
export AGENTOPS_SWE_MODEL=<litellm 模型名>       # 如 openai/gpt-4o-mini
export AGENTOPS_SWE_API_KEY=<你的密钥>
export AGENTOPS_SWE_API_BASE=<可选，自定义网关>
# AGENTOPS_SWE_EXEC=local 时需显式 AGENTOPS_SWE_ALLOW_LOCAL=1（真实模式默认要求 docker 隔离）
```

## 启动

```bash
# 方式一：原生进程（日常开发）
make migrate-up                                        # 数据库迁移
cd platform && go run ./cmd/server &                   # Go 平台 :8080
cd agent && .venv/bin/uvicorn app.main:app --port 8000 &  # Python :8000
cd console && npm run dev                              # 控制台 :5173（/api 代理到 8080）

# 方式二：Docker Compose（已在 colima + compose v5.6 验证）
cd deploy && docker compose up -d
```

## 跑第一个 Agent 审计任务

双进程启动后（Go :8080 + Python :8000，Mock 模式无需任何 API Key）：

```bash
# 提交 SWE 修复任务：agent 选引擎，fixture 选考题
curl -X POST localhost:8080/api/v1/tasks -H 'Content-Type: application/json' -d '{
  "task_type": "swe.fix",
  "input": {
    "agent": "miniswe",
    "fixture": "boundary_bug",
    "problem": "修复 utils.py 中的数组越界缺陷，保持函数签名不变"
  },
  "allowed_tools": []}'

# 查看结果：任务状态、轨迹、四层审计报告
curl localhost:8080/api/v1/tasks/<id>
curl localhost:8080/api/v1/tasks/<id>/trace
```

换引擎只改一个字段：`"agent": "openhands"` / `"cline"` / `"aider"`。同一批 fixture 换引擎各跑一轮，再到控制台"评测"页横向对比——这就是平台的完整用法闭环。

## 平台能力总览

**可靠执行内核**（阶段 B）：任务状态机（6 态）、Worker `FOR UPDATE SKIP LOCKED` 调度、租约 + fencing token、指数退避重试、Worker 崩溃自动接管（R2/R3 用例可复现）、TIMEOUT_UNKNOWN 留痕不盲重试。故障注入演示：

```bash
curl -X POST localhost:8080/api/v1/tasks -H 'Content-Type: application/json' -d '{
  "task_type":"mock.fail","input":{"goal":"f"},
  "params":{"fail_times":2,"error_class":"TRANSIENT_NETWORK"}}'   # 注入 2 次可重试失败 → 观察退避
```

**Agent Runtime**（阶段 C）：计划-执行循环、9 类执行限额（步数/耗时/成本/工具调用…）、模型适配层（Mock / OpenAI 兼容）、结构化轨迹。

**自动评测与失败诊断**（阶段 D）：数据集导入（JSONL）、批量评测（每项指标带 caliber 口径，无拍脑袋综合分）、版本对比、失败任务证据包诊断（10 类故障分类，证据不足明确标 LOW + 人工介入）：

```bash
curl -X POST localhost:8080/api/v1/datasets?name=demo-mixed \
  -H 'Content-Type: application/x-ndjson' --data-binary @agent/evaldata/demo-mixed.jsonl
curl -X POST localhost:8080/api/v1/evaluations -H 'Content-Type: application/json' \
  -d '{"dataset_id":"<id>","agent_version":"miniswe"}'
```

**管理控制台**（阶段 E）：任务列表/详情/时间线/审计报告、评测指标卡与版本对比、Agent 引擎注册表、健康页。

**一键演示与验收**（阶段 F）：`make demo`（全链路自起自清）、`make bench`（并发压测）、`make metrics`（观测指标）、`bash scripts/security-scan.sh`（5 项安全扫描）、故障复现脚本见 `scripts/faults/`。

## 测试

```bash
make test              # Go 单元测试
make test-integration  # Go 集成测试（真实 PG，自动拉起 uvicorn 跑 L4/L5 端到端）
make test-py           # Python 测试（74 用例：规则/口径/审计/诊断/契约）
make build-console     # 前端类型检查 + 构建
```

当前状态：Go 单元 + 集成全绿，Python 74 用例全绿（含四层审计矩阵），前端 11 用例全绿。完整矩阵见 [docs/testing-strategy.md](docs/testing-strategy.md)，实测数据见 [最终验收报告](docs/final-acceptance-report.md)。

## 常见问题排查

| 现象 | 处理 |
|---|---|
| Go 依赖下载超时 | `go env -w GOPROXY=https://goproxy.cn,direct` |
| server 启动报数据库连接失败 | 确认 postgresql@17 已启动且 `agentops_dev` 已创建 |
| 前端健康页 degraded | Go 平台未启动或 PG 不可达；看 Go 日志中的依赖状态 |
| compose 端口冲突 | 修改 `deploy/docker-compose.yml` 端口映射 |
| colima 拉镜像失败（DNS/超时） | ① `colima ssh -- sudo sh -c 'echo "nameserver 223.5.5.5" > /etc/resolv.conf'`；② daemon 配 registry-mirrors（docker.m.daocloud.io）；③ 重启 VM 内 docker |
| Aider 安装失败 | Python 3.14 缺 numpy wheel，需降级到 3.12/3.13 建独立 venv |
| 真实模式启动报 CONFIG_ERROR | 按报错补齐 `AGENTOPS_SWE_MODEL/API_KEY`，或设 `AGENTOPS_SWE_ALLOW_LOCAL=1` 显式接受 local 隔离降级 |

## 已知限制

1. 取消请求在步骤/工具调用边界生效，长睡眠类步骤需等租约过期由 reaper 确认（不撤销已发出的外部副作用）。
2. Agent/评测运行态存于 Python 进程内存，重启后 Go 按可重试错误重新下发（任务不悬挂，评测可整体重跑且样例不重复）。
3. 无用户认证与 RBAC（MVP 从简，接口预留扩展）；内部接口仅靠服务令牌 + 网络边界，不暴露公网。
4. Cline/Aider 引擎 runner 已就绪但未在真实模型下完成全量审计实测（mini-SWE 与 OpenHands 已实测）。
5. SWE 考题目前为手工构造的 3 个 fixture；自动生成考题（含缺陷注入与验收测试）在演进路线中。

## 后续演进

- **考题自动生成**：用 LLM 为任意仓库批量生成带缺陷的 fixture 与验收测试，替代手工构造
- 引擎矩阵扩展：SWE-agent、AutoGen 等按子进程模式接入
- 先模块化后服务化：Worker/网关/评测/诊断按 [架构文档 §9](docs/architecture.md) 路径拆分独立部署；引入 MQ 前须补齐投递幂等、重放与死信设计

## License

MIT
