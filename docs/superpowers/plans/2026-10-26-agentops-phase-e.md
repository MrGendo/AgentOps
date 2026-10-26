# AgentOps 阶段 E 任务计划
- 日期：2026-10-26
- 规格：`docs/superpowers/specs/2026-10-26-agentops-phase-e-design.md`
- 状态：待审批

## 任务清单
- [x] T1 路由与骨架：react-router-dom、导航布局、types.ts（契约对齐）
- [x] T2 API 层 api.ts 全量 fetcher + 错误解包 + vitest 环境与 mock fetch 单测
- [x] T3 format.ts 纯函数（徽标/耗时/指标格式化）+ 单测
- [x] T4 任务列表页（筛选/分页/创建对话框/取消/自动刷新）
- [x] T5 任务详情页（步骤+尝试/工具调用/时间线/诊断/操作）
- [x] T6 评测页（数据集导入/创建评测/指标卡/运行详情/版本对比）
- [x] T7 样式与降级态（全局 CSS、错误横幅、加载/空态）
- [x] T8 验证：vitest + npm build + 真实后端端点复核 + README 手动验收清单；任务日志 + git 提交

## 验收对照
- 需求 §17-E「前端通过真实 API 完成查询与必要任务操作」→ T4-T8
- 规格 §8 五条 → T8 手动清单与既有后端集成测试
