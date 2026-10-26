#!/bin/bash
# AgentOps 全链路演示（需求 §19 完整闭环）：
# 提交任务 → Go 调度 → Python Agent 规划 → Go 工具网关执行 → 持久化 → 评测与诊断
# 自起自清，可重复运行。用法：bash scripts/demo.sh
set -e
ROOT=$(cd "$(dirname "$0")/.." && pwd)
cd "$ROOT"
STARTED=()

cleanup() {
  for pid in "${STARTED[@]:-}"; do kill "$pid" 2>/dev/null || true; done
}
trap cleanup EXIT

port_up() { curl -sf -m 2 "$1" >/dev/null 2>&1; }

# 服务自起（已在运行则复用）
if ! port_up http://localhost:8080/api/v1/health; then
  echo "[demo] 启动 Go 平台…"
  (cd platform && go run ./cmd/server >/tmp/demo_go.log 2>&1) & STARTED+=($!)
fi
if ! port_up http://localhost:8000/internal/v1/health; then
  echo "[demo] 启动 Python Agent Runtime…"
  (cd agent && .venv/bin/python3 -m uvicorn app.main:app --port 8000 >/tmp/demo_py.log 2>&1) & STARTED+=($!)
fi
for i in $(seq 1 30); do
  port_up http://localhost:8080/api/v1/health && port_up http://localhost:8000/internal/v1/health && break
  sleep 1
done

echo "=== ① 创建 Agent 诊断任务（§3.2 场景）==="
TID=$(curl -s -X POST localhost:8080/api/v1/tasks -H 'Content-Type: application/json' -d '{
  "task_type":"agent.ops_diagnosis",
  "input":{"service":"order-api","描述":"近期响应变慢，请诊断"},
  "allowed_tools":["knowledge_search","calculator","mock_http"],
  "timeout_seconds":60}' | python3 -c 'import json,sys;print(json.load(sys.stdin)["data"]["id"])')
echo "任务 ID: $TID"

echo "=== ② 等待执行（Mock 模型规划 4 步 + 网关工具调用）==="
for i in $(seq 1 30); do
  ST=$(curl -s localhost:8080/api/v1/tasks/$TID | python3 -c 'import json,sys;print(json.load(sys.stdin)["data"]["status"])')
  [ "$ST" = "SUCCEEDED" ] && break; sleep 1
done
echo "任务状态: $ST"
curl -s localhost:8080/api/v1/tasks/$TID/trace > /tmp/demo_trace.json
python3 - <<'PYEOF'
import json

d = json.load(open("/tmp/demo_trace.json"))["data"]
steps = [(s["name"], s["status"]) for s in d.get("steps") or []]
calls = [(c["tool_name"], c["status"]) for c in d.get("tool_calls") or []]
print("执行步骤:", steps)
print("工具调用:", calls)
runs = d.get("agent_runs") or []
if runs:
    r = runs[-1]
    tok = (r.get("token_usage") or {}).get("total_tokens") or 0
    print("Agent 运行: 迭代", r["iteration_count"], "次, 工具", r["tool_call_count"], "次, Token", tok)
PYEOF

echo "=== ③ 批量评测（demo-mixed：成功/重试/多工具/失败 各一）==="
DS_NAME="demo-$(date +%s)"
curl -s -X POST "localhost:8080/api/v1/datasets?name=$DS_NAME" -H 'Content-Type: application/x-ndjson' \
  --data-binary @agent/evaldata/demo-mixed.jsonl -o /dev/null
DS=$(curl -s localhost:8080/api/v1/datasets | python3 -c "import json,sys;print([d['id'] for d in json.load(sys.stdin)['data'] if d['name']=='$DS_NAME'][0])")
RID=$(curl -s -X POST localhost:8080/api/v1/evaluations -H 'Content-Type: application/json' \
  -d "{\"dataset_id\":\"$DS\",\"agent_version\":\"demo\"}" | python3 -c 'import json,sys;print(json.load(sys.stdin)["data"]["id"])')
for i in $(seq 1 60); do
  EST=$(curl -s localhost:8080/api/v1/evaluations/$RID | python3 -c 'import json,sys;print(json.load(sys.stdin)["data"]["status"])')
  [ "$EST" = "SUCCEEDED" ] && break; sleep 1
done
curl -s localhost:8080/api/v1/evaluations/$RID > /tmp/demo_eval.json
python3 - <<'PYEOF'
import json

d = json.load(open("/tmp/demo_eval.json"))["data"]
m = d.get("metrics") or {}
g = lambda k: (m.get(k) or {}).get("value")
print("评测状态:", d["status"], " 样例数:", m.get("case_count"))
print("成功率", g("task_success_rate"), " 正确率", g("result_correctness"),
      " 重试率", g("retry_rate"), " 工具调用准确率", g("tool_call_accuracy"))
print("（口径见 metrics 各项 caliber 字段；控制台评测页可展开全部指标）")
PYEOF

echo "=== ④ 失败样例诊断（引用真实证据）==="
FAILID=$(curl -s "localhost:8080/api/v1/tasks?status=FAILED&limit=5" | python3 -c '
import json, sys
for t in json.load(sys.stdin)["data"]:
    if t["error_code"] == "ARGUMENT_INVALID":
        print(t["id"]); break')
if [ -n "$FAILID" ]; then
  curl -s localhost:8080/api/v1/tasks/$FAILID/diagnosis > /tmp/demo_diag.json
  python3 - <<'PYEOF'
import json

r = json.load(open("/tmp/demo_diag.json"))["data"][0]
print("故障码:", r["fault_code"], " 置信度:", r["confidence"], " 需人工:", r["need_human"])
print("根因假设:", r["root_cause_hypothesis"])
print("证据:", ["[%s] %s" % (e["type"], str(e["ref"])[:8]) for e in r["evidence"][:3]])
PYEOF
fi

echo "=== ⑤ 观测基线（make metrics 可看全部）==="
bash scripts/metrics.sh 2>/dev/null | head -12
echo "[demo] 完成。控制台: cd console && npm run dev → http://localhost:5173"
