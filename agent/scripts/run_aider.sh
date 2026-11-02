#!/bin/bash
# Aider CLI 黑盒 runner：AgentOps 适配器以子进程调用
# 参数：$1=workspace $2=problem $3=model $4=api_key $5=base_url $6=timeout
set -e
WS="$1"; PROBLEM="$2"; MODEL="$3"; KEY="$4"; BASE="$5"
export OPENAI_API_KEY="$KEY"
if [ -n "$BASE" ]; then
  export OPENAI_API_BASE="$BASE"
fi
AIDER=$(command -v aider || echo "")
[ -z "$AIDER" ] && { echo "ERROR: aider CLI not found" >&2; exit 127; }
cd "$WS"
exec "$AIDER" --model "$MODEL" --message "$PROBLEM" --yes-always --no-git --no-auto-commits 2>&1
