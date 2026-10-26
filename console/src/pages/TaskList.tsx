import { useCallback, useEffect, useRef, useState } from "react";
import { Link } from "react-router-dom";

import { api, type CreateTaskPayload } from "../api";
import {
  canCancel, canRetry, durationText, formatDateTime, shortId, statusBadge,
} from "../format";
import type { Task, ToolInfo } from "../types";

// 任务类型与创建模板（与平台内置执行器一致）
const TYPE_TEMPLATES: Record<string, { input: string; params: string; tools: string[] }> = {
  "mock.echo": { input: '{"msg":"hello"}', params: "{}", tools: [] },
  "mock.fail": { input: '{"goal":"重试演示"}', params: '{"fail_times":1,"error_class":"TRANSIENT_NETWORK"}', tools: [] },
  "mock.sleep": { input: '{"goal":"超时演示"}', params: '{"seconds":2}', tools: [] },
  "mock.tools": { input: '{"goal":"全链路演示"}', params: "{}", tools: ["calculator", "knowledge_search", "task_status", "mock_http"] },
  "agent.ops_diagnosis": { input: '{"service":"order-api","描述":"近期响应变慢，请诊断"}', params: "{}", tools: ["knowledge_search", "calculator", "mock_http"] },
};

function taskDuration(t: Task): number | null {
  if (!t.started_at || !t.finished_at) return null;
  return new Date(t.finished_at).getTime() - new Date(t.started_at).getTime();
}

