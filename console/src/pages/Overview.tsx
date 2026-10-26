import { useCallback, useEffect, useState } from "react";
import { Link } from "react-router-dom";

import { api } from "../api";
import { PageHeader } from "../components/Layout";
import { StatCard } from "../components/StatCard";
import { StatusBadge } from "../components/StatusBadge";
import { EmptyState, ErrorBanner, Loading } from "../components/StateViews";
import { Icon } from "../icons";
import { formatDateTime, shortId } from "../format";
import type { Task } from "../types";

// 总览：核心运行情况。统计来自各状态列表的 meta.total（真实计数），无汇总接口不伪造。
export default function Overview() {
  const [totals, setTotals] = useState<Record<string, number | null>>({
    "": null, PENDING: null, RUNNING: null, SUCCEEDED: null, FAILED: null,
  });
  const [recent, setRecent] = useState<Task[] | null>(null);
  const [failed, setFailed] = useState<Task[] | null>(null);
  const [health, setHealth] = useState<{ status: string; dependencies: { postgres: string } } | null>(null);
  const [error, setError] = useState<string | null>(null);

  const load = useCallback(async () => {
    setError(null);
    try {
      const [all, pending, running, ok, failedList, latest, hl] = await Promise.all([
        api.listTasks({ limit: 1 }),
        api.listTasks({ status: "PENDING", limit: 1 }),
        api.listTasks({ status: "RUNNING", limit: 1 }),
        api.listTasks({ status: "SUCCEEDED", limit: 1 }),
        api.listTasks({ status: "FAILED", limit: 5 }),
        api.listTasks({ limit: 8 }),
        api.health().catch(() => null),
      ]);
      setTotals({
        "": all.meta.total ?? 0,
        PENDING: pending.meta.total ?? 0,
        RUNNING: running.meta.total ?? 0,
        SUCCEEDED: ok.meta.total ?? 0,
        FAILED: failedList.meta.total ?? 0,
      });
      setRecent(latest.data ?? []);
      setFailed(failedList.data ?? []);
      setHealth(hl);
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e));
    }
  }, []);

  useEffect(() => {
    void load();
    const h = window.setInterval(() => void load(), 10_000);
    return () => window.clearInterval(h);
  }, [load]);

  const pendingCount = totals[""];

  return (
    <section>
      <PageHeader
        title="总览"
        desc="平台核心运行情况 · 数据每 10 秒自动刷新"
        actions={<button onClick={() => void load()}><Icon name="refresh" size={14} />刷新</button>}
      />
      {error && <ErrorBanner message={`加载失败：${error}`} />}

      <div className="stat-grid">
        <StatCard label="任务总数" value={pendingCount ?? "—"} icon={<Icon name="tasks" size={15} />} />
        <StatCard label="等待中" value={totals.PENDING ?? "—"} tone="wait" icon={<Icon name="clock" size={15} />} />
        <StatCard label="运行中" value={totals.RUNNING ?? "—"} tone="run" icon={<Icon name="pulse" size={15} />} />
        <StatCard label="成功" value={totals.SUCCEEDED ?? "—"} tone="ok" icon={<Icon name="check" size={15} />} />
        <StatCard label="失败" value={totals.FAILED ?? "—"} tone="err" icon={<Icon name="warn" size={15} />} />
      </div>

      <div className="card">
        <h3>平台健康</h3>
        {health ? (
          <div className="row">
            <span className="muted">Go 平台：</span>
            <b className={health.status === "ok" ? "green-text" : "orange-text"}>{health.status}</b>
            <span className="muted">PostgreSQL：</span>
            <b className={health.dependencies.postgres === "up" ? "green-text" : "red-text"}>
              {health.dependencies.postgres}
            </b>
            <Link to="/health" className="small-text">详情 →</Link>
          </div>
        ) : <span className="muted">不可用</span>}
      </div>

      <div className="card">
        <h3>最近任务</h3>
        {recent === null ? <Loading /> : recent.length === 0 ? (
          <EmptyState text="暂无任务" hint="可到「任务」页创建" />
        ) : (
          <table className="table">
            <thead>
              <tr><th>ID</th><th>类型</th><th>状态</th><th>尝试</th><th>错误码</th><th>创建时间</th></tr>
            </thead>
            <tbody>
              {recent.map((t) => (
                <tr key={t.id}>
                  <td className="mono"><Link to={`/tasks/${t.id}`}>{shortId(t.id)}</Link></td>
                  <td>{t.task_type}</td>
                  <td><StatusBadge status={t.status} /></td>
                  <td>{t.attempt_count}</td>
                  <td className="ellipsis" title={t.error_message ?? ""}>{t.error_code ?? "—"}</td>
                  <td className="muted">{formatDateTime(t.created_at)}</td>
                </tr>
              ))}
            </tbody>
          </table>
        )}
      </div>

      <div className="card">
        <h3>近期失败任务</h3>
        {failed === null ? <Loading /> : failed.length === 0 ? (
          <EmptyState text="近期没有失败任务" />
        ) : (
          <table className="table">
            <thead><tr><th>ID</th><th>类型</th><th>错误码</th><th>时间</th><th></th></tr></thead>
            <tbody>
              {failed.map((t) => (
                <tr key={t.id}>
                  <td className="mono"><Link to={`/tasks/${t.id}`}>{shortId(t.id)}</Link></td>
                  <td>{t.task_type}</td>
                  <td className="red-text">{t.error_code ?? "—"}</td>
                  <td className="muted">{formatDateTime(t.finished_at ?? t.created_at)}</td>
                  <td><Link to={`/tasks/${t.id}`} className="small-text">查看诊断 →</Link></td>
                </tr>
              ))}
            </tbody>
          </table>
        )}
      </div>
    </section>
  );
}
