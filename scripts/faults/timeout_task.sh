#!/bin/bash
# TIMEOUT_UNKNOWN 演示（对应 R7）：外部调用超时后结果未知，
# 网关落 TIMEOUT_UNKNOWN 审计、任务不自动重试。
set -e
TID=$(curl -s -X POST localhost:8080/api/v1/tasks -H 'Content-Type: application/json' \
  -d '{"task_type":"mock.echo","input":{"goal":"timeout-demo"},"allowed_tools":["mock_http"],"timeout_seconds":30}' \
  | python3 -c 'import json,sys;print(json.load(sys.stdin)["data"]["id"])')
echo "[fault] 直接经网关对任务 $TID 调 mock_http timeout 模式…"
curl -s -X POST localhost:8080/api/v1/tool-calls \
  -H 'Content-Type: application/json' -H 'X-Service-Token: dev-service-token' \
  -d "{\"task_id\":\"$TID\",\"tool_name\":\"mock_http\",\"params\":{\"mode\":\"timeout\"}}" \
  | python3 -c 'import json,sys;d=json.load(sys.stdin)["data"];print("工具状态:", d["status"], "错误码:", d.get("error_code"))'
echo "[fault] 审计与事件："
psql postgres://localhost:5432/agentops_dev?sslmode=disable -c \
  "SELECT tool_name, status FROM tool_calls WHERE task_id='$TID'; SELECT event_type FROM task_events WHERE task_id='$TID' AND event_type LIKE 'tool.call%' ORDER BY seq;"
echo "[fault] 结论：TIMEOUT_UNKNOWN 只留痕不重试（§6.5），由人工/下游确认流程处理。"
