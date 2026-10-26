import { describe, expect, it } from "vitest";

import {
  canCancel, canRetry, durationText, isTerminal, metricText, shortId, statusBadge,
} from "./format";

describe("状态语义（与后端状态机一致）", () => {
  it("终态识别", () => {
    expect(isTerminal("SUCCEEDED")).toBe(true);
    expect(isTerminal("FAILED")).toBe(true);
    expect(isTerminal("CANCELLED")).toBe(true);
    expect(isTerminal("RUNNING")).toBe(false);
    expect(isTerminal("PENDING")).toBe(false);
  });
  it("仅 FAILED/CANCELLED 可重试", () => {
    expect(canRetry("FAILED")).toBe(true);
    expect(canRetry("CANCELLED")).toBe(true);
    expect(canRetry("SUCCEEDED")).toBe(false);
    expect(canRetry("RUNNING")).toBe(false);
  });
  it("非终态可取消", () => {
    expect(canCancel("PENDING")).toBe(true);
    expect(canCancel("RUNNING")).toBe(true);
    expect(canCancel("CANCEL_REQUESTED")).toBe(true);
    expect(canCancel("SUCCEEDED")).toBe(false);
  });
  it("徽标映射", () => {
    expect(statusBadge("FAILED").className).toContain("red");
    expect(statusBadge("SUCCEEDED").label).toBe("成功");
  });
});

describe("格式化", () => {
  it("耗时", () => {
    expect(durationText(null)).toBe("—");
    expect(durationText(50)).toBe("50ms");
    expect(durationText(1500)).toBe("1.5s");
    expect(durationText(125_000)).toBe("2m5s");
  });
  it("比率指标", () => {
    expect(metricText(null)).toBe("—");
    expect(metricText(0.75)).toBe("75.0%");
    expect(metricText(1)).toBe("100.0%");
  });
  it("ID 缩写", () => {
    expect(shortId("12345678-90ab")).toBe("12345678");
  });
});
