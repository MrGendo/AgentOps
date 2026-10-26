// 与 docs/openapi/api-v1.yaml 一一对应的类型定义；字段以后端 JSON 为准。

export type TaskStatus =
  | "PENDING"
  | "RUNNING"
  | "CANCEL_REQUESTED"
  | "SUCCEEDED"
  | "FAILED"
  | "CANCELLED";

export interface Task {
  id: string;
  task_type: string;
  input: Record<string, unknown>;
  params: Record<string, unknown>;
  allowed_tools?: string[];
  status: TaskStatus;
  idempotency_key?: string;
  trace_id: string;
  attempt_count: number;
  max_attempts: number;
  timeout_seconds: number;
  error_code?: string | null;
  error_message?: string | null;
  finish_reason?: string | null;
  created_at: string;
  started_at?: string | null;
  finished_at?: string | null;
}

export interface Envelope<T> {
  data: T;
  meta: { next_cursor?: string | null; total?: number; [k: string]: unknown };
}

export interface StepAttempt {
  id: string;
  attempt_no: number;
  status: string;
  error_class?: string | null;
  error_code?: string | null;
  error_message?: string | null;
  started_at: string;
  finished_at?: string | null;
  duration_ms?: number | null;
}

export interface Step {
  id: string;
  seq: number;
  name: string;
  step_type: string;
  status: string;
  output_ref?: string | null;
  error_code?: string | null;
  error_message?: string | null;
  started_at?: string | null;
  finished_at?: string | null;
  attempts: StepAttempt[];
}

export interface ToolCall {
  id: string;
  tool_name: string;
  tool_version: string;
  status: string;
  params: unknown;
  result?: unknown;
  error_code?: string | null;
  error_message?: string | null;
  duration_ms?: number | null;
  created_at: string;
}

export interface AgentRunSummary {
  id: string;
  status: string;
  finish_reason: string;
  iteration_count: number;
  tool_call_count: number;
  model_call_count: number;
  token_usage?: { prompt_tokens?: number; completion_tokens?: number; total_tokens?: number };
  final_output?: unknown;
  error_code: string;
}

export interface Trace {
  steps: Step[];
  tool_calls: ToolCall[];
  agent_runs: AgentRunSummary[];
}

export interface TaskEvent {
  seq: number;
  event_id: string;
  event_type: string;
  payload: Record<string, unknown>;
  created_at: string;
  step_id?: string;
  tool_call_id?: string;
}

export interface ToolInfo {
  name: string;
  version: string;
  description: string;
  risk_level: string;
  timeout_ms: number;
  idempotent: boolean;
}

export interface DiagnosisReport {
  id: string;
  task_id: string;
  fault_code: string;
  confidence: "HIGH" | "MEDIUM" | "LOW";
  root_cause_hypothesis: string;
  evidence: Array<{ type: string; ref: string; detail: string }>;
  suggestions: string[];
  need_human: boolean;
  diagnoser_version: string;
  created_at: string;
}

export interface Dataset {
  id: string;
  name: string;
  version: number;
  description?: string;
  case_count: number;
  created_at: string;
}

export interface MetricItem {
  value: number | null;
  caliber: string;
}

export interface EvalRun {
  id: string;
  dataset_id: string;
  dataset_name: string;
  dataset_version: number;
  agent_version: string;
  status: string;
  metrics?: Record<string, MetricItem> | null;
  error_code?: string | null;
  error_message?: string | null;
  created_at: string;
  finished_at?: string | null;
}

export interface EvalCaseResult {
  case_seq: number;
  task_id?: string | null;
  status: string;
  scores: Array<{ rule: string; pass: boolean; evidence?: Record<string, unknown> }>;
  evidence: { all_rules_pass?: boolean };
  duration_ms?: number | null;
  error_code?: string | null;
}

export interface EvalRunDetail extends EvalRun {
  results: EvalCaseResult[];
}
