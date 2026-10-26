import type { ReactNode } from "react";
import { NavLink } from "react-router-dom";

import { Icon, type IconName } from "../icons";

// 全局壳：浅色侧边导航 + 内容区（含页头插槽）。
const NAV: Array<{ to: string; label: string; icon: IconName; end?: boolean }> = [
  { to: "/", label: "总览", icon: "overview", end: true },
  { to: "/tasks", label: "任务", icon: "tasks", end: true },
  { to: "/evaluations", label: "评测与诊断", icon: "chart" },
  { to: "/health", label: "平台健康", icon: "pulse" },
];

export function Layout(props: { children: ReactNode }) {
  return (
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
          {NAV.map((n) => (
            <NavLink key={n.to} to={n.to} end={n.end}
                     className={({ isActive }) => "nav-link" + (isActive ? " active" : "")}>
              <Icon name={n.icon} size={17} />
              {n.label}
            </NavLink>
          ))}
        </nav>
        <div className="sidebar-foot">MVP · Console v1</div>
      </aside>
      <main className="content">{props.children}</main>
    </div>
  );
}

// 页头：标题 + 说明 + 操作区
export function PageHeader(props: { title: string; desc?: string; actions?: ReactNode }) {
  return (
    <div className="page-head">
      <div>
        <h2>{props.title}</h2>
        {props.desc && <p className="page-desc">{props.desc}</p>}
      </div>
      {props.actions && <div className="page-actions">{props.actions}</div>}
    </div>
  );
}
