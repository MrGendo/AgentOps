// 展示层纯函数：状态徽标、耗时与指标格式化（可单测）。

import type { TaskStatus } from "./types";

const TERMINAL: TaskStatus[] = ["SUCCEEDED", "FAILED", "CANCELLED"];

export function isTerminal(status: TaskStatus): boolean {
  return TERMINAL.includes(status);
}

/** 终态 FAILED/CANCELLED 可手动重试（与后端状态机一致） */
export function canRetry(status: TaskStatus): boolean {
  return status === "FAILED" || status === "CANCELLED";
}

/** 非终态可请求取消 */
export function canCancel(status: TaskStatus): boolean {
  return !isTerminal(status);
}

export interface Badge {
  label: string;
  className: string;
}

const BADGES: Record<TaskStatus, Badge> = {
  PENDING: { label: "等待", className: "badge gray" },
  RUNNING: { label: "执行中", className: "badge blue" },
  CANCEL_REQUESTED: { label: "取消中", className: "badge orange" },
  SUCCEEDED: { label: "成功", className: "badge green" },
  FAILED: { label: "失败", className: "badge red" },
  CANCELLED: { label: "已取消", className: "badge gray" },
};

export function statusBadge(status: TaskStatus): Badge {
  return BADGES[status] ?? { label: status, className: "badge gray" };
}

export function durationText(ms: number | null | undefined): string {
  if (ms === null || ms === undefined) return "—";
  if (ms < 1000) return `${ms}ms`;
  if (ms < 60_000) return `${(ms / 1000).toFixed(1)}s`;
  return `${Math.floor(ms / 60_000)}m${Math.round((ms % 60_000) / 1000)}s`;
}

export function formatDateTime(iso?: string | null): string {
  if (!iso) return "—";
  const d = new Date(iso);
  return `${d.toLocaleDateString()} ${d.toLocaleTimeString()}`;
}

/** 比率类指标显示为百分比；null 显示占位符（口径见 caliber） */
export function metricText(value: number | null | undefined): string {
  if (value === null || value === undefined) return "—";
  return `${(value * 100).toFixed(1)}%`;
}

export function shortId(id: string): string {
  return id.slice(0, 8);
}
