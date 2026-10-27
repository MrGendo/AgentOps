"""独立验收测试：以当前解释器执行 pytest（白名单形态，免 shell 拼接）。

判定以退出码为准（0=通过），不信 Agent 自述。
"""

import re
import subprocess
import sys
from pathlib import Path

_ALLOWED = re.compile(r"^(?:python3? -m )?pytest(.*)$")
TAIL_LINES = 40


class UnsafeTestCommand(Exception):
    """验收命令不在白名单内"""


def run_acceptance(ws: Path, test_cmd: str, timeout: int = 120) -> dict:
    m = _ALLOWED.match(test_cmd.strip())
    if not m:
        raise UnsafeTestCommand(f"验收命令必须是 pytest 形态，拒绝: {test_cmd!r}")
    args = m.group(1).split()
    proc = subprocess.run([sys.executable, "-m", "pytest", *args], cwd=ws,
                          capture_output=True, text=True, timeout=timeout)
    tail = "\n".join((proc.stdout + proc.stderr).splitlines()[-TAIL_LINES:])
    return {"passed": proc.returncode == 0, "exit_code": proc.returncode,
            "output_tail": tail, "command": test_cmd}
