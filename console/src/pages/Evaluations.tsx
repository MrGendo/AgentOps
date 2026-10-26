import { useCallback, useEffect, useMemo, useState } from "react";
import { Link } from "react-router-dom";

import { api } from "../api";
import { durationText, formatDateTime, metricText } from "../format";
import type { Dataset, EvalRun, EvalRunDetail } from "../types";

// 核心四指标（§12.3：成功率/正确率/时长/重试率）
const CORE_METRICS: Array<{ key: string; label: string; kind: "rate" | "ms" }> = [
  { key: "task_success_rate", label: "任务成功率", kind: "rate" },
  { key: "result_correctness", label: "结果正确率", kind: "rate" },
  { key: "retry_rate", label: "重试率", kind: "rate" },
  { key: "avg_duration_ms", label: "平均时长", kind: "ms" },
];

function metricValue(run: EvalRun, key: string, kind: "rate" | "ms"): string {
  const m = run.metrics?.[key];
  if (!m) return "—";
  return kind === "rate" ? metricText(m.value) : durationText(m.value);
}

export default function Evaluations() {
  const [datasets, setDatasets] = useState<Dataset[]>([]);
  const [runs, setRuns] = useState<EvalRun[]>([]);
  const [datasetFilter, setDatasetFilter] = useState("");
  const [detail, setDetail] = useState<EvalRunDetail | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [toast, setToast] = useState<string | null>(null);

  const notify = (m: string) => {
    setToast(m);
    window.setTimeout(() => setToast(null), 3000);
  };

  const load = useCallback(async () => {
    try {
      const [ds, rs] = await Promise.all([
        api.listDatasets(),
        api.listEvaluations(datasetFilter ? { dataset_id: datasetFilter } : {}),
      ]);
      setDatasets(ds.data ?? []);
      setRuns(rs.data ?? []);
      setError(null);
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e));
    }
  }, [datasetFilter]);

  useEffect(() => {
    void load();
  }, [load]);

  // 版本对比：当前过滤下的运行按 agent_version 分组取最新
  const compare = useMemo(() => {
    const byVer = new Map<string, EvalRun>();
    for (const r of runs) {
      const prev = byVer.get(r.agent_version);
      if (!prev || prev.created_at < r.created_at) byVer.set(r.agent_version, r);
    }
    return [...byVer.values()].sort((a, b) => a.agent_version.localeCompare(b.agent_version));
  }, [runs]);

  const openDetail = async (id: string) => {
    const resp = await api.getEvaluation(id);
    setDetail(resp.data);
  };

  return (
    <section>
      <div className="toolbar"><h2>评测</h2></div>
      {error && <div className="banner red">加载失败：{error}</div>}
      {toast && <div className="banner blue">{toast}</div>}

      <div className="card">
        <h3>数据集（{datasets.length}）</h3>
        <table className="table">
          <thead><tr><th>名称</th><th>版本</th><th>样例数</th><th>描述</th><th>创建</th></tr></thead>
          <tbody>
            {datasets.map((d) => (
              <tr key={d.id}>
                <td>{d.name}</td><td>v{d.version}</td><td>{d.case_count}</td>
                <td>{d.description ?? "—"}</td><td>{formatDateTime(d.created_at)}</td>
              </tr>
            ))}
            {datasets.length === 0 && <tr><td colSpan={5} className="muted center">暂无数据集</td></tr>}
          </tbody>
        </table>
        <ImportForm onDone={(msg) => { notify(msg); void load(); }} />
      </div>

      <div className="card">
        <h3>发起评测</h3>
        <NewRunForm datasets={datasets} onDone={(msg) => { notify(msg); void load(); }} />
      </div>

      {compare.length >= 2 && (
        <div className="card">
          <h3>版本对比（每版本取最新一次运行）</h3>
          <table className="table">
            <thead>
              <tr>
                <th>Agent 版本</th>
                {CORE_METRICS.map((m) => <th key={m.key}>{m.label}</th>)}
              </tr>
            </thead>
            <tbody>
              {compare.map((r) => (
                <tr key={r.id}>
                  <td><b>{r.agent_version}</b></td>
                  {CORE_METRICS.map((m) => <td key={m.key}>{metricValue(r, m.key, m.kind)}</td>)}
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}

      <div className="card">
        <h3>评测运行（{runs.length}）</h3>
        <label>按数据集过滤
          <select value={datasetFilter} onChange={(e) => setDatasetFilter(e.target.value)}>
            <option value="">全部</option>
            {datasets.map((d) => <option key={d.id} value={d.id}>{d.name} v{d.version}</option>)}
          </select>
        </label>
        <table className="table">
          <thead>
            <tr>
              <th>运行</th><th>数据集</th><th>版本</th><th>状态</th>
              {CORE_METRICS.map((m) => <th key={m.key}>{m.label}</th>)}
              <th>时间</th>
            </tr>
          </thead>
          <tbody>
            {runs.map((r) => (
              <tr key={r.id} className="clickable" onClick={() => void openDetail(r.id)}>
                <td>{r.id.slice(0, 8)}</td>
                <td>{r.dataset_name} v{r.dataset_version}</td>
                <td>{r.agent_version}</td>
                <td>{r.status}</td>
                {CORE_METRICS.map((m) => <td key={m.key}>{metricValue(r, m.key, m.kind)}</td>)}
                <td>{formatDateTime(r.created_at)}</td>
              </tr>
            ))}
            {runs.length === 0 && <tr><td colSpan={9} className="muted center">暂无评测运行</td></tr>}
          </tbody>
        </table>
      </div>

      {detail && (
        <div className="card">
          <h3>运行详情 {detail.id.slice(0, 8)}（{detail.status}）</h3>
          <table className="table">
            <thead><tr><th>样例</th><th>状态</th><th>规则</th><th>耗时</th><th>任务</th></tr></thead>
            <tbody>
              {detail.results?.map((r) => (
                <tr key={r.case_seq}>
                  <td>#{r.case_seq}</td>
                  <td>{r.status}</td>
                  <td>
                    {(r.scores ?? []).map((s) => (
                      <span key={s.rule} className={s.pass ? "green-text" : "red-text"}>
                        {s.rule}:{s.pass ? "过" : "否"}{" "}
                      </span>
                    ))}
                  </td>
                  <td>{durationText(r.duration_ms)}</td>
                  <td>
                    {r.task_id
                      ? <Link to={`/tasks/${r.task_id}`}>{r.task_id.slice(0, 8)}</Link>
                      : "—"}
                  </td>
                </tr>
              ))}
              {(!detail.results || detail.results.length === 0) && (
                <tr><td colSpan={5} className="muted center">暂无样例结果</td></tr>
              )}
            </tbody>
          </table>
          {detail.metrics && (
            <details><summary>全部指标（含口径说明）</summary>
              <table className="table small">
                <thead><tr><th>指标</th><th>值</th><th>口径</th></tr></thead>
                <tbody>
                  {Object.entries(detail.metrics).map(([k, v]) => (
                    <tr key={k}>
                      <td>{k}</td>
                      <td>{typeof v?.value === "number"
                        ? (k.includes("rate") || k.includes("correctness") ? metricText(v.value) : durationText(v.value))
                        : JSON.stringify(v?.value ?? null)}</td>
                      <td className="muted">{(v as { caliber?: string })?.caliber ?? ""}</td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </details>
          )}
        </div>
      )}
    </section>
  );
}

function ImportForm(props: { onDone: (msg: string) => void }) {
  const [name, setName] = useState("");
  const [text, setText] = useState("");
  const [err, setErr] = useState<string | null>(null);

  const pickFile = async (f: File | undefined) => {
    if (f) setText(await f.text());
  };

  const submit = async () => {
    if (!name || !text.trim()) {
      setErr("需要数据集名与 JSONL 内容");
      return;
    }
    try {
      // 本地逐行校验，避免坏行打到后端
      for (const [i, line] of text.split("\n").entries()) {
        if (line.trim()) JSON.parse(line); // 抛错即拦截
        if (JSON.parse(line.trim() || "{}")?.input_task === undefined && line.trim()) {
          throw new Error(`第 ${i + 1} 行缺少 input_task`);
        }
      }
    } catch (e) {
      setErr(`JSONL 校验失败：${e instanceof Error ? e.message : e}`);
      return;
    }
    try {
      await api.importDataset(name, text);
      setErr(null);
      setText("");
      props.onDone(`数据集 ${name} 已导入`);
    } catch (e) {
      setErr(e instanceof Error ? e.message : String(e));
    }
  };

  return (
    <div className="import">
      <label>名称 <input value={name} onChange={(e) => setName(e.target.value)} placeholder="demo-mixed" /></label>
      <label>JSONL 文件 <input type="file" accept=".jsonl,.ndjson,.txt"
        onChange={(e) => void pickFile(e.target.files?.[0])} /></label>
      <label>或粘贴（每行一个样例）
        <textarea rows={3} value={text} onChange={(e) => setText(e.target.value)} /></label>
      {err && <div className="banner red">{err}</div>}
      <button className="primary" onClick={() => void submit()}>导入</button>
    </div>
  );
}

function NewRunForm(props: { datasets: Dataset[]; onDone: (msg: string) => void }) {
  const [datasetId, setDatasetId] = useState("");
  const [version, setVersion] = useState("v1");
  const [err, setErr] = useState<string | null>(null);

  const submit = async () => {
    if (!datasetId) {
      setErr("请选择数据集");
      return;
    }
    try {
      await api.createEvaluation({ dataset_id: datasetId, agent_version: version || "default" });
      setErr(null);
      props.onDone("评测已受理（异步执行，稍后刷新查看指标）");
    } catch (e) {
      setErr(e instanceof Error ? e.message : String(e));
    }
  };

  return (
    <div className="row">
      <label>数据集
        <select value={datasetId} onChange={(e) => setDatasetId(e.target.value)}>
          <option value="">请选择</option>
          {props.datasets.map((d) => (
            <option key={d.id} value={d.id}>{d.name} v{d.version}（{d.case_count} 例）</option>
          ))}
        </select>
      </label>
      <label>Agent 版本 <input value={version} onChange={(e) => setVersion(e.target.value)} /></label>
      <button className="primary" onClick={() => void submit()}>发起</button>
      {err && <div className="banner red">{err}</div>}
    </div>
  );
}
