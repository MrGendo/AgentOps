-- 评测与诊断表（阶段 D；蓝本 docs/database.md §2.7-2.11）
-- 与核心任务状态逻辑隔离：独立表组，Go 统一写入（架构 D-1）
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
    input_task     jsonb NOT NULL,
    expected       jsonb NOT NULL DEFAULT '{}',
    allowed_tools  jsonb NOT NULL DEFAULT '[]',
    run_limits     jsonb NOT NULL DEFAULT '{}',
    scoring_rules  jsonb NOT NULL DEFAULT '[]',
    tags           text[] NOT NULL DEFAULT '{}',
    difficulty     text NOT NULL DEFAULT 'normal',
    UNIQUE (dataset_id, seq)
);

CREATE TABLE evaluation_runs (
    id               uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    dataset_id       uuid NOT NULL REFERENCES evaluation_datasets(id),
    dataset_version  int  NOT NULL,
    agent_version    text NOT NULL,
    run_config       jsonb NOT NULL DEFAULT '{}',
    status           text NOT NULL DEFAULT 'PENDING'
                     CHECK (status IN ('PENDING','RUNNING','SUCCEEDED','FAILED','CANCELLED')),
    metrics          jsonb,
    error_code       text,
    error_message    text,
    started_at       timestamptz,
    finished_at      timestamptz,
    created_at       timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX idx_eval_runs_dataset ON evaluation_runs (dataset_id, agent_version);

CREATE TABLE evaluation_results (
    id            uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    eval_run_id   uuid NOT NULL REFERENCES evaluation_runs(id) ON DELETE CASCADE,
    case_id       uuid NOT NULL REFERENCES evaluation_cases(id),
    task_id       uuid REFERENCES tasks(id),
    agent_run_id  uuid,
    status        text NOT NULL CHECK (status IN ('SUCCEEDED','FAILED','ERROR')),
    scores        jsonb NOT NULL DEFAULT '{}',
    evidence      jsonb NOT NULL DEFAULT '{}',
    token_usage   jsonb,
    duration_ms   int,
    error_code    text,
    created_at    timestamptz NOT NULL DEFAULT now(),
    UNIQUE (eval_run_id, case_id)
);

CREATE TABLE diagnosis_reports (
    id                    uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    task_id               uuid NOT NULL REFERENCES tasks(id) ON DELETE CASCADE,
    agent_run_id          uuid,
    fault_code            text NOT NULL CHECK (fault_code IN
                          ('MODEL_ERROR','PLANNING_ERROR','TOOL_SELECTION_ERROR',
                           'TOOL_ARGUMENT_ERROR','TOOL_EXECUTION_ERROR','TIMEOUT_OR_UNKNOWN',
                           'CONTEXT_ERROR','POLICY_VIOLATION','ORCHESTRATION_ERROR',
                           'EVALUATION_ERROR')),
    confidence            text NOT NULL CHECK (confidence IN ('HIGH','MEDIUM','LOW')),
    root_cause_hypothesis text NOT NULL,
    evidence              jsonb NOT NULL DEFAULT '[]',
    suggestions           jsonb NOT NULL DEFAULT '[]',
    need_human            boolean NOT NULL DEFAULT false,
    diagnoser_version     text NOT NULL,
    config                jsonb NOT NULL DEFAULT '{}',
    created_at            timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX idx_diagnosis_task ON diagnosis_reports (task_id);
