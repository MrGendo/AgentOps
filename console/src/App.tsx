import { BrowserRouter, NavLink, Route, Routes } from "react-router-dom";

import Evaluations from "./pages/Evaluations";
import Health from "./pages/Health";
import TaskDetail from "./pages/TaskDetail";
import TaskList from "./pages/TaskList";

// 极简线性图标（17px 描边，与导航字号对齐）
const icon = {
  fill: "none", stroke: "currentColor", strokeWidth: 1.8,
  strokeLinecap: "round" as const, strokeLinejoin: "round" as const,
};
const TaskIcon = () => (
  <svg viewBox="0 0 24 24" {...icon}><path d="M9 5H7a2 2 0 0 0-2 2v12a2 2 0 0 0 2 2h10a2 2 0 0 0 2-2V7a2 2 0 0 0-2-2h-2M9 5a2 2 0 0 0 2 2h2a2 2 0 0 0 2-2M9 5a2 2 0 0 1 2-2h2a2 2 0 0 1 2 2m-6 9 2 2 4-4" /></svg>
);
const ChartIcon = () => (
  <svg viewBox="0 0 24 24" {...icon}><path d="M4 20V10m6 10V4m6 16v-7" /></svg>
);
const PulseIcon = () => (
  <svg viewBox="0 0 24 24" {...icon}><path d="M3 12h4l2-6 4 12 2-6h6" /></svg>
);

export default function App() {
  return (
    <BrowserRouter>
      <div className="shell">
        <aside className="sidebar">
          <div className="brand">
            <div className="brand-mark">A</div>
            <div>
              <b>AgentOps</b>
              <small>可靠执行 · 评测 · 诊断</small>
            </div>
          </div>
          <nav className="nav">
            <NavLink to="/" end><TaskIcon />任务</NavLink>
            <NavLink to="/evaluations"><ChartIcon />评测</NavLink>
            <NavLink to="/health"><PulseIcon />平台健康</NavLink>
          </nav>
          <div className="sidebar-foot">MVP · Console v1</div>
        </aside>
        <main className="content">
          <Routes>
            <Route path="/" element={<TaskList />} />
            <Route path="/tasks/:id" element={<TaskDetail />} />
            <Route path="/evaluations" element={<Evaluations />} />
            <Route path="/health" element={<Health />} />
          </Routes>
        </main>
      </div>
    </BrowserRouter>
  );
}
