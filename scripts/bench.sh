#!/bin/bash
# 并发压测（测量脚本，非 SLA）：并发提交 N 个 mock.echo 任务并等待完成，
# 统计吞吐与任务耗时分布（P50/P95/P99 来自真实 tasks 表）+ API 延迟。
# 用法：bash scripts/bench.sh [任务数=200] [并发度=20]
set -e
ROOT=$(cd "$(dirname "$0")/.." && pwd)
cd "$ROOT"
N=${1:-200}; C=${2:-20}
DSN="postgres://localhost:5432/agentops_dev?sslmode=disable"
STARTED=()
cleanup() { for p in "${STARTED[@]:-}"; do kill "$p" 2>/dev/null || true; done; }
trap cleanup EXIT
if ! curl -sf -m 2 localhost:8080/api/v1/health >/dev/null; then
  (cd platform && go run ./cmd/server >/tmp/bench_go.log 2>&1) & STARTED+=($!)
  for i in $(seq 1 30); do curl -sf -m 2 localhost:8080/api/v1/health >/dev/null && break; sleep 1; done
fi

echo "[bench] 提交 $N 个任务（并发 $C）…"
T0=$(python3 -c 'import time;print(time.time())')
seq "$N" | xargs -P "$C" -I{} curl -s -o /dev/null -X POST localhost:8080/api/v1/tasks \
  -H 'Content-Type: application/json' \
  -d '{"task_type":"mock.echo","input":{"n":{}},"timeout_seconds":30}'
T1=$(python3 -c 'import time;print(time.time())')

echo "[bench] 等待全部完成…"
for i in $(seq 1 120); do
  DONE=$(psql "$DSN" -tA -c "SELECT count(*) FROM tasks WHERE status='SUCCEEDED' AND created_at > now() - interval '10 minutes'")
  [ "$DONE" -ge "$N" ] && break; sleep 1
done
T2=$(python3 -c 'import time;print(time.time())')

echo "[bench] 提交吞吐: $(python3 -c "print(f'{$N/($T1-$T0):.0f} tasks/s')")"
echo "[bench] 全部完成耗时: $(python3 -c "print(f'{$T2-$T0:.1f}s')")（含 Worker 消费）"
echo "[bench] 任务端到端耗时分布（本批真实数据）："
psql "$DSN" -c "
SELECT
  percentile_cont(0.5) WITHIN GROUP (ORDER BY EXTRACT(EPOCH FROM (finished_at-started_at))*1000) AS p50_ms,
  percentile_cont(0.95) WITHIN GROUP (ORDER BY EXTRACT(EPOCH FROM (finished_at-started_at))*1000) AS p95_ms,
  percentile_cont(0.99) WITHIN GROUP (ORDER BY EXTRACT(EPOCH FROM (finished_at-started_at))*1000) AS p99_ms,
  round(avg(EXTRACT(EPOCH FROM (finished_at-started_at))*1000)) AS avg_ms,
  count(*) AS n
FROM tasks WHERE status='SUCCEEDED' AND created_at > now() - interval '10 minutes'"

echo "[bench] API 延迟（各 100 次）："
TID=$(psql "$DSN" -tA -c "SELECT id FROM tasks WHERE status='SUCCEEDED' ORDER BY created_at DESC LIMIT 1")
for path in "/api/v1/tasks?limit=20" "/api/v1/tasks/$TID"; do
  {
    for i in $(seq 1 100); do curl -s -o /dev/null -w '%{time_total}\n' "localhost:8080$path"; done
  } | python3 -c "
import sys
v = sorted(float(x) for x in sys.stdin)
print(f'  GET $path → P50 {v[49]*1000:.1f}ms  P95 {v[94]*1000:.1f}ms  max {v[-1]*1000:.1f}ms')"
done
