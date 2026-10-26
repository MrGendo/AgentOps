# AgentOps 安全检查清单（阶段 F）
- 日期：2026-10-26；扫描脚本：`bash scripts/security-scan.sh`（本次实跑结果：5/5 通过）

| # | 检查项 | 结果 | 证据 |
|---|---|---|---|
| 1 | 密钥不硬编码 | ✓ | 扫描脚本 ①：sk-/api_key/password 模式零命中；密钥仅 `AGENTOPS_MODEL_API_KEY` 环境变量（config.py 只读 env） |
| 2 | 敏感文件不入库 | ✓ | 扫描脚本 ②：`.env`、`data/` 均在 .gitignore 且 `git ls-files` 无命中 |
| 3 | 内部接口服务令牌 | ✓ | 扫描脚本 ③：Go `/api/v1/tool-calls`（middleware.go serviceToken）与 Python `/internal/v1/*`（main.py require_token）均强制校验；集成测试：无令牌 401（HTTP 冒烟实测）、令牌错误 401（test_api） |
| 4 | 工具权限独立校验 | ✓ | 网关对任务 allowed_tools 独立判定（§13.1），未授权调用落 REJECTED 审计；测试 TestInvokePermissionDenied |
| 5 | 不可信输入处理 | ✓ | calculator 模式校验拒绝 `1+2;rm -rf /`（tools_test）；JSONL 逐行校验（前端本地 + 后端 task_type 校验）；知识库内容不改权限（架构 §13.2：权限规则只在网关） |
| 6 | 无高危通用工具 | ✓ | 扫描脚本 ④：无 shell/exec 类工具；四个内置工具均为只读或受控 Mock |
| 7 | CORS 未开放 | ✓ | 扫描脚本 ⑤：未配置任何跨域开放 |
| 8 | 数据库边界 | ✓ | Python 不直连数据库（架构 D-1，代码无 DB 连接）；核心表仅 Go 写入 |
| 9 | 网络边界 | ✓ | compose 无公网端口需求说明；`/internal/v1` 仅绑定本机/内网；服务间令牌双端校验 |
| 10 | 超时与大小限制 | ✓ | 工具级超时（网关强制）、模型超时（适配器）、内部 HTTP 客户端 10s、任务级 timeout_seconds；请求体 1MB 限制为契约要求（阶段 B 实现统一包络） |

已知不足（如实）：无用户认证/RBAC（需求 §3.1 允许 MVP 从简）；服务令牌为静态共享令牌，服务化拆分时应升级为按调用方签发。
