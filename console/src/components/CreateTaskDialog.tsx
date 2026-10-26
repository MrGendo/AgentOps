import { useState } from "react";

import { api, type CreateTaskPayload } from "../api";
import { Icon } from "../icons";
import type { ToolInfo } from "../types";

// 任务类型模板（与平台内置执行器一致）
export const TYPE_TEMPLATES: Record<string, { input: string; params: string; tools: string[] }> = {
  "mock.echo": { input: '{"msg":"hello"}', params: "{}", tools: [] },
  "mock.fail": { input: '{"goal":"重试演示"}', params: '{"fail_times":1,"error_class":"TRANSIENT_NETWORK"}', tools: [] },
  "mock.sleep": { input: '{"goal":"超时演示"}', params: '{"seconds":2}', tools: [] },
  "mock.tools": { input: '{"goal":"全链路演示"}', params: "{}", tools: ["calculator", "knowledge_search", "task_status", "mock_http"] },
  "agent.ops_diagnosis": { input: '{"service":"order-api","描述":"近期响应变慢，请诊断"}', params: "{}", tools: ["knowledge_search", "calculator", "mock_http"] },
};

export function CreateTaskDialog(props: {
  tools: ToolInfo[];
  onClose: () => void;
  onCreated: (msg: string) => void;
}) {
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
      <div className="dialog" onClick={(e) => e.stopPropagation()} role="dialog" aria-label="创建任务">
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
        {err && <div className="banner red"><Icon name="warn" size={14} />{err}</div>}
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
