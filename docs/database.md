# AgentOps 数据库设计
- 版本：1.0（阶段 A 定稿，阶段 B 落地为 golang-migrate 迁移）
- 依据：`requirements.md` §6、§7、§8、§9、§10
- 说明：本文件即迁移脚本的唯一蓝本；字段变更必须先改本文件再生成迁移。

## 1. 总则
1. 所有表主键用 `uuid`（`gen_random_uuid()`，PG13+ 内置）。
2. 时间统一 `timestamptz`，由应用层写入 UTC；`task_events.created_at` 仅作展示，排序依据 `seq`。
3. 状态类字段用 `text + CHECK` 约束（可读、易演进），不使用 PG enum。
4. 大对象（模型原始 IO、轨迹、报告全文）只存 `*_ref` 相对引用（`data/` 目录），表内保留摘要与统计字段。
5. 核心表（tasks / task_steps / step_attempts / tool_calls / task_events）只允许 Go 写入。

## 2. 表定义

### 2.1 tasks —— 任务主表
```sql
CREATE TABLE tasks (
    id               uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    task_type        text NOT NULL,                          -- 任务类型（如 ops_diagnosis）
    input            jsonb NOT NULL,                         -- 用户输入
    params           jsonb NOT NULL DEFAULT '{}',            -- 执行参数
    allowed_tools    jsonb NOT NULL DEFAULT '[]',            -- 任务授权的工具名列表（§13.1）
    status           text NOT NULL DEFAULT 'PENDING'
                     CHECK (status IN ('PENDING','RUNNING','CANCEL_REQUESTED',
                                       'SUCCEEDED','FAILED','CANCELLED')),
    idempotency_key  text UNIQUE,                            -- 幂等键（§10.2 唯一约束保护）
    trace_id         uuid NOT NULL DEFAULT gen_random_uuid(),
    attempt_count    int  NOT NULL DEFAULT 0,                -- 当前尝试序号（从 1 起）
    max_attempts     int  NOT NULL DEFAULT 3,
    timeout_seconds  int  NOT NULL DEFAULT 600,              -- 单次尝试最大执行时长
    lease_version    bigint NOT NULL DEFAULT 0,              -- fencing token（§6.4）
    lease_owner      text,                                   -- 持有租约的 worker id
    lease_expires_at timestamptz,
    scheduled_at     timestamptz NOT NULL DEFAULT now(),     -- 下次可领取时间（退避重试）
    error_code       text,
    error_message    text,
    finish_reason    text,        -- SUCCEEDED_CANCELLED_RACE 等竞争裁决标记
    result_ref       text,                                   -- 最终结果文件引用
    created_at       timestamptz NOT NULL DEFAULT now(),
    started_at       timestamptz,
    finished_at      timestamptz
);
CREATE INDEX idx_tasks_status_sched ON tasks (status, scheduled_at)
    WHERE status = 'PENDING';                                -- Worker 领取扫描路径
CREATE INDEX idx_tasks_created ON tasks (created_at DESC);   -- 列表默认排序
CREATE INDEX idx_tasks_trace ON tasks (trace_id);
```

### 2.2 task_steps —— 执行步骤
```sql
CREATE TABLE task_steps (
    id             uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    task_id        uuid NOT NULL REFERENCES tasks(id) ON DELETE CASCADE,
    seq            int  NOT NULL,                            -- 步骤序号
    name           text NOT NULL,
    step_type      text NOT NULL,                            -- plan/tool/report ...
    status         text NOT NULL DEFAULT 'PENDING'
                   CHECK (status IN ('PENDING','RUNNING','SUCCEEDED','FAILED','CANCELLED')),
    input_digest   jsonb,                                    -- 输入摘要
    output_ref     text,                                     -- 输出引用
    attempt_count  int  NOT NULL DEFAULT 0,
    error_code     text,
    error_message  text,
    started_at     timestamptz,
    finished_at    timestamptz,
    UNIQUE (task_id, seq)
);
CREATE INDEX idx_steps_task ON task_steps (task_id, seq);
```