export default function TaskList() {
  const [tasks, setTasks] = useState<Task[]>([]);
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

  useEffect(() => {
    void load();
  }, [load]);

  // 自动刷新：仅当前页轮询
  useEffect(() => {
    if (timer.current) window.clearInterval(timer.current);
    if (auto) {
      timer.current = window.setInterval(() => void load(), 5000);
    }
    return () => {
      if (timer.current) window.clearInterval(timer.current);
    };
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
    try {
      await api.cancelTask(id);
      showToast("取消请求已受理");
      void load();
    } catch (e) {
      showToast(`取消失败：${e instanceof Error ? e.message : e}`);
    }
  };
  const retry = async (id: string) => {
    try {
      await api.retryTask(id);
      showToast("重试已受理，任务回到等待调度");
      void load();
    } catch (e) {
      showToast(`重试失败：${e instanceof Error ? e.message : e}`);
    }
  };

  const pageIdx = cursors.length - 1;
  const hasPrev = pageIdx > 0;

  return (
    <section>
      <div className="toolbar">
        <h2>任务</h2>
        <button className="primary" onClick={() => setDialogOpen(true)}>创建任务</button>
      </div>

      <div className="filters">
        <label>状态
          <select value={status} onChange={(e) => { setStatus(e.target.value); resetPage(); }}>
            <option value="">全部</option>
            {["PENDING", "RUNNING", "CANCEL_REQUESTED", "SUCCEEDED", "FAILED", "CANCELLED"].map((s) => (
              <option key={s} value={s}>{s}</option>
            ))}
          </select>
        </label>
        <label>类型
          <input value={type} placeholder="如 mock.echo" onChange={(e) => { setType(e.target.value); resetPage(); }} />
        </label>
        <label className="check">
          <input type="checkbox" checked={auto} onChange={(e) => setAuto(e.target.checked)} /> 自动刷新（5s）
        </label>
        <span className="muted">共 {meta.total ?? "—"} 条</span>
      </div>

      {error && <div className="banner red">加载失败：{error}</div>}
      {toast && <div className="banner blue">{toast}</div>}

      <table className="table">
        <thead>
          <tr>
            <th>ID</th><th>类型</th><th>状态</th><th>尝试</th><th>耗时</th><th>错误</th><th>创建时间</th><th>操作</th>
          </tr>
        </thead>
        <tbody>
          {tasks.map((t) => {
            const badge = statusBadge(t.status);
            return (
              <tr key={t.id}>
                <td><Link to={`/tasks/${t.id}`}>{shortId(t.id)}</Link></td>
                <td>{t.task_type}</td>
                <td><span className={badge.className}>{badge.label}</span></td>
                <td>{t.attempt_count}{t.attempt_count > 1 && <span className="muted">（重试过）</span>}</td>
                <td>{durationText(taskDuration(t))}</td>
                <td className="ellipsis" title={t.error_message ?? ""}>{t.error_code ?? "—"}</td>
                <td>{formatDateTime(t.created_at)}</td>
                <td className="actions">
                  {canCancel(t.status) && <button onClick={() => void cancel(t.id)}>取消</button>}
                  {canRetry(t.status) && <button onClick={() => void retry(t.id)}>重试</button>}
                </td>
              </tr>
            );
          })}
          {tasks.length === 0 && (
            <tr><td colSpan={8} className="muted center">暂无任务（可在上方创建）</td></tr>
          )}
        </tbody>
      </table>

      <div className="pager">
        <button disabled={!hasPrev} onClick={prevPage}>上一页</button>
        <span className="muted">第 {pageIdx + 1} 页</span>
        <button disabled={!meta.next_cursor} onClick={nextPage}>下一页</button>
      </div>

      {dialogOpen && (
        <CreateDialog
          tools={tools}
          onClose={() => setDialogOpen(false)}
          onCreated={(msg) => { setDialogOpen(false); showToast(msg); resetPage(); void load(); }}
        />
      )}
    </section>
  );

  function resetPage() {
    setCursors([undefined]);
    setCursor(undefined);
  }
}

function CreateDialog(props: { tools: ToolInfo[]; onClose: () => void; onCreated: (msg: string) => void }) {
  const [taskType, setTaskType] = useState("mock.echo");
  const [input, setInput] = useState(TYPE_TEMPLATES["mock.echo"].input);
  const [params, setParams] = useState(TYPE_TEMPLATES["mock.echo"].params);
  const [selected, setSelected] = useState<string[]>(TYPE_TEMPLATES["mock.echo"].tools);
  const [timeoutSec, setTimeoutSec] = useState(60);
  const [maxAttempts, setMaxAttempts] = useState(3);
  const [err, setErr] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);

  const pickType = (t: string) => {
    setTaskType(t);
    setInput(TYPE_TEMPLATES[t].input);
    setParams(TYPE_TEMPLATES[t].params);
    setSelected(TYPE_TEMPLATES[t].tools);
  };

  const submit = async () => {
    let inputObj: Record<string, unknown>;
    let paramsObj: Record<string, unknown>;
    try {
      inputObj = JSON.parse(input);
      paramsObj = JSON.parse(params || "{}");
    } catch {
      setErr("input / params 不是合法 JSON");
      return;
    }
    const payload: CreateTaskPayload = {
      task_type: taskType, input: inputObj, params: paramsObj,
      allowed_tools: selected, timeout_seconds: timeoutSec, max_attempts: maxAttempts,
    };
    setBusy(true);
    setErr(null);
    try {
      const resp = await api.createTask(payload);
      props.onCreated(`任务已创建：${resp.data.id.slice(0, 8)}`);
    } catch (e) {
      setErr(e instanceof Error ? e.message : String(e));
    } finally {
      setBusy(false);
    }
  };

  return (
    <div className="dialog-mask" onClick={props.onClose}>
      <div className="dialog" onClick={(e) => e.stopPropagation()}>
        <h3>创建任务</h3>
        <label>任务类型
          <select value={taskType} onChange={(e) => pickType(e.target.value)}>
            {Object.keys(TYPE_TEMPLATES).map((t) => <option key={t}>{t}</option>)}
          </select>
        </label>
        <label>输入 input（JSON）
          <textarea rows={3} value={input} onChange={(e) => setInput(e.target.value)} />
        </label>
        <label>参数 params（JSON，可空）
          <textarea rows={2} value={params} onChange={(e) => setParams(e.target.value)} />
        </label>
        <fieldset>
          <legend>授权工具（{props.tools.length} 个可用）</legend>
          {props.tools.map((t) => (
            <label key={t.name} className="check">
              <input
                type="checkbox"
                checked={selected.includes(t.name)}
                onChange={(e) =>
                  setSelected((s) => e.target.checked ? [...s, t.name] : s.filter((n) => n !== t.name))
                }
              /> {t.name}（{t.risk_level}）
            </label>
          ))}
        </fieldset>
        <div className="row">
          <label>超时（秒）
            <input type="number" value={timeoutSec} min={1}
                   onChange={(e) => setTimeoutSec(Number(e.target.value) || 60)} />
          </label>
          <label>最大尝试
            <input type="number" value={maxAttempts} min={1}
                   onChange={(e) => setMaxAttempts(Number(e.target.value) || 3)} />
          </label>
        </div>
        {err && <div className="banner red">{err}</div>}
        <div className="row end">
          <button onClick={props.onClose}>取消</button>
          <button className="primary" disabled={busy} onClick={() => void submit()}>
            {busy ? "提交中…" : "创建"}
          </button>
        </div>
      </div>
    </div>
  );
}
