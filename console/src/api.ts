// 统一 API 访问层：全部请求经此发出；错误体 {error:{code,message}} 统一解包。

export class ApiError extends Error {
  code: string;
  status: number;
  constructor(code: string, message: string, status: number) {
    super(message);
    this.code = code;
    this.status = status;
  }
}

async function request<T>(path: string, init?: RequestInit): Promise<T> {
  const resp = await fetch(path, {
    headers: { "Content-Type": "application/json" },
    ...init,
  });
  const body = await resp.json().catch(() => null);
  if (!resp.ok) {
    const err = (body as { error?: { code?: string; message?: string } })?.error;
    throw new ApiError(err?.code ?? `HTTP_${resp.status}`, err?.message ?? resp.statusText, resp.status);
  }
  return body as T;
}

function qs(params: Record<string, string | undefined>): string {
  const q = Object.entries(params)
    .filter(([, v]) => v !== undefined && v !== "")
    .map(([k, v]) => `${encodeURIComponent(k)}=${encodeURIComponent(v!)}`)
    .join("&");
  return q ? `?${q}` : "";
}

// 与后端 envelope 对齐的返回结构
export interface ListResult<T> {
  data: T[];
  meta: { next_cursor?: string | null; total?: number; [k: string]: unknown };
}

export interface CreateTaskPayload {
  task_type: string;
  input: Record<string, unknown>;
  params?: Record<string, unknown>;
  allowed_tools?: string[];
  max_attempts?: number;
  timeout_seconds?: number;
  idempotency_key?: string;
}

export const api = {
  health: () => request<{ status: string; dependencies: { postgres: string } }>("/api/v1/health"),

  listTasks: (filter: { status?: string; task_type?: string; limit?: number; cursor?: string }) =>
    request<ListResult<import("./types").Task>>(`/api/v1/tasks${qs({
      status: filter.status, task_type: filter.task_type,
      limit: filter.limit ? String(filter.limit) : undefined, cursor: filter.cursor,
    })}`),

  createTask: (payload: CreateTaskPayload) =>
    request<{ data: import("./types").Task; meta: Record<string, unknown> }>("/api/v1/tasks", {
      method: "POST",
      body: JSON.stringify(payload),
    }),

  getTask: (id: string) =>
    request<{ data: import("./types").Task }>(`/api/v1/tasks/${id}`),

  cancelTask: (id: string) =>
    request<{ data: import("./types").Task }>(`/api/v1/tasks/${id}/cancel`, { method: "POST" }),

  retryTask: (id: string) =>
    request<{ data: import("./types").Task }>(`/api/v1/tasks/${id}/retry`, { method: "POST" }),

  getTrace: (id: string) =>
    request<{ data: import("./types").Trace }>(`/api/v1/tasks/${id}/trace`),

  listEvents: (id: string, cursor?: string) =>
    request<ListResult<import("./types").TaskEvent>>(
      `/api/v1/tasks/${id}/events${qs({ limit: "50", cursor })}`),

  listTools: () =>
    request<ListResult<import("./types").ToolInfo>>("/api/v1/tools"),

  getDiagnosis: (id: string) =>
    request<{ data: import("./types").DiagnosisReport[]; meta: Record<string, unknown> }>(
      `/api/v1/tasks/${id}/diagnosis`),

  listDatasets: () =>
    request<ListResult<import("./types").Dataset>>("/api/v1/datasets"),

  importDataset: (name: string, ndjson: string) =>
    request<{ data: import("./types").Dataset }>(`/api/v1/datasets${qs({ name })}`, {
      method: "POST",
      headers: { "Content-Type": "application/x-ndjson" },
      body: ndjson,
    }),

  listEvaluations: (filter: { dataset_id?: string; agent_version?: string } = {}) =>
    request<ListResult<import("./types").EvalRun>>(
      `/api/v1/evaluations${qs(filter as Record<string, string | undefined>)}`),

  createEvaluation: (payload: { dataset_id: string; agent_version?: string }) =>
    request<{ data: import("./types").EvalRun }>("/api/v1/evaluations", {
      method: "POST",
      body: JSON.stringify(payload),
    }),

  getEvaluation: (id: string) =>
    request<{ data: import("./types").EvalRunDetail }>(`/api/v1/evaluations/${id}`),
};
