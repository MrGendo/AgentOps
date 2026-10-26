import { BrowserRouter, Route, Routes } from "react-router-dom";

import { Layout } from "./components/Layout";
import Evaluations from "./pages/Evaluations";
import Health from "./pages/Health";
import Overview from "./pages/Overview";
import TaskDetail from "./pages/TaskDetail";
import TaskList from "./pages/TaskList";

export default function App() {
  return (
    <BrowserRouter>
      <Layout>
        <Routes>
          <Route path="/" element={<Overview />} />
          <Route path="/tasks" element={<TaskList />} />
          <Route path="/tasks/:id" element={<TaskDetail />} />
          <Route path="/evaluations" element={<Evaluations />} />
          <Route path="/health" element={<Health />} />
        </Routes>
      </Layout>
    </BrowserRouter>
  );
}
