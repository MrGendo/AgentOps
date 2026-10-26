# AgentOps 任务状态机设计
- 版本：1.0（阶段 A 定稿）
- 依据：`requirements.md` §6.2、§6.4、§6.5、§13.6
- 实现约束：所有转换由 Go 统一模块裁决，SQL 条件更新保证原子性（见 [database.md](database.md) §3）。

## 1. 状态定义
| 状态 | 含义 | 是否终态 |
|---|---|---|
| `PENDING` | 等待调度 | 否 |
| `RUNNING` | 正在执行（持有有效租约） | 否 |
| `CANCEL_REQUESTED` | 用户已请求取消，等待执行侧停止 | 否 |
| `SUCCEEDED` | 成功完成 | 是 |
| `FAILED` | 执行失败（含重试耗尽） | 是 |
| `CANCELLED` | 已确认取消 | 是 |

## 2. 合法转换矩阵
| 从 \ 到 | PENDING | RUNNING | CANCEL_REQUESTED | SUCCEEDED | FAILED | CANCELLED |
|---|---|---|---|---|---|---|
| PENDING | — | 领取（claim） | 用户取消（直接取消未开始任务） | ✗ | ✗ | ✗ |
| RUNNING | 重试退避调度（retry.schedule） | 续租（lease_version+1） | 用户取消请求 | 执行成功 | 执行失败/超时 | 执行侧确认停止 |
| CANCEL_REQUESTED | ✗ | ✗ | — | 完成竞争胜出（带标记） | 失败竞争（带标记） | 执行侧确认停止 |
| SUCCEEDED | ✗ | ✗（重试须走显式 retry 流程生成新 attempt） | ✗ | ✗ | ✗ | ✗ |
| FAILED | 手动重试 → PENDING | ✗ | ✗ | ✗ | ✗ | ✗ |
| CANCELLED | 手动重试 → PENDING | ✗ | ✗ | ✗ | ✗ | ✗ |

规则要点：
1. **终态不可被普通更新覆盖**：终态行上的任何条件 UPDATE 因 `WHERE status IN (非终态)` 而 0 行命中，天然拒绝。
2. **重新执行必须走显式重试**：`POST /api/v1/tasks/{id}/retry` 将终态任务重置为 `PENDING`（attempt_count 不清零，scheduled_at=now()），并写 `task.retry_requested` 事件（含操作者）；只有 FAILED/CANCELLED 可重试。
3. **未开始任务的取消**：PENDING → CANCELLED 允许直接取消（无副作用）；已运行任务的取消必须经过 CANCEL_REQUESTED。
4. `RUNNING → PENDING` 仅用于自动重试退避调度：失败且 attempt_count < max_attempts 且错误类别可重试时，写回 PENDING 并设置 scheduled_at = now() + backoff(attempt, jitter)。

## 3. 竞争与并发裁决规则
### 3.1 取消 vs 完成（RUNNING 上的竞争）
用户请求取消与 Worker 报告完成可能并发。裁决：**数据库条件更新先到先得，后到方不回滚已落库状态，只追加事件标记。**
```
完成路径: UPDATE tasks SET status='SUCCEEDED', finish_reason=r
          WHERE id=? AND status='RUNNING' AND lease_version=$v
  影响 1 行 → 成功落终态；随后发现存在 task.cancel_requested 事件
              → finish_reason='SUCCEEDED_AFTER_CANCEL_REQUEST'（§6.2「明确完成竞争规则」）
  影响 0 行 → 状态已不是 RUNNING：
      若 status='CANCEL_REQUESTED' → 竞争取消侧：
          UPDATE ... SET status='SUCCEEDED', finish_reason='CANCEL_RACE_LOST'
          WHERE id=? AND status='CANCEL_REQUESTED'  -- 已完成事实优先，取消侧改为记录
      其他（已被恢复/重试/终态）→ 放弃本次结果，写 task.result_discarded 事件
取消路径: UPDATE tasks SET status='CANCEL_REQUESTED' WHERE id=? AND status='RUNNING'
  影响 0 行且 status∈终态 → 返回「任务已结束」的明确响应（不是错误）
```
### 3.2 执行侧停止确认
CANCEL_REQUESTED → CANCELLED 只能由**持有有效租约的 Worker** 在执行侧真正停止后写入（检查点：工具调用间隙、模型请求返回）。租约过期被接管时，恢复流程发现 CANCEL_REQUESTED 直接落 CANCELLED。
### 3.3 旧 Worker 写入保护（fencing）
- Worker 领取时获得 `lease_version=v`；一切状态更新（含步骤/工具结果关联的任务级写入）都携带 `AND lease_version=v`。
- 租约过期后任务被新 Worker 接管（v+1），旧 Worker 的更新 0 行命中 → 返回 `LEASE_STOLEN`，旧 Worker 立即放弃并停止副作用（无法撤销的已发出请求除外，见 §5）。

