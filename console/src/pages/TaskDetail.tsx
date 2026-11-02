import { useCallback, useEffect, useState } from "react";
import { Link, useParams } from "react-router-dom";

import { api } from "../api";
import { PageHeader } from "../components/Layout";
import { StatusBadge } from "../components/StatusBadge";
import { EmptyState, ErrorBanner, Loading } from "../components/StateViews";
import { Icon } from "../icons";
import { canCancel, canRetry, durationText, formatDateTime, isTerminal } from "../format";
import type { DiagnosisReport, Task, TaskEvent, Trace } from "../types";

const EVENT_COLORS: Record<string, string> = {
  "task.failed": "red", "tool.call.rejected": "orange", "lease.revoked": "orange",
  "task.result_discarded": "orange", "task.succeeded": "green", "task.cancelled": "gray",
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

  const notify = (m: string) => { setToast(m); window.setTimeout(() => setToast(null), 3000); };

  const load = useCallback(async (withTrace = true) => {
    try {
      const t = await api.getTask(id);
      setTask(t.data);
      if (withTrace) {
        const [tr, ev, dg] = await Promise.all([
          api.getTrace(id), api.listEvents(id), api.getDiagnosis(id).catch(() => null),
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

  useEffect(() => { void load(); }, [load]);

  useEffect(() => {
    if (!task || isTerminal(task.status)) return;
    const h = window.setInterval(() => void load(false), 5000);
    return () => window.clearInterval(h);
  }, [task, load]);

  const doCancel = async () => {
    try { await api.cancelTask(id); notify("取消请求已受理"); void load(false); }
    catch (e) { notify(`取消失败：${e instanceof Error ? e.message : e}`); }
  };
  const doRetry = async () => {
    try { await api.retryTask(id); notify("重试已受理"); void load(); }
    catch (e) { notify(`重试失败：${e instanceof Error ? e.message : e}`); }
  };
  const loadMoreEvents = async () => {
    const resp = await api.listEvents(id, eventCursor);
    setEvents((prev) => [...prev, ...(resp.data ?? [])]);
    setMoreEvents(Boolean(resp.meta?.next_cursor));
    setEventCursor(resp.meta?.next_cursor ?? undefined);
  };

  if (error) return <ErrorBanner message={`加载失败：${error}`} />;
  if (!task) return <Loading />;

  // 执行进度 = 步骤完成度（真实数据计算，非估算）
  const steps = trace?.steps ?? [];
  const doneSteps = steps.filter((s) => s.status === "SUCCEEDED").length;
  const progress = steps.length ? Math.round((doneSteps / steps.length) * 100) : null;

  return (
    <section>
      <PageHeader
        title={`任务 ${task.id.slice(0, 8)}`}
        desc={`${task.task_type} · Trace ${task.trace_id.slice(0, 8)}`}
        actions={
          <>
            {canCancel(task.status) && <button onClick={() => void doCancel()}><Icon name="cancel" size={14} />取消</button>}
            {canRetry(task.status) && <button className="primary" onClick={() => void doRetry()}><Icon name="retry" size={14} />重试</button>}
          </>
        }
      />
      {toast && <div className="banner blue">{toast}</div>}

      <div className="card">
        <div className="row" style={{ justifyContent: "space-between", marginBottom: 12 }}>
          <h3 style={{ margin: 0 }}>基本信息</h3>
          <StatusBadge status={task.status} />
        </div>
        <div className="kv">
          <span>类型</span><b>{task.task_type}</b>
          <span>尝试</span><b>{task.attempt_count} / 上限 {task.max_attempts}</b>
          <span>超时上限</span><b>{task.timeout_seconds}s</b>
          <span>授权工具</span><b>{(task.allowed_tools ?? []).join("、") || "—"}</b>
          <span>创建</span><b>{formatDateTime(task.created_at)}</b>
          <span>开始</span><b>{formatDateTime(task.started_at)}</b>
          <span>结束</span><b>{formatDateTime(task.finished_at)}</b>
          {task.finish_reason && <><span>结束原因</span><b>{task.finish_reason}</b></>}
          {task.error_code && <><span>错误码</span><b className="red-text">{task.error_code}</b></>}
        </div>
        {progress !== null && (
          <div style={{ marginTop: 12 }}>
            <div className="muted small-text" style={{ marginBottom: 4 }}>
              执行进度：{doneSteps}/{steps.length} 步骤完成（{progress}%）
            </div>
            <div className="progress-bar"><i style={{ width: `${progress}%` }} /></div>
          </div>
        )}
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
            <span>工具调用</span><b>{r.tool_call_count}</b>
            <span>Token</span><b>{r.token_usage?.total_tokens ?? 0}</b>
          </div>
          {r.final_output != null && (
            <details><summary className="muted">最终输出</summary>
              <pre className="code">{JSON.stringify(r.final_output, null, 2)}</pre>
            </details>
          )}
        </div>
      ))}

      <div className="card">
        <h3>执行步骤（{steps.length}）</h3>
        {steps.length === 0 ? <EmptyState text="尚无步骤" /> : steps.map((s) => (
          <details key={s.id} className="step">
            <summary>
              <span className={s.status === "SUCCEEDED" ? "green-text" : s.status === "FAILED" ? "red-text" : "muted"}>
                #{s.seq} {s.name} — {s.status}
              </span>
              {s.error_code && <span className="muted small-text">（{s.error_code}）</span>}
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
      </div>

      <div className="card">
        <h3>工具调用（{trace?.tool_calls?.length ?? 0}）</h3>
        {(trace?.tool_calls ?? []).length === 0 ? <EmptyState text="无工具调用" /> : (
          <table className="table">
            <thead><tr><th>工具</th><th>版本</th><th>状态</th><th>耗时</th><th>错误</th></tr></thead>
            <tbody>
              {trace?.tool_calls?.map((c) => (
                <tr key={c.id}>
                  <td>{c.tool_name}</td>
                  <td className="mono muted">{c.tool_version}</td>
                  <td>{c.status}</td>
                  <td>{durationText(c.duration_ms)}</td>
                  <td className="ellipsis" title={c.error_message ?? ""}>{c.error_code ?? "—"}</td>
                </tr>
              ))}
            </tbody>
          </table>
        )}
      </div>

      <div className="card">
        <h3>事件时间线</h3>
        {events.length === 0 ? <EmptyState text="无事件" /> : (
          <>
            <ol className="timeline">
              {events.map((e) => (
                <li key={e.seq} className={EVENT_COLORS[e.event_type] ?? ""}>
                  <span className="mono muted">#{e.seq}</span> {e.event_type}
                  <span className="muted small-text"> {formatDateTime(e.created_at)}</span>
                </li>
              ))}
            </ol>
            {moreEvents && <button onClick={() => void loadMoreEvents()}>加载更多</button>}
          </>
        )}
      </div>

      {(() => {
        const fo = (trace?.agent_runs ?? []).slice(-1)[0]?.final_output as
          { audit_levels?: Array<{level:string;name:string;passed:boolean;detail:string}>; audit_summary?: string } | undefined;
        const levels = fo?.audit_levels;
        if (!levels || levels.length === 0) return null;
        return (
          <div className="card">
            <h3>验收审计（四层）</h3>
            <p className="muted small-text">{fo?.audit_summary}</p>
            <table className="table">
              <thead><tr><th>层级</th><th>名称</th><th>结果</th><th>详情</th></tr></thead>
              <tbody>
                {levels.map((l, i) => (
                  <tr key={i}>
                    <td><b>{l.level}</b></td>
                    <td>{l.name}</td>
                    <td><span className={"badge " + (l.passed ? "green" : "red")}>{l.passed ? "通过" : "未过"}</span></td>
                    <td className="muted small-text" style={{maxWidth:300, overflow:"hidden", textOverflow:"ellipsis", whiteSpace:"nowrap"}}>{l.detail}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        );
      })()}

      <div className="card">
        <h3>失败诊断</h3>
        {reports === null && <span className="muted">—</span>}
        {reports?.length === 0 && (
          <EmptyState
            text={task.status === "FAILED" || task.status === "CANCELLED"
              ? "暂无报告（查询时已自动触发生成，稍后刷新）"
              : "任务未失败，无诊断报告"}
          />
        )}
        {reports?.map((r) => (
          <div key={r.id} className="diagnosis">
            <div className="kv" style={{ marginBottom: 8 }}>
              <span>故障码</span><b className={r.confidence === "LOW" ? "orange-text" : "red-text"}>{r.fault_code}</b>
              <span>置信度</span><b>{r.confidence}</b>
              <span>需人工</span><b>{r.need_human ? "是" : "否"}</b>
              <span>诊断器</span><b>{r.diagnoser_version}</b>
            </div>
            <p style={{ margin: "4px 0" }}><b>根因假设：</b>{r.root_cause_hypothesis}</p>
            <p style={{ margin: "4px 0" }}><b>证据：</b></p>
            <ul>
              {r.evidence?.map((ev, i) => (
                <li key={i} className="mono small-text">[{ev.type}] {ev.ref} — {ev.detail}</li>
              ))}
            </ul>
            <p style={{ margin: "4px 0" }}><b>建议：</b></p>
            <ul>{r.suggestions?.map((s, i) => <li key={i}>{s}</li>)}</ul>
          </div>
        ))}
      </div>

      <p><Link to="/tasks">← 返回任务列表</Link></p>
    </section>
  );
}