### 2.3 step_attempts —— 步骤尝试历史（重试不覆盖证据，§6.3）
```sql
CREATE TABLE step_attempts (
    id             uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    step_id        uuid NOT NULL REFERENCES task_steps(id) ON DELETE CASCADE,
    task_id        uuid NOT NULL REFERENCES tasks(id) ON DELETE CASCADE,  -- 冗余，便于按任务查询
    attempt_no     int  NOT NULL,
    status         text NOT NULL CHECK (status IN ('RUNNING','SUCCEEDED','FAILED','CANCELLED')),
    input_digest   jsonb,
    output_ref     text,
    error_class    text,        -- §6.5 错误类别
    error_code     text,
    error_message  text,
    started_at     timestamptz NOT NULL,
    finished_at    timestamptz,
    duration_ms    int,
    UNIQUE (step_id, attempt_no)
);
CREATE INDEX idx_attempts_task ON step_attempts (task_id);
```

### 2.4 tool_calls —— 工具调用记录
```sql
CREATE TABLE tool_calls (
    id              uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    task_id         uuid NOT NULL REFERENCES tasks(id) ON DELETE CASCADE,
    step_id         uuid REFERENCES task_steps(id) ON DELETE SET NULL,
    agent_run_id    uuid,                                     -- 关联 agent_runs（阶段 B/C 建 FK）
    tool_name       text NOT NULL,
    tool_version    text NOT NULL,
    params          jsonb NOT NULL,
    status          text NOT NULL
                    CHECK (status IN ('SUCCEEDED','FAILED','TIMEOUT_UNKNOWN','REJECTED')),
                    -- TIMEOUT_UNKNOWN：外部超时后结果未知（§6.5），禁止自动重试
    result          jsonb,                                    -- 小结果内联
    result_ref      text,                                     -- 大结果引用
    error_code      text,
    error_message   text,
    idempotency_key text UNIQUE,                              -- 工具级幂等（§6.6）
    risk_level      text NOT NULL DEFAULT 'LOW',
    duration_ms     int,
    created_at      timestamptz NOT NULL DEFAULT now(),
    started_at      timestamptz,
    finished_at     timestamptz
);
CREATE INDEX idx_tool_calls_task ON tool_calls (task_id);
CREATE INDEX idx_tool_calls_run  ON tool_calls (agent_run_id);
```

### 2.5 task_events —— 事件与审计（追加写，永不更新）
```sql
CREATE TABLE task_events (
    seq          bigserial PRIMARY KEY,                       -- 全局全序（§6.7）
    event_id     uuid NOT NULL UNIQUE DEFAULT gen_random_uuid(),
    task_id      uuid NOT NULL,
    trace_id     uuid NOT NULL,
    step_id      uuid,
    attempt_id   uuid,
    tool_call_id uuid,
    event_type   text NOT NULL,   -- task.created/task.claimed/task.completed/step.started/
                                  -- tool.call.started/retry.scheduled/lease.revoked/...
    payload      jsonb NOT NULL DEFAULT '{}',
    created_at   timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX idx_events_task ON task_events (task_id, seq);
CREATE INDEX idx_events_trace ON task_events (trace_id);
CREATE INDEX idx_events_type ON task_events (event_type, created_at);
```
> task_id 不建 FK：事件表是全链路审计底座，用宽列 + 索引换写入吞吐与历史完整性。

