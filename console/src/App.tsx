import { BrowserRouter, NavLink, Route, Routes } from "react-router-dom";

import Evaluations from "./pages/Evaluations";
import Health from "./pages/Health";
import TaskDetail from "./pages/TaskDetail";
import TaskList from "./pages/TaskList";

export default function App() {
  return (
    <BrowserRouter>
      <header className="topbar">
        <h1>AgentOps 控制台</h1>
        <nav>
          <NavLink to="/" end>任务</NavLink>
          <NavLink to="/evaluations">评测</NavLink>
          <NavLink to="/health">健康</NavLink>
        </nav>
      </header>
      <main>
        <Routes>
          <Route path="/" element={<TaskList />} />
          <Route path="/tasks/:id" element={<TaskDetail />} />
          <Route path="/evaluations" element={<Evaluations />} />
          <Route path="/health" element={<Health />} />
        </Routes>
      </main>
    </BrowserRouter>
  );
}
