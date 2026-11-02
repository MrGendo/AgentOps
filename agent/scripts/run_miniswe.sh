#!/bin/bash
# mini-SWE-agent 黑盒 runner（子进程模式，避免 SDK 生命周期冲突）
set -e
WS="$1"; PROBLEM="$2"; MODEL="$3"; KEY="$4"; BASE="$5"
export OPENAI_API_KEY="$KEY"
if [ -n "$BASE" ]; then export OPENAI_API_BASE="$BASE"; fi
cd "$WS"
exec python3 -m minisweagent run --model "$MODEL" -y "$PROBLEM" 2>&1 || true
