#!/usr/bin/env python3
"""OpenHands 独立运行器（由 AgentOps 适配器以子进程方式调用）。

本脚本独占工作区——AgentOps 主进程不介入 SDK 内部生命周期。
脚本退出后，AgentOps 检查工作区的 git diff 并执行四层审计。
"""
import argparse
import os
import sys

def main():
    p = argparse.ArgumentParser()
    p.add_argument("--workspace", required=True)
    p.add_argument("--problem", required=True)
    p.add_argument("--model", required=True)
    p.add_argument("--api-key", required=True)
    p.add_argument("--base-url", default="")
    p.add_argument("--timeout", type=int, default=600)
    args = p.parse_args()

    os.environ["OPENHANDS_SUPPRESS_BANNER"] = "1"

    from openhands.sdk import LLM, Agent, Conversation, Tool
    import openhands.tools.terminal  # noqa: F401 注册工具
    import openhands.tools.file_editor  # noqa: F401

    llm_kwargs = {"model": args.model, "api_key": args.api_key}
    if args.base_url:
        llm_kwargs["base_url"] = args.base_url
    llm = LLM(**llm_kwargs)
    agent = Agent(llm=llm, tools=[
        Tool(name="terminal", params={}),
        Tool(name="file_editor", params={}),
    ])

    # workspace 由本进程独占管理，SDK 随便怎么折腾
    conv = Conversation(agent=agent, workspace=args.workspace)
    conv.send_message(args.problem + "\n\n## 环境提示\n- pytest 已可用，直接 python -m pytest -q\n- 不要 pip install\n")
    conv.run()
    print("AGENT_DONE", file=sys.stderr)

if __name__ == "__main__":
    main()
