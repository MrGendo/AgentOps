# AgentOps 阶段 B 任务计划
- 日期：2026-10-26
- 规格：`docs/superpowers/specs/2026-10-26-agentops-phase-b-design.md`
- 状态：待审批

## 任务清单
### 数据与领域基础
- [x] T1 `000002_core_tables` 迁移（6 表 + 索引 + 约束）+ `make migrate-up/down` 在 dev/test 双库实测
- [x] T2 `internal/statemachine`：转换矩阵 + 竞争裁决纯函数 + L1 全矩阵测试
- [x] T3 `internal/retry`：错误分类枚举 + Retryable + Backoff（含抖动上界）+ L1 测试
### 任务服务与 API
- [x] T4 `internal/task`：创建（幂等键冲突返回已存在）、详情、列表（游标+过滤）、取消、重试 + events 同事务写入 + L1/L2 测试（R9 并发幂等）
- [x] T5 `internal/httpapi`：统一错误包络、request_id 中间件、8 条任务路由接线 + 处理器测试（含 409/404 分支）
### Worker 与租约
- [x] T6 `internal/worker`：领取循环（SKIP LOCKED）、心跳续租、取消检查点 + L2（R1 并发领取、R10 取消竞争）
- [x] T7 reaper：租约过期回收 + 任务超时判定 + L2（R2 崩溃恢复、R3 fencing、R4 双超时区分）
- [x] T8 mock 执行器四类（echo/fail/sleep/tools）+ steps/attempts 全记录 + 重试联动（R5/R6）+ L2
### 工具网关
- [x] T9 `internal/gateway`：注册表 + Schema 校验 + 权限 + 超时执行 + tool_calls 审计 + /tools、/tool-calls 路由 + L1
- [x] T10 四工具实现（calculator 求值器 / knowledge_search + 示例知识库 / task_status / mock_http）+ L1（含非法参数与 TIMEOUT_UNKNOWN）
- [x] T11 R7/R8 补齐：mock_http timeout 模式留痕不重试、事务失败注入测试
### 收尾
- [x] T12 L4 端到端冒烟：mock.tools 全链路（创建→领取→四工具→SUCCEEDED→trace 完整）
- [x] T13 README 更新（新命令、Mock 任务演示、故障复现方式）+ 任务日志 + git 提交

## 验收对照
- 需求 §17-B「无需真实模型运行 Mock 任务并验证核心可靠性」→ T8/T10/T12
- 规格 §13 验收四条 → T2-T11 测试证据 + T12 演示
