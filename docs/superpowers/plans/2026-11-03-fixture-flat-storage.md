# 2026-11-03 fixture 平铺入库 + 工作区自动建基线

## 背景

三个 SWE fixture（boundary_bug / logic_error / missing_validation）的 `repo/` 目录以 gitlink（mode 160000）提交——嵌套 git 仓库被识别为无 `.gitmodules` 的子模块引用。GitHub 克隆后这些目录为空，fixture 不可用，`make test-py` 与 swe.fix 演示在新鲜克隆上直接失败。

## 方案（已获用户批准）

1. fixture 的 `repo/` 移除内嵌 `.git`，文件平铺入库（普通 blob）
2. `workspace.py::prepare_workspace` 复制后若工作区无 `.git`，自动 `git init + add + commit` 建基线（本地现存 fixture 带 `.git`，行为不变；新鲜克隆走自动建基线路径）
3. 补一条"无 .git 的 fixture 也能产出 diff"的测试
4. 新增 MIT LICENSE 文件（用户已确认）

## 任务清单

- [x] 移除三个 fixture 的内嵌 `.git`
- [x] git 索引中 gitlink 换为平铺文件
- [x] workspace.py 自动建基线（git -c 显式用户配置，不依赖全局 git config）
- [x] 新增 test_workspace_auto_init_baseline
- [x] 全量 `make test-py` 验证（现有用例走真实 fixture，即真实回归）
- [x] LICENSE 文件（MIT）

## 验证结论

- `make test-py` 等价命令：**75 passed**（74 存量 + 1 新增 `test_workspace_auto_init_baseline`）
- 本地 fixture 移除 `.git` 后，全部存量用例（工作区隔离/补丁提取/Mock 端到端验收）走自动建基线路径仍全绿——等价于新鲜克隆环境的真实回归
- `git ls-files -s | awk '$1 == "160000"'` 无输出：索引中不再存在任何 gitlink
- fixture 内 `__pycache__/*.pyc` 运行产物已被 .gitignore 覆盖，未入库
