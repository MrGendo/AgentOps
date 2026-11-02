#!/bin/bash
# Cline CLI 黑盒 runner：AgentOps 适配器以子进程调用
# 参数：$1=workspace $2=problem $3=model $4=api_key $5=base_url $6=timeout
set -e
WS="$1"; PROBLEM="$2"; MODEL="$3"; KEY="$4"; BASE="$5"; TIMEOUT="$6"
export ANTHROPIC_API_KEY="$KEY"
export OPENAI_API_KEY="$KEY"
if [ -n "$BASE" ]; then
  export OPENAI_BASE_URL="$BASE"
  export ANTHROPIC_BASE_URL="$BASE"
fi
cd "$WS"
exec cline --yolo --cwd "$WS" "$PROBLEM"
