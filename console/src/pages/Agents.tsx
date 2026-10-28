import { useCallback, useEffect, useState } from "react";

import { listAgents, type AgentInfo } from "../api";
import { PageHeader } from "../components/Layout";
import { EmptyState, ErrorBanner, Loading } from "../components/StateViews";
import { formatDateTime, metricText } from "../format";

// Agent 管理页：已注册 / 可运行 / 已测试三态分离展示。
export default function Agents() {
  const [agents, setAgents] = useState<AgentInfo[] | null>(null);
  const [error, setError] = useState<string | null>(null);

  const load = useCallback(async () => {
    try {
      const r = await listAgents();
      setAgents(r.data ?? []);
      setError(null);
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e));
    }
  }, []);

  useEffect(() => { void load(); }, [load]);

  return (
    <section>
      <PageHeader title="Agent 引擎" desc="注册信息 · 配置状态 · 真实运行统计（三态分离）"
        actions={<button onClick={() => void load()}>刷新</button>} />
      {error && <ErrorBanner message={`加载失败：${error}`} />}
      {agents === null ? <Loading /> : agents.length === 0 ? <EmptyState text="无已注册 Agent" /> : (
        <div className="stat-grid">
          {agents.map((a) => (
            <div className="card" key={a.agent_id} style={{ margin: 0 }}>
              <div className="row" style={{ justifyContent: "space-between", marginBottom: 8 }}>
                <h3 style={{ margin: 0 }}>{a.display_name}</h3>
                <span className={"badge " + (a.available ? "green" : "gray")}>
                  {a.available ? "可运行" : "不可用"}
                </span>
              </div>
              <div className="kv" style={{ gridTemplateColumns: "96px 1fr" }}>
                <span>标识</span><b className="mono">{a.agent_id}</b>
                <span>版本</span><b>{a.version}</b>
                <span>配置</span><b>{a.configuration_status === "complete" || a.configuration_status === "mock" ? "完整" : "不完整"}</b>
                <span>说明</span><b className="muted">{a.configuration_reason}</b>
                <span>已测试</span><b>{a.total_runs > 0 ? `是（${a.total_runs} 次）` : "否"}</b>
                <span>成功率</span><b>{a.success_rate === null || a.success_rate === undefined ? "—" : metricText(a.success_rate)}</b>
                <span>最近运行</span><b>{a.last_run_status ? `${a.last_run_status} · ${formatDateTime(a.last_used_at)}` : "—"}</b>
              </div>
            </div>
          ))}
        </div>
      )}
    </section>
  );
}
