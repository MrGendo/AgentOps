import { beforeEach, describe, expect, it, vi } from "vitest";

import { ApiError, api } from "./api";

function mockFetch(status: number, body: unknown) {
  return vi.fn().mockResolvedValue({
    ok: status >= 200 && status < 300,
    status,
    statusText: "err",
    json: () => Promise.resolve(body),
  });
}

beforeEach(() => {
  vi.restoreAllMocks();
});

describe("api 层", () => {
  it("listTasks 拼接过滤与分页参数", async () => {
    const f = mockFetch(200, { data: [], meta: {} });
    vi.stubGlobal("fetch", f);
    await api.listTasks({ status: "FAILED", limit: 20, cursor: "c_1_2" });
    const [url] = f.mock.calls[0] as [string];
    expect(url).toContain("status=FAILED");
    expect(url).toContain("limit=20");
    expect(url).toContain("cursor=c_1_2");
  });

  it("createTask 以 JSON 体 POST", async () => {
    const f = mockFetch(201, { data: { id: "t1" }, meta: {} });
    vi.stubGlobal("fetch", f);
    await api.createTask({ task_type: "mock.echo", input: { m: 1 } });
    const [, init] = f.mock.calls[0] as [string, RequestInit];
    expect(init.method).toBe("POST");
    expect(JSON.parse(init.body as string).task_type).toBe("mock.echo");
  });

  it("错误包络解包为 ApiError（含稳定错误码）", async () => {
    vi.stubGlobal("fetch", mockFetch(409, {
      error: { code: "TASK_ALREADY_FINISHED", message: "任务已处于终态", retryable: false },
    }));
    await expect(api.cancelTask("t1")).rejects.toMatchObject({
      code: "TASK_ALREADY_FINISHED",
      message: "任务已处于终态",
    });
    await expect(api.cancelTask("t1")).rejects.toBeInstanceOf(ApiError);
  });

  it("JSONL 导入使用 x-ndjson 内容类型与查询参数", async () => {
    const f = mockFetch(201, { data: { id: "d1" }, meta: {} });
    vi.stubGlobal("fetch", f);
    await api.importDataset("demo", '{"a":1}\n');
    const [url, init] = f.mock.calls[0] as [string, RequestInit];
    expect(url).toContain("name=demo");
    expect((init.headers as Record<string, string>)["Content-Type"]).toBe("application/x-ndjson");
  });
});
