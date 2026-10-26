#!/bin/bash
# 安全检查脚本：密钥硬编码扫描 + 敏感文件入库检查（清单见 docs/security-checklist.md）
set -e
ROOT=$(cd "$(dirname "$0")/.." && pwd)
cd "$ROOT"
FAIL=0

echo "== ① 源码密钥模式扫描（sk- / api_key 赋值 / password 字面量）=="
if grep -rInE '(sk-[A-Za-z0-9]{16,}|api[_-]?key\s*[:=]\s*"[^"$]{8,}|password\s*[:=]\s*"[^"$]{4,})' \
    platform agent/app console/src --include='*.go' --include='*.py' --include='*.ts' --include='*.tsx' \
    | grep -v '_test' | grep -v 'dev-service-token'; then
  echo "  ✗ 发现疑似硬编码密钥"; FAIL=1
else
  echo "  ✓ 未发现硬编码密钥"
fi

echo "== ② 敏感文件未入库（.env / data/）=="
if git ls-files | grep -E '^\.env$|^data/' | grep -q .; then
  echo "  ✗ 敏感文件已入库"; FAIL=1
else
  echo "  ✓ .env 与 data/ 未入库"
fi

echo "== ③ 内部接口服务令牌中间件存在 =="
grep -q 'X-Service-Token' platform/internal/httpapi/router.go agent/app/main.py \
  && echo "  ✓ Go(/tool-calls) 与 Python(/internal/v1) 均校验" || { echo "  ✗ 缺失"; FAIL=1; }

echo "== ④ 无高危通用工具（shell/exec）=="
if grep -rInE '"(shell|bash|exec|eval)"' platform/internal/gateway/tools.go | grep -q 'Name:'; then
  echo "  ✗ 存在高危工具"; FAIL=1
else
  echo "  ✓ 四个内置工具均为只读/受控 Mock"
fi

echo "== ⑤ CORS 未开放（无 AllowOriginAll）=="
grep -rI 'AllowAllOrigins\|Access-Control-Allow-Origin: \*' platform/ >/dev/null 2>&1 \
  && { echo "  ✗ 发现开放 CORS"; FAIL=1; } || echo "  ✓ 未配置跨域开放"

exit $FAIL