## 4. 租约生命周期
| 动作 | SQL 语义 | 说明 |
|---|---|---|
| 领取 | 见 database.md §3.3，`lease_version+1` | claim 与 lease 原子同时发生 |
| 心跳续租 | `UPDATE ... SET lease_expires_at=now()+ttl WHERE id=? AND lease_owner=? AND lease_version=v AND status='RUNNING'` | 续租失败即自觉放弃 |
| 过期检测 | 恢复器扫描 `status='RUNNING' AND lease_expires_at<now()` | 判定为 Worker 崩溃，按 §6.5「Worker 崩溃：租约过期后恢复」处理：attempt_count<max → 重新 PENDING（scheduled_at=now()）；否则 FAILED(lease_expired) |
| 任务超时 | `now()-started_at(本次尝试) > timeout_seconds` | 与租约超时区分：任务超时是业务时长上限，租约超时是活性信号（§16.2） |

## 5. 错误类别 → 状态影响（§6.5 落地）
| 错误类别 | 可重试 | 对任务状态的影响 |
|---|---|---|
| 参数错误 ARGUMENT_INVALID | 否 | 直接 FAILED |
| 权限拒绝 PERMISSION_DENIED | 否 | 直接 FAILED + POLICY_VIOLATION 事件 |
| 业务规则拒绝 BUSINESS_REJECTED | 否（默认） | 直接 FAILED |
| 模型限流 MODEL_RATE_LIMITED | 是（退避+上限） | 退避重排 PENDING |
| 临时网络错误 TRANSIENT_NETWORK | 是（退避+上限） | 退避重排 PENDING |
| 可确认未执行的工具失败 TOOL_FAILED_CLEAN | 按工具策略 | 工具级重试或任务级退避 |
| Worker 崩溃（租约过期） | 是（不占 attempt 退避窗口，受 max_attempts 约束） | 恢复器重排 PENDING |
| 结果未知 TIMEOUT_UNKNOWN | **否（自动）** | 停止自动重试，任务 FAILED(TIMEOUT_UNKNOWN)，转人工/下游确认流程（§13.6） |
| 取消 CANCELLED | — | 停止调度 |

退避公式：`wait = min(base * 2^attempt + rand(0, base), max_wait)`，默认 base=2s、max_wait=60s、max_attempts=3（`.env` 可调）。

## 6. 步骤状态机（简化版）
PENDING → RUNNING → SUCCEEDED / FAILED / CANCELLED；FAILED 且任务级允许重试 → 新 attempt（保留历史于 step_attempts），步骤状态回 RUNNING。步骤级转换在任务租约保护下发生，不单独建状态机表。

## 7. 状态机测试要求（对应 testing-strategy.md）
每条矩阵单元格（含非法转换）至少一个用例；§3 竞争场景以并发事务/ goroutine 实测，断言「无中间态丢失、无双重终态、事件序列可重建」。
