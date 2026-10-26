# AgentOps 阶段 C 设计规格（Python Agent Runtime）
- 日期：2026-10-26
- 依据：`requirements.md` §7、§11.2、§17-C；契约 `docs/openapi/internal-v1.yaml`
- 状态：已批准并执行完成

## 1. 目标
交付 Python Agent Runtime：模型适配层（Mock 默认 + 可配置真实适配器）、结构化计划生成与校验、受限执行循环、经 Go 网关的工具调用、运行轨迹，以及 Go 侧 AgentExecutor 打通调度；跨语言契约测试防漂移。验收（§17-C）：**一个包含多步骤和多次工具调用的端到端 Agent 任务**在无真实模型条件下完整跑通。

## 2. 范围与非范围
**范围**：`agent/`（models / runtime / gateway client / API）、`platform` 的 AgentExecutor 与 agent_runs 落库、契约测试、双进程 L4。
**非范围**：评测与诊断（阶段 D）、前端（E）、真实模型的实际联调（适配器代码就位、密钥留环境变量，默认关闭）。

## 3. Python 模块结构（agent/app/）
```
config.py      环境变量（模型选择/密钥/Go 地址/令牌/限制参数），密钥只从环境读取
main.py        /internal/v1：agent-runs POST(202)+GET、health；服务令牌中间件
models/        base.py 接口（generate_text / generate_structured / 超时 / 错误分类 / usage）
               mock.py  脚本化 MockLLM（按场景模板产出计划→工具调用→终答）
               openai_compat.py  OpenAI 兼容适配器（httpx，默认不启用）
runtime/
  planner.py   计划生成与 pydantic 校验（步骤数上限、字段完备、类型合法）
  loop.py      执行循环：限制 enforcement、重复调用检测、轨迹、终止原因
  trajectory.py 逐步耗时/模型调用数/工具调用引用/token 汇总/脱敏
gwclient.py    Go 网关客户端：httpx + X-Service-Token + 超时 + 错误映射
runs.py        运行状态仓（进程内存 + 锁；终态可查询，Go 拉取后仍保留至 TTL）
```

## 4. 模型适配层
- 接口：`generate_structured(prompt_schema) -> dict`、`generate_text(prompt) -> str`；每次调用返回 `usage{prompt,completion,total}` 与 `request_id`。
- 错误分类（映射 Go retry 类别）：`RATE_LIMITED`（退避重试）、`TIMEOUT`、`INVALID_OUTPUT`（结构化解析失败）、`PROVIDER_ERROR`。
- **MockLLM（脚本化，验收主力）**：按 goal 关键词选场景脚本；ops 诊断脚本固定产出 4 步计划（知识检索→指标计算→下游探测→汇总报告），逐轮给出工具调用参数，最终输出含证据引用的结构化报告。无网络、无密钥、确定性输出。
- openai_compat：环境变量 `AGENTOPS_MODEL_API_KEY/BASE_URL/MODEL_NAME` 驱动；未配置即不注册。密钥不进日志与轨迹。

## 5. 计划与执行循环（§7.2 限制全表落地）
| 限制 | 默认 | enforcement 点 |
|---|---|---|
| 最大迭代次数 | 10 | 每轮循环顶部 |
| 最大工具调用数 | 20 | CallTool 前 |
| 最大执行时长 | 300s | 循环级 deadline |
| 最大计划步骤数 | 8 | planner 校验即拒 |
| 模型请求超时 | 60s | 适配器内 |
| 单次上下文上限 | 32KB | 组装 prompt 前 |
| 重复工具调用 | 同 (tool, params) 二次 | 检测并跳过+计数 |
| 结构化输出校验 | pydantic | 每次模型返回 |
| 终止策略 | 达限即停，返回明确 finish_reason | MAX_ITERATIONS / MAX_TOOL_CALLS / TIMEOUT / MODEL_ERROR / INVALID_PLAN / COMPLETED / CANCELLED |

取消传播：Go 侧任务取消（CANCEL_REQUESTED）在轮询中发现后调用 Python 侧取消（运行仓标记，循环在下一轮边界停止）。

## 6. 与 Go 的协作
- 下发：Go AgentExecutor `POST /internal/v1/agent-runs`（task_id/trace_id/goal/allowed_tools/run_limits），Python 立即 202 返回 run_id。
- 工具：循环内经 `POST /api/v1/tool-calls`（服务令牌）调用网关，tool_calls 审计在 Go（含 step 关联由 Python 传 step 标识）。
- 结果：Go 轮询 `GET /internal/v1/agent-runs/{id}` 至终态；将 plan/finish_reason/token_usage/迭代数落 **agent_runs 表**（架构 D-1：Python 不直连库）。
- Python 不可用：AgentExecutor 按 `AGENT_SERVICE_UNAVAILABLE`（可重试）走 Go 统一退避；Python 侧模型失败映射为不可重试的 MODEL_ERROR / 可重试的 RATE_LIMITED。

## 7. 契约测试（L3）
- Python：pytest 加载 `docs/openapi/internal-v1.yaml`，断言端点/字段/示例与实现响应结构一致（轻量结构断言，不引全量 schema 引擎）。
- Go：AgentExecutor 的请求体/响应解析字段以同一 yaml 的示例为黄金样例。
- 双端样例漂移即测试失败。

## 8. 测试计划
- pytest 单测：planner 校验、限制 enforcement（逐项）、重复调用检测、MockLLM 确定性、gwclient 错误映射（httpx MockTransport）、agent-runs API 异步语义。
- Go 集成：AgentExecutor 对桩 Python（httptest）——服务不可用重试、终态映射、agent_runs 落库。
- L4 双进程：`agent.ops_diagnosis` 任务 → Python Mock 规划 → 网关 4 工具调用 → SUCCEEDED，agent_runs + trace 完整（验收主场景）。

## 9. 风险与对策
| 风险 | 对策 |
|---|---|
| 内存运行仓在 Python 重启后丢失 | Go 轮询得 404 → 按可重试错误处置；任务不悬挂 |
| Mock 脚本过拟合导致循环逻辑未被检验 | 限制类单测不依赖脚本，直接注入越界行为 |
| 双进程 L4 的时序不稳定 | 断言终态与轨迹内容，不断言耗时；轮询窗口 30s |

## 10. 验收标准（对照 §16.4）
1. 结构化计划可生成且校验拒非法计划；2. 经网关多次工具调用且结果驱动后续推理；3. 达到各项上限可停止并给 finish_reason；4. 模型异常与输出格式错误可识别分类；5. 轨迹关联任务与工具调用记录；6. Mock 模式全流程可跑；7. 契约测试双端通过；8. L4 端到端验收场景通过。
