# AgentOps 阶段 E 设计规格（管理控制台）
- 日期：2026-10-26
- 依据：`requirements.md` §12、§16.6、§17-E；契约 `docs/openapi/api-v1.yaml`
- 状态：已批准并执行完成

## 1. 目标
交付 React 管理控制台：任务列表（创建/筛选/取消）、任务详情（步骤/尝试/工具调用/时间线/诊断 + 取消/重试操作）、评测页（数据集导入/指标/版本对比/失败样例下钻）。验收（§16.6）：全部数据来自真实后端 API，创建/取消/重试操作与后端状态一致。

## 2. 技术与结构决策
- 既有 Vite + React 19 + TS 骨架之上扩展；新增 `react-router-dom`（页面路由）与 `vitest`（纯函数单测）。不引入 UI 组件库与状态管理库——页面规模小，原生 fetch + hooks 足够（最简原则）。
- 开发期经 Vite 代理 `/api → :8080`（已有配置）；生产形态由 nginx 反代（已有配置）。
- 全部请求走 `src/api.ts` 统一层：错误统一呈现 `{code,message}`，禁止页面散落 fetch。

```
console/src/
  api.ts        全量 API fetcher（与 api-v1.yaml 对齐）
  types.ts      契约类型（Task/Step/ToolCall/Event/Trace/Dataset/EvalRun/Diagnosis）
  format.ts     纯函数：状态徽标、耗时、指标格式化（可测）
  App.tsx       路由 + 顶部导航（任务 / 评测 / 平台健康）
  pages/
    TaskList.tsx     列表 + 筛选 + 游标分页 + 创建对话框 + 行内取消
    TaskDetail.tsx   详情 + 轨迹 + 时间线 + 诊断 + 操作
    Evaluations.tsx  数据集/运行列表 + 创建评测 + 运行详情 + 版本对比
    Health.tsx       健康页（既有，保留）
  styles.css
```

## 3. 页面行为
### 3.1 任务列表（/）
- 筛选：status、task_type；游标分页（上一页/下一页）。
- 列：ID（缩写）、类型、状态徽标、尝试次数（>1 高亮为“重试过”）、耗时、错误摘要、创建时间。
- 创建对话框：task_type（下拉：mock.* 与 agent.ops_diagnosis）、input JSON（校验合法性）、allowed_tools（多选，来自 GET /tools）、超时/重试次数（可选）。
- 行内操作：非终态显示“取消”；终态 FAILED/CANCELLED 显示“重试”。
- 自动刷新开关（默认开，5s 轮询当前页）。

### 3.2 任务详情（/tasks/:id）
- 信息卡：全字段 + trace_id；操作按钮（取消/重试，按状态机可见性显示，结果以 toast 提示）。
- 步骤区：每步展开显示 attempts 历史（状态/错误类/耗时）。
- 工具调用表：工具/版本/状态/耗时/错误码。
- 事件时间线：seq 升序（游标加载更多），类型着色。
- 诊断区：GET /tasks/{id}/diagnosis；无报告且失败时显示“自动生成中/证据不足”态。
- 30s 轮询（任务终态后停止）。

### 3.3 评测页（/evaluations）
- 数据集卡：列表 + 导入（文件选择读文本 → POST x-ndjson + ?name=；或粘贴 JSON）。
- 创建评测：选数据集 + agent_version；运行列表含状态与四项核心指标（成功率/正确率/重试率/平均时长）。
- 运行详情：逐样例结果（状态/规则得分/耗时），失败样例链接到对应任务详情。
- 版本对比：选数据集后按 agent_version 分组并列展示核心指标。

## 4. 契约对齐
types.ts 字段名与 api-v1.yaml 一一对应（camel/snake 以后端 JSON 为准）；错误体 `{error:{code,message}}` 在 api.ts 统一解包。列表游标、事件游标按后端 meta.next_cursor 透传。

## 5. 错误与降级
- 后端不可达/降级（health degraded）：页面顶部横幅提示，保留已有数据。
- 操作失败：toast 展示错误码与信息，不静默。

## 6. 测试与验收
- vitest：format.ts 纯函数（状态徽标/耗时/指标格式化）+ api.ts fetcher（mock fetch，断言 URL/方法/错误解包）。
- `npm run build`（tsc + vite）通过。
- 页面级实测：对真实后端（Go+Python 双进程）逐端点复核页面所用的全部 API（curl），README 提供浏览器手动验收清单（§16.6 五条逐条）。
- 浏览器自动化测试不在本阶段范围（如实说明；阶段 F 可选补充）。

## 7. 风险
| 风险 | 对策 |
|---|---|
| 创建对话框的 JSON 输入易错 | 提交前本地 JSON 校验 + 示例模板 |
| 事件时间线数据量大 | 游标分页默认 50 条，按需加载 |
| 轮询风暴 | 仅当前页/详情轮询，切页即停 |

## 8. 验收标准（§16.6 五条）
列表与详情可见；时间线/工具调用/错误信息可见；评测指标与诊断报告可见；创建/取消/重试与后端状态一致（手动清单 + 后端集成测试已覆盖语义）。
