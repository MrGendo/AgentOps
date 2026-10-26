-- 核心表结构（阶段 B）：与 docs/database.md §2 一一对应，只增不改
CREATE TABLE tasks (
    id               uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    task_type        text NOT NULL,
    input            jsonb NOT NULL,
    params           jsonb NOT NULL DEFAULT '{}',
    allowed_tools    jsonb NOT NULL DEFAULT '[]',
    status           text NOT NULL DEFAULT 'PENDING'
                     CHECK (status IN ('PENDING','RUNNING','CANCEL_REQUESTED',
                                       'SUCCEEDED','FAILED','CANCELLED')),
    idempotency_key  text UNIQUE,
    trace_id         uuid NOT NULL DEFAULT gen_random_uuid(),
    attempt_count    int  NOT NULL DEFAULT 0,
    max_attempts     int  NOT NULL DEFAULT 3,
    timeout_seconds  int  NOT NULL DEFAULT 600,
    lease_version    bigint NOT NULL DEFAULT 0,
    lease_owner      text,
    lease_expires_at timestamptz,
    scheduled_at     timestamptz NOT NULL DEFAULT now(),
    error_code       text,
    error_message    text,
    finish_reason    text,
    result_ref       text,
    created_at       timestamptz NOT NULL DEFAULT now(),
    started_at       timestamptz,
    finished_at      timestamptz
);
CREATE INDEX idx_tasks_status_sched ON tasks (status, scheduled_at) WHERE status = 'PENDING';
CREATE INDEX idx_tasks_created ON tasks (created_at DESC);
CREATE INDEX idx_tasks_trace ON tasks (trace_id);

CREATE TABLE task_steps (
    id             uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    task_id        uuid NOT NULL REFERENCES tasks(id) ON DELETE CASCADE,
    seq            int  NOT NULL,
    name           text NOT NULL,
    step_type      text NOT NULL,
    status         text NOT NULL DEFAULT 'PENDING'
                   CHECK (status IN ('PENDING','RUNNING','SUCCEEDED','FAILED','CANCELLED')),
    input_digest   jsonb,
    output_ref     text,
    attempt_count  int  NOT NULL DEFAULT 0,
    error_code     text,
    error_message  text,
    started_at     timestamptz,
    finished_at    timestamptz,
    UNIQUE (task_id, seq)
);
CREATE INDEX idx_steps_task ON task_steps (task_id, seq);

CREATE TABLE step_attempts (
    id             uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    step_id        uuid NOT NULL REFERENCES task_steps(id) ON DELETE CASCADE,
    task_id        uuid NOT NULL REFERENCES tasks(id) ON DELETE CASCADE,
    attempt_no     int  NOT NULL,
    status         text NOT NULL CHECK (status IN ('RUNNING','SUCCEEDED','FAILED','CANCELLED')),
    input_digest   jsonb,
    output_ref     text,
    error_class    text,
    error_code     text,
    error_message  text,
    started_at     timestamptz NOT NULL,
    finished_at    timestamptz,
    duration_ms    int,
    UNIQUE (step_id, attempt_no)
);
CREATE INDEX idx_attempts_task ON step_attempts (task_id);

CREATE TABLE tool_calls (
    id              uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    task_id         uuid NOT NULL REFERENCES tasks(id) ON DELETE CASCADE,
    step_id         uuid REFERENCES task_steps(id) ON DELETE SET NULL,
    agent_run_id    uuid,
    tool_name       text NOT NULL,
    tool_version    text NOT NULL,
    params          jsonb NOT NULL,
    status          text NOT NULL
                    CHECK (status IN ('SUCCEEDED','FAILED','TIMEOUT_UNKNOWN','REJECTED')),
    result          jsonb,
    result_ref      text,
    error_code      text,
    error_message   text,
    idempotency_key text UNIQUE,
    risk_level      text NOT NULL DEFAULT 'LOW',
    duration_ms     int,
    created_at      timestamptz NOT NULL DEFAULT now(),
    started_at      timestamptz,
    finished_at     timestamptz
);
CREATE INDEX idx_tool_calls_task ON tool_calls (task_id);
CREATE INDEX idx_tool_calls_run  ON tool_calls (agent_run_id);

CREATE TABLE task_events (
    seq          bigserial PRIMARY KEY,
    event_id     uuid NOT NULL UNIQUE DEFAULT gen_random_uuid(),
    task_id      uuid NOT NULL,
    trace_id     uuid NOT NULL,
    step_id      uuid,
    attempt_id   uuid,
    tool_call_id uuid,
    event_type   text NOT NULL,
    payload      jsonb NOT NULL DEFAULT '{}',
    created_at   timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX idx_events_task ON task_events (task_id, seq);
CREATE INDEX idx_events_trace ON task_events (trace_id);
CREATE INDEX idx_events_type ON task_events (event_type, created_at);

-- agent_runs 阶段 C 才有写入方，此阶段先建表稳定迁移基线（设计决策 D-B4）
CREATE TABLE agent_runs (
    id                  uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    task_id             uuid NOT NULL REFERENCES tasks(id) ON DELETE CASCADE,
    agent_version       text NOT NULL,
    model_config        jsonb NOT NULL DEFAULT '{}',
    status              text NOT NULL DEFAULT 'PENDING'
                        CHECK (status IN ('PENDING','RUNNING','SUCCEEDED','FAILED','CANCELLED')),
    goal_digest         text,
    plan                jsonb,
    plan_valid          boolean,
    plan_errors         jsonb,
    model_call_count    int NOT NULL DEFAULT 0,
    tool_call_count     int NOT NULL DEFAULT 0,
    iteration_count     int NOT NULL DEFAULT 0,
    token_usage         jsonb,
    trajectory_ref      text,
    final_output_ref    text,
    finish_reason       text,
    error_code          text,
    error_message       text,
    created_at          timestamptz NOT NULL DEFAULT now(),
    started_at          timestamptz,
    finished_at         timestamptz
);
CREATE INDEX idx_agent_runs_task ON agent_runs (task_id);
