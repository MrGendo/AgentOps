import { useCallback, useEffect, useState } from "react";
import { fetchHealth, type HealthResponse } from "./api";

// 阶段 A 骨架页：验证前端与真实 Go 后端的连通性；
// 任务列表/详情/时间线等页面按阶段 E 契约扩展。
export default function App() {
  const [health, setHealth] = useState<HealthResponse | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [loading, setLoading] = useState(false);

  const refresh = useCallback(async () => {
    setLoading(true);
    setError(null);
    try {
      setHealth(await fetchHealth());
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e));
    } finally {
      setLoading(false);
    }
  }, []);

  useEffect(() => {
    void refresh();
  }, [refresh]);

  return (
    <main style={{ fontFamily: "system-ui, sans-serif", maxWidth: 720, margin: "48px auto", padding: "0 16px" }}>
      <h1>AgentOps 控制台</h1>
      <p>智能体可靠执行、工具治理、评测与诊断平台（阶段 A 骨架）</p>

      <section style={{ marginTop: 32 }}>
        <h2>平台健康</h2>
        {error && <p style={{ color: "#c0392b" }}>请求失败：{error}</p>}
        {health && (
          <ul>
            <li>平台状态：<strong>{health.status}</strong></li>
            <li>PostgreSQL：<strong>{health.dependencies.postgres}</strong></li>
          </ul>
        )}
        <button onClick={() => void refresh()} disabled={loading}>
          {loading ? "检查中…" : "刷新"}
        </button>
      </section>
    </main>
  );
}
