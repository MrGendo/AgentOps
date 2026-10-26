# AgentOps 阶段 C 任务计划
- 日期：2026-10-26
- 规格：`docs/superpowers/specs/2026-10-26-agentops-phase-c-design.md`
- 状态：待审批

## 任务清单
### Python Runtime
- [x] T1 `app/config.py`（密钥只读环境）+ 服务令牌中间件 + 异常体系；httpx 弃用告警处理
- [x] T2 模型适配层：BaseLLM 接口 + 脚本化 MockLLM（ops 诊断场景）+ openai_compat 适配器 + 单测
- [x] T3 `runtime/planner.py`：计划 pydantic 校验（步骤上限/字段/类型）+ 单测
- [x] T4 `gwclient.py`：网关客户端（令牌/超时/TIMEOUT_UNKNOWN 映射）+ MockTransport 单测
- [x] T5 `runtime/loop.py` + `trajectory.py`：限制全表 enforcement、重复调用检测、轨迹与终止原因 + 单测
- [x] T6 `/internal/v1/agent-runs` POST(202)/GET + 运行仓 + 取消标记 + API 单测
### Go 侧
- [x] T7 `internal/worker/agent_executor.go`：下发/轮询/错误分类/agent_runs 落库 + config（AGENTOPS_AGENT_URL）+ 桩 Python 集成测试
### 集成与收尾
- [x] T8 契约测试：Python 读 yaml 结构断言 + Go 黄金样例一致性测试
- [x] T9 L4 双进程端到端：agent.ops_diagnosis 多步骤多工具真实链路 + 断言 agent_runs/trace
- [x] T10 README（Agent 任务演示、Mock 模型说明、双进程启动）+ 任务日志 + git 提交

## 验收对照
- 需求 §17-C「多步骤+多次工具调用的端到端 Agent 任务」→ T9
- 规格 §10 验收 8 条 → T2-T9 测试证据
