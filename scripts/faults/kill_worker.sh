#!/bin/bash
# Worker 崩溃恢复复现（对应自动化测试 R2/R3）：
# server 角色不跑 Worker；独立 Worker 领取长任务后被 kill -9；
# 短租约下 reaper 回收任务，重启的 Worker 接管并完成。
set -e
ROOT=$(cd "$(dirname "$0")/../.." && pwd)
cd "$ROOT"
PIDS=()
cleanup() { for p in "${PIDS[@]:-}"; do kill -9 "$p" 2>/dev/null || true; done; }
trap cleanup EXIT

echo "[fault] 以 server 角色启动平台（无 Worker）…"
(cd platform && AGENTOPS_ROLE=server AGENTOPS_LEASE_TTL_SECONDS=3 \
  go run ./cmd/server >/tmp/fault_go.log 2>&1) & PIDS+=($!)
for i in $(seq 1 30); do curl -sf -m 2 localhost:8080/api/v1/health >/dev/null && break; sleep 1; done

echo "[fault] 提交 30s 长任务…"
TID=$(curl -s -X POST localhost:8080/api/v1/tasks -H 'Content-Type: application/json' \
  -d '{"task_type":"mock.sleep","input":{"goal":"fault-demo"},"params":{"seconds":30},"timeout_seconds":60}' \
  | python3 -c 'import json,sys;print(json.load(sys.stdin)["data"]["id"])')

echo "[fault] 启动独立 Worker（租约 3s）…"
(cd platform && AGENTOPS_ROLE=worker AGENTOPS_LEASE_TTL_SECONDS=3 AGENTOPS_PORT=8081 \
  go run ./cmd/server >/tmp/fault_worker.log 2>&1) & PIDS+=($!)
sleep 3
WKILL=$(pgrep -f "AGENTOPS_ROLE=worker" | head -1)
echo "[fault] kill -9 Worker（pid=$WKILL）…"
pkill -9 -f "exe/server" 2>/dev/null || true   # go run 的子进程即 Worker

echo "[fault] 等待租约过期回收 + 重启 Worker 接管…"
(cd platform && AGENTOPS_ROLE=worker AGENTOPS_LEASE_TTL_SECONDS=3 AGENTOPS_PORT=8081 \
  go run ./cmd/server >/tmp/fault_worker2.log 2>&1) & PIDS+=($!)
for i in $(seq 1 40); do
  ST=$(curl -s localhost:8080/api/v1/tasks/$TID | python3 -c 'import json,sys;print(json.load(sys.stdin)["data"]["status"])')
  [ "$ST" = "SUCCEEDED" ] && break; sleep 1
done
echo "[fault] 任务终态: $ST（应为 SUCCEEDED：崩溃→回收→接管→完成）"
echo "[fault] 恢复证据："
psql "$(grep AGENTOPS_DATABASE_URL .env 2>/dev/null | cut -d= -f2- || echo postgres://localhost:5432/agentops_dev?sslmode=disable)" \
  -c "SELECT event_type, created_at FROM task_events WHERE task_id='$TID' AND event_type IN ('task.claimed','lease.revoked','task.retry_scheduled') ORDER BY seq"
