import { useCallback, useEffect, useRef, useState } from "react";
import { Link } from "react-router-dom";

import { api } from "../api";
import { PageHeader } from "../components/Layout";
import { CreateTaskDialog } from "../components/CreateTaskDialog";
import { StatusBadge } from "../components/StatusBadge";
import { EmptyState, ErrorBanner } from "../components/StateViews";
import { Icon } from "../icons";
import { canCancel, canRetry, durationText, formatDateTime, shortId } from "../format";
import type { Task, ToolInfo } from "../types";

function taskDuration(t: Task): number | null {
  if (!t.started_at || !t.finished_at) return null;
  return new Date(t.finished_at).getTime() - new Date(t.started_at).getTime();
}

export default function TaskList() {
  const [tasks, setTasks] = useState<Task[] | null>(null);
  const [meta, setMeta] = useState<{ next_cursor?: string | null; total?: number }>({});
  const [status, setStatus] = useState("");
  const [type, setType] = useState("");
  const [cursor, setCursor] = useState<string | undefined>(undefined);
  const [cursors, setCursors] = useState<(string | undefined)[]>([undefined]);
  const [error, setError] = useState<string | null>(null);
  const [toast, setToast] = useState<string | null>(null);
  const [auto, setAuto] = useState(true);
  const [dialogOpen, setDialogOpen] = useState(false);
  const [tools, setTools] = useState<ToolInfo[]>([]);
  const timer = useRef<number | null>(null);

  const showToast = (msg: string) => {
    setToast(msg);
    window.setTimeout(() => setToast(null), 3000);
  };
  const resetPage = () => {
    setCursors([undefined]);
    setCursor(undefined);
  };

  const load = useCallback(async () => {
    try {
      const resp = await api.listTasks({ status: status || undefined, task_type: type || undefined, cursor });
      setTasks(resp.data ?? []);
      setMeta(resp.meta ?? {});
      setError(null);
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e));
    }
  }, [status, type, cursor]);

  useEffect(() => { void load(); }, [load]);

  useEffect(() => {
    if (timer.current) window.clearInterval(timer.current);
    if (auto) timer.current = window.setInterval(() => void load(), 5000);
    return () => { if (timer.current) window.clearInterval(timer.current); };
  }, [auto, load]);

  useEffect(() => {
    void api.listTools().then((r) => setTools(r.data ?? [])).catch(() => undefined);
  }, []);

  const nextPage = () => {
    if (meta.next_cursor) {
      setCursors((cs) => [...cs, meta.next_cursor!]);
      setCursor(meta.next_cursor);
    }
  };
  const prevPage = () => {
    setCursors((cs) => {
      if (cs.length <= 1) return cs;
      const next = cs.slice(0, -1);
      setCursor(next[next.length - 1]);
      return next;
    });
  };

  const cancel = async (id: string) => {
    try { await api.cancelTask(id); showToast("取消请求已受理"); void load(); }
    catch (e) { showToast(`取消失败：${e instanceof Error ? e.message : e}`); }
  };
  const retry = async (id: string) => {
    try { await api.retryTask(id); showToast("重试已受理，任务回到等待调度"); void load(); }
    catch (e) { showToast(`重试失败：${e instanceof Error ? e.message : e}`); }
  };

  return (
    <section>
      <PageHeader
        title="任务管理"
        desc="创建、筛选、取消与重试任务"
        actions={
          <>
            <button onClick={() => void load()}><Icon name="refresh" size={14} />刷新</button>
            <button className="primary" onClick={() => setDialogOpen(true)}>
              <Icon name="plus" size={14} />创建任务
            </button>
          </>
        }
      />
      {toast && <div className="banner blue">{toast}</div>}
      {error && <ErrorBanner message={`加载失败：${error}`} />}

      <div className="tabs" role="tablist">
        {[
          { v: "", label: "全部" }, { v: "PENDING", label: "等待" },
          { v: "RUNNING", label: "执行中" }, { v: "SUCCEEDED", label: "成功" },
          { v: "FAILED", label: "失败" },
        ].map((t) => (
          <button key={t.v} role="tab" aria-selected={status === t.v}
                  className={"tab" + (status === t.v ? " on" : "")}
                  onClick={() => { setStatus(t.v); resetPage(); }}>
            {t.label}{t.v === "" && <span className="muted"> {meta.total ?? ""}</span>}
          </button>
        ))}
      </div>

      <div className="filters">
        <label>状态（含低频）
          <select value={status} onChange={(e) => { setStatus(e.target.value); resetPage(); }}>
            <option value="">全部</option>
            {["PENDING", "RUNNING", "CANCEL_REQUESTED", "SUCCEEDED", "FAILED", "CANCELLED"].map((s) => (
              <option key={s} value={s}>{s}</option>
            ))}
          </select>
        </label>
        <label>类型 / 关键字
          <input value={type} placeholder="如 mock.echo 或 agent" onChange={(e) => { setType(e.target.value); resetPage(); }} />
        </label>
        <label className="check">
          <input type="checkbox" checked={auto} onChange={(e) => setAuto(e.target.checked)} /> 自动刷新（5s）
        </label>
      </div>

      <div className="card" style={{ padding: 0, overflow: "hidden" }}>
        {tasks === null ? <div style={{ padding: 8 }}><EmptyState text="加载中…" /></div>
          : tasks.length === 0 ? <EmptyState text="暂无任务" hint="点击右上角「创建任务」开始" />
          : (
        <table className="table">
          <thead>
            <tr>
              <th>ID</th><th>类型</th><th>状态</th><th>尝试</th><th>耗时</th><th>错误</th><th>创建时间</th><th>操作</th>
            </tr>
          </thead>
          <tbody>
            {tasks.map((t) => (
              <tr key={t.id}>
                <td className="mono"><Link to={`/tasks/${t.id}`}>{shortId(t.id)}</Link></td>
                <td>{t.task_type}</td>
                <td><StatusBadge status={t.status} /></td>
                <td>{t.attempt_count}{t.attempt_count > 1 && <span className="muted">（重试过）</span>}</td>
                <td>{durationText(taskDuration(t))}</td>
                <td className="ellipsis" title={t.error_message ?? ""}>{t.error_code ?? "—"}</td>
                <td className="muted">{formatDateTime(t.created_at)}</td>
                <td>
                  <span style={{ display: "inline-flex", gap: 6 }}>
                    {canCancel(t.status) && <button onClick={() => void cancel(t.id)}>取消</button>}
                    {canRetry(t.status) && <button onClick={() => void retry(t.id)}>重试</button>}
                  </span>
                </td>
              </tr>
            ))}
          </tbody>
        </table>
          )}
      </div>

      <div className="pager">
        <button disabled={cursors.length <= 1} onClick={prevPage}>上一页</button>
        <span className="muted small-text">第 {cursors.length} 页 · 共 {meta.total ?? "—"} 条</span>
        <button disabled={!meta.next_cursor} onClick={nextPage}>下一页</button>
      </div>

      {dialogOpen && (
        <CreateTaskDialog tools={tools} onClose={() => setDialogOpen(false)}
          onCreated={(msg) => { setDialogOpen(false); showToast(msg); resetPage(); void load(); }} />
      )}
    </section>
  );
}