### 2.6 agent_runs —— Agent 运行（Python 计算、Go 落库）
```sql
CREATE TABLE agent_runs (
    id                  uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    task_id             uuid NOT NULL REFERENCES tasks(id) ON DELETE CASCADE,
    agent_version       text NOT NULL,
    model_config        jsonb NOT NULL DEFAULT '{}',          -- 模型名/参数（不含密钥）
    status              text NOT NULL DEFAULT 'PENDING'
                        CHECK (status IN ('PENDING','RUNNING','SUCCEEDED','FAILED','CANCELLED')),
    goal_digest         text,                                 -- 输入目标摘要
    plan                jsonb,                                -- 生成的计划
    plan_valid          boolean,
    plan_errors         jsonb,
    model_call_count    int NOT NULL DEFAULT 0,
    tool_call_count     int NOT NULL DEFAULT 0,
    iteration_count     int NOT NULL DEFAULT 0,
    token_usage         jsonb,        -- {prompt_tokens, completion_tokens, total_tokens}
    trajectory_ref      text,                                 -- 完整轨迹文件引用
    final_output_ref    text,
    finish_reason       text,        -- COMPLETED/MAX_ITERATIONS/MAX_TOOL_CALLS/TIMEOUT/...
    error_code          text,
    error_message       text,
    created_at          timestamptz NOT NULL DEFAULT now(),
    started_at          timestamptz,
    finished_at         timestamptz
);
CREATE INDEX idx_agent_runs_task ON agent_runs (task_id);
```

### 2.7 evaluation_datasets / 2.8 evaluation_cases
```sql
CREATE TABLE evaluation_datasets (
    id          uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    name        text NOT NULL,
    version     int  NOT NULL DEFAULT 1,
    description text,
    case_count  int  NOT NULL DEFAULT 0,
    created_at  timestamptz NOT NULL DEFAULT now(),
    UNIQUE (name, version)
);

CREATE TABLE evaluation_cases (
    id             uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    dataset_id     uuid NOT NULL REFERENCES evaluation_datasets(id) ON DELETE CASCADE,
    seq            int  NOT NULL,
    input_task     jsonb NOT NULL,               -- 输入任务
    expected       jsonb NOT NULL DEFAULT '{}',  -- 预期结果/关键约束
    allowed_tools  jsonb NOT NULL DEFAULT '[]',
    run_limits     jsonb NOT NULL DEFAULT '{}',
    scoring_rules  jsonb NOT NULL DEFAULT '{}',  -- 确定性规则优先（§8.4）
    tags           text[] NOT NULL DEFAULT '{}',
    difficulty     text NOT NULL DEFAULT 'normal',
    UNIQUE (dataset_id, seq)
);
```

### 2.9 evaluation_runs / 2.10 evaluation_results
```sql
CREATE TABLE evaluation_runs (
    id               uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    dataset_id       uuid NOT NULL REFERENCES evaluation_datasets(id),
    dataset_version  int  NOT NULL,
    agent_version    text NOT NULL,
    run_config       jsonb NOT NULL DEFAULT '{}',
    status           text NOT NULL DEFAULT 'PENDING'
                     CHECK (status IN ('PENDING','RUNNING','SUCCEEDED','FAILED','CANCELLED')),
    metrics          jsonb,      -- §8.3 全部分项指标（口径写明 per-call / per-task）
    started_at       timestamptz,
    finished_at      timestamptz,
    created_at       timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX idx_eval_runs_dataset ON evaluation_runs (dataset_id, agent_version);

CREATE TABLE evaluation_results (
    id            uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    eval_run_id   uuid NOT NULL REFERENCES evaluation_runs(id) ON DELETE CASCADE,
    case_id       uuid NOT NULL REFERENCES evaluation_cases(id),
    task_id       uuid REFERENCES tasks(id),       -- 对应真实任务
    agent_run_id  uuid,
    status        text NOT NULL CHECK (status IN ('SUCCEEDED','FAILED','ERROR')),
    scores        jsonb NOT NULL DEFAULT '{}',     -- 分项得分（规则名→结果）
    evidence      jsonb NOT NULL DEFAULT '{}',     -- 判分证据引用
    token_usage   jsonb,
    duration_ms   int,
    error_code    text,
    created_at    timestamptz NOT NULL DEFAULT now(),
    UNIQUE (eval_run_id, case_id)
);
```

