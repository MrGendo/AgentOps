#!/bin/bash
# 模型选型对比演示：同一 swe-local 数据集，两个 agent_version 各跑一轮评测。
# 前置：.env 已配置可用模型；Go(8080) + Python(8000) 已启动。
set -e
DS_NAME=swe-local
DS=$(curl -s localhost:8080/api/v1/datasets | python3 -c "import json,sys;print(next((d['id'] for d in json.load(sys.stdin)['data'] if d['name']=='$DS_NAME'), ''))")
if [ -z "$DS" ]; then
  curl -s -X POST "localhost:8080/api/v1/datasets?name=$DS_NAME&version=1" \
    -H 'Content-Type: application/x-ndjson' --data-binary @/tmp/swe-local.jsonl -o /dev/null
  DS=$(curl -s localhost:8080/api/v1/datasets | python3 -c "import json,sys;print(next((d['id'] for d in json.load(sys.stdin)['data'] if d['name']=='$DS_NAME'), ''))")
fi
for V in "$@"; do
  RID=$(curl -s -X POST localhost:8080/api/v1/evaluations -H 'Content-Type: application/json' \
    -d "{\"dataset_id\":\"$DS\",\"agent_version\":\"$V\",\"run_config\":{\"case_timeout_seconds\":480}}" \
    | python3 -c 'import json,sys;print(json.load(sys.stdin)["data"]["id"])')
  echo "[compare] $V → $RID"
  for i in $(seq 1 450); do
    ST=$(curl -s localhost:8080/api/v1/evaluations/$RID | python3 -c 'import json,sys;print(json.load(sys.stdin)["data"]["status"])')
    [ "$ST" = "SUCCEEDED" ] || [ "$ST" = "FAILED" ] && break; sleep 2
  done
  echo "[compare] $V 终态: $ST —— 指标见控制台评测页（数据集 $DS_NAME 版本对比）"
done
