import { useCallback, useEffect, useState } from "react";
import { Link, useParams } from "react-router-dom";

import { api } from "../api";
import { canCancel, canRetry, durationText, formatDateTime, isTerminal, statusBadge } from "../format";
import type { DiagnosisReport, Task, TaskEvent, Trace } from "../types";

const EVENT_COLORS: Record<string, string> = {
  "task.failed": "red",
  "tool.call.rejected": "orange",
  "lease.revoked": "orange",
  "task.result_discarded": "orange",
  "task.succeeded": "green",
  "task.cancelled": "gray",
};

export default function TaskDetail() {
  const { id = "" } = useParams();
  const [task, setTask] = useState<Task | null>(null);
  const [trace, setTrace] = useState<Trace | null>(null);
  const [events, setEvents] = useState<TaskEvent[]>([]);
  const [eventCursor, setEventCursor] = useState<string | undefined>(undefined);
  const [moreEvents, setMoreEvents] = useState(false);
  const [reports, setReports] = useState<DiagnosisReport[] | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [toast, setToast] = useState<string | null>(null);

  const notify = (m: string) => {
    setToast(m);
    window.setTimeout(() => setToast(null), 3000);
  };

  const load = useCallback(async (withTrace = true) => {
    try {
      const t = await api.getTask(id);
      setTask(t.data);
      if (withTrace) {
        const [tr, ev, dg] = await Promise.all([
          api.getTrace(id),
          api.listEvents(id),
          api.getDiagnosis(id).catch(() => null),
        ]);
        setTrace(tr.data);
        setEvents(ev.data ?? []);
        setMoreEvents(Boolean(ev.meta?.next_cursor));
        if (dg) setReports(dg.data ?? []);
      }
      setError(null);
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e));
    }
  }, [id]);

  useEffect(() => {
    void load();
  }, [load]);

  // 未到终态时轮询刷新；终态后停
  useEffect(() => {
    if (!task || isTerminal(task.status)) return;
    const h = window.setInterval(() => void load(false), 5000);
    return () => window.clearInterval(h);
  }, [task, load]);

  const doCancel = async () => {
    try {
      await api.cancelTask(id);
      notify("取消请求已受理");
      void load(false);
    } catch (e) {
      notify(`取消失败：${e instanceof Error ? e.message : e}`);
    }
  };
  const doRetry = async () => {
    try {
      await api.retryTask(id);
      notify("重试已受理");
      void load();
    } catch (e) {
      notify(`重试失败：${e instanceof Error ? e.message : e}`);
    }
  };

  const loadMoreEvents = async () => {
    const resp = await api.listEvents(id, eventCursor);
    setEvents((prev) => [...prev, ...(resp.data ?? [])]);
    setMoreEvents(Boolean(resp.meta?.next_cursor));
    setEventCursor(resp.meta?.next_cursor ?? undefined);
  };

  if (error) return <div className="banner red">加载失败：{error}</div>;
  if (!task) return <p className="muted">加载中…</p>;

  const badge = statusBadge(task.status);

  return (
    <section>
      <div className="toolbar">
        <h2>任务 {task.id.slice(0, 8)}</h2>
        <div className="actions">
          {canCancel(task.status) && <button onClick={() => void doCancel()}>取消</button>}
          {canRetry(task.status) && <button className="primary" onClick={() => void doRetry()}>重试</button>}
        </div>
      </div>
      {toast && <div className="banner blue">{toast}</div>}

      <div className="card">
        <div className="kv">
          <span>类型</span><b>{task.task_type}</b>
          <span>状态</span><b><span className={badge.className}>{badge.label}</span></b>
          <span>尝试</span><b>{task.attempt_count} / 上限 {task.max_attempts}</b>
          <span>超时上限</span><b>{task.timeout_seconds}s</b>
          <span>Trace ID</span><b className="mono">{task.trace_id.slice(0, 8)}</b>
          <span>创建</span><b>{formatDateTime(task.created_at)}</b>
          <span>开始</span><b>{formatDateTime(task.started_at)}</b>
          <span>结束</span><b>{formatDateTime(task.finished_at)}</b>
          {task.error_code && <><span>错误码</span><b className="red-text">{task.error_code}</b></>}
          {task.finish_reason && <><span>结束原因</span><b>{task.finish_reason}</b></>}
          <span>授权工具</span><b>{(task.allowed_tools ?? []).join("、") || "—"}</b>
        </div>
        {task.error_message && <pre className="code">{task.error_message}</pre>}
      </div>

      {trace?.agent_runs?.map((r) => (
        <div className="card" key={r.id}>
          <h3>Agent 运行</h3>
          <div className="kv">
            <span>状态</span><b>{r.status}</b>
            <span>结束原因</span><b>{r.finish_reason || "—"}</b>
            <span>迭代</span><b>{r.iteration_count}</b>
            <span>模型调用</span><b>{r.model_call_count}</b>
            <span>Token</span><b>{r.token_usage?.total_tokens ?? 0}</b>
          </div>
          {r.final_output != null && (
            <details><summary>最终输出</summary>
              <pre className="code">{JSON.stringify(r.final_output, null, 2)}</pre>
            </details>
          )}
        </div>
      ))}

      <div className="card">
        <h3>执行步骤（{trace?.steps?.length ?? 0}）</h3>
        {trace?.steps?.map((s) => (
          <details key={s.id} className="step">
            <summary>
              <span className={s.status === "SUCCEEDED" ? "green-text" : s.status === "FAILED" ? "red-text" : ""}>
                #{s.seq} {s.name} — {s.status}
              </span>
              {s.error_code && <span className="muted">（{s.error_code}）</span>}
            </summary>
            <table className="table small">
              <thead><tr><th>尝试</th><th>状态</th><th>错误类</th><th>耗时</th></tr></thead>
              <tbody>
                {s.attempts?.map((a) => (
                  <tr key={a.id}>
                    <td>#{a.attempt_no}</td>
                    <td>{a.status}</td>
                    <td>{a.error_class ?? "—"}</td>
                    <td>{durationText(a.duration_ms)}</td>
                  </tr>
                ))}
              </tbody>
            </table>
            {s.output_ref && <pre className="code small">{s.output_ref}</pre>}
          </details>
        ))}
        {trace?.steps?.length === 0 && <p className="muted">尚无步骤</p>}
      </div>

      <div className="card">
        <h3>工具调用（{trace?.tool_calls?.length ?? 0}）</h3>
        <table className="table">
          <thead><tr><th>工具</th><th>版本</th><th>状态</th><th>耗时</th><th>错误</th></tr></thead>
          <tbody>
            {trace?.tool_calls?.map((c) => (
              <tr key={c.id}>
                <td>{c.tool_name}</td>
                <td>{c.tool_version}</td>
                <td>{c.status}</td>
                <td>{durationText(c.duration_ms)}</td>
                <td className="ellipsis" title={c.error_message ?? ""}>{c.error_code ?? "—"}</td>
              </tr>
            ))}
            {trace?.tool_calls?.length === 0 && (
              <tr><td colSpan={5} className="muted center">无工具调用</td></tr>
            )}
          </tbody>
        </table>
      </div>

      <div className="card">
        <h3>事件时间线</h3>
        <ol className="timeline">
          {events.map((e) => (
            <li key={e.seq} className={EVENT_COLORS[e.event_type] ?? ""}>
              <span className="mono muted">#{e.seq}</span> {e.event_type}
              <span className="muted"> {formatDateTime(e.created_at)}</span>
            </li>
          ))}
        </ol>
        {moreEvents && <button onClick={() => void loadMoreEvents()}>加载更多</button>}
        {events.length === 0 && <p className="muted">无事件</p>}
      </div>

      <div className="card">
        <h3>失败诊断</h3>
        {reports === null && <p className="muted">—</p>}
        {reports?.length === 0 && (
          <p className="muted">
            {task.status === "FAILED" || task.status === "CANCELLED"
              ? "暂无报告（查询时已自动触发生成，稍后刷新）"
              : "任务未失败，无诊断报告"}
          </p>
        )}
        {reports?.map((r) => (
          <div key={r.id} className="diagnosis">
            <div className="kv">
              <span>故障码</span><b className={r.confidence === "LOW" ? "orange-text" : "red-text"}>{r.fault_code}</b>
              <span>置信度</span><b>{r.confidence}</b>
              <span>需人工</span><b>{r.need_human ? "是" : "否"}</b>
              <span>诊断器</span><b>{r.diagnoser_version}</b>
            </div>
            <p><b>根因假设：</b>{r.root_cause_hypothesis}</p>
            <p><b>证据：</b></p>
            <ul>
              {r.evidence?.map((ev, i) => (
                <li key={i} className="mono small-text">[{ev.type}] {ev.ref} — {ev.detail}</li>
              ))}
            </ul>
            <p><b>建议：</b></p>
            <ul>{r.suggestions?.map((s, i) => <li key={i}>{s}</li>)}</ul>
          </div>
        ))}
      </div>

      <p><Link to="/">← 返回任务列表</Link></p>
    </section>
  );
}
