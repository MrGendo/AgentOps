// 统一 API 访问层：所有请求走相对路径 /api/v1（开发期由 Vite 代理到 Go 平台）。
// 与 docs/openapi/api-v1.yaml 契约对齐。

export interface HealthResponse {
  status: "ok" | "degraded";
  dependencies: {
    postgres: "up" | "down";
  };
}

export async function fetchHealth(): Promise<HealthResponse> {
  const resp = await fetch("/api/v1/health");
  if (!resp.ok) {
    throw new Error(`健康检查失败: HTTP ${resp.status}`);
  }
  return resp.json() as Promise<HealthResponse>;
}
