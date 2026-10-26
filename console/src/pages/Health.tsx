import { useCallback, useEffect, useState } from "react";

import { api } from "../api";
import { PageHeader } from "../components/Layout";
import { ErrorBanner } from "../components/StateViews";

// 平台健康页：Go 与 PG 状态实时展示。
export default function Health() {
  const [health, setHealth] = useState<Awaited<ReturnType<typeof api.health>> | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [loading, setLoading] = useState(false);

  const refresh = useCallback(async () => {
    setLoading(true);
    setError(null);
    try {
      setHealth(await api.health());
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
    <section>
      <PageHeader title="平台健康" desc="Go 平台与依赖状态" />
      {error && <ErrorBanner message={`请求失败：${error}`} />}
      {health && (
        <div className="cards">
          <div className="card">
            <h3>Go 平台</h3>
            <p>状态：<strong>{health.status}</strong></p>
          </div>
          <div className="card">
            <h3>PostgreSQL</h3>
            <p>状态：<strong>{health.dependencies.postgres}</strong></p>
          </div>
        </div>
      )}
      <button onClick={() => void refresh()} disabled={loading}>
        {loading ? "检查中…" : "刷新"}
      </button>
    </section>
  );
}