### 2.11 diagnosis_reports —— 诊断报告
```sql
CREATE TABLE diagnosis_reports (
    id                   uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    task_id              uuid NOT NULL REFERENCES tasks(id) ON DELETE CASCADE,
    agent_run_id         uuid,
    fault_code           text NOT NULL CHECK (fault_code IN
                         ('MODEL_ERROR','PLANNING_ERROR','TOOL_SELECTION_ERROR',
                          'TOOL_ARGUMENT_ERROR','TOOL_EXECUTION_ERROR','TIMEOUT_OR_UNKNOWN',
                          'CONTEXT_ERROR','POLICY_VIOLATION','ORCHESTRATION_ERROR',
                          'EVALUATION_ERROR')),
    confidence           text NOT NULL CHECK (confidence IN ('HIGH','MEDIUM','LOW')),
    root_cause_hypothesis text NOT NULL,
    evidence             jsonb NOT NULL DEFAULT '[]',  -- [{type,ref,seq,...}] 引用真实事件/步骤/调用
    suggestions          jsonb NOT NULL DEFAULT '[]',
    need_human           boolean NOT NULL DEFAULT false,
    diagnoser_version    text NOT NULL,
    config               jsonb NOT NULL DEFAULT '{}',
    created_at           timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX idx_diagnosis_task ON diagnosis_reports (task_id);
```
> 证据不足时 `confidence='LOW'` 且 hypothesis 明确标注不确定，禁止编造根因（§9.3）。

## 3. 事务与一致性策略
1. **任务创建（幂等）**：单事务 `INSERT ... ON CONFLICT (idempotency_key) DO NOTHING RETURNING id`；0 行则 `SELECT` 已存在任务返回，保证重复提交不重复建任务。
2. **状态变更**：一律 `UPDATE tasks SET status=?, lease_version=lease_version+1, ... WHERE id=? AND status=? [AND lease_version=?]`，同事务追加 task_events；影响行数=0 即并发冲突，按 [state-machine.md](state-machine.md) 的裁决规则处理。**终态更新同样走条件，天然不可被普通更新覆盖。**
3. **原子领取**：
```sql
BEGIN;
SELECT id FROM tasks
 WHERE status='PENDING' AND scheduled_at<=now()
 ORDER BY created_at
 LIMIT 1
 FOR UPDATE SKIP LOCKED;                -- 并发下互不阻塞、不重复领取
UPDATE tasks SET status='RUNNING', attempt_count=attempt_count+1,
       lease_version=lease_version+1, lease_owner=$worker,
       lease_expires_at=now()+'30s'::interval, started_at=coalesce(started_at,now())
 WHERE id=$id;                          -- 同事务内完成，领取即持租约
COMMIT;  -- 同事务写 task.claimed 事件
```
4. **事件与状态同事务**：时间线重建不依赖异步补写。
5. **大结果写入**：先写 `data/` 文件，再在事务内落引用；文件孤儿由清理任务兜底（阶段 B）。
6. **评测/诊断写入**：Python 计算完成后经 Go 写接口落库，与任务核心状态事务隔离（§10.9）。

## 4. 索引与查询路径
| 高频查询 | 路径 |
|---|---|
| Worker 扫描待领取任务 | `idx_tasks_status_sched`（部分索引） |
| 控制台任务列表/筛选 | `idx_tasks_created` + status/type 过滤（列表页限时分页） |
| 任务详情时间线 | `idx_events_task (task_id, seq)` |
| 证据回溯（trace_id 全链路） | `idx_tasks_trace` + `idx_events_trace` |
| 评测版本对比 | `idx_eval_runs_dataset (dataset_id, agent_version)` |

## 5. 迁移规范
- `platform/migrations/NNNN_name.up.sql` / `.down.sql`，golang-migrate 版本化。
- 迁移只增不改：已发布的迁移文件禁止修改，变更走新迁移。
- `make migrate-up / migrate-down / migrate-status`；CI 与本地验证均以 `agentops_test` 库执行。
