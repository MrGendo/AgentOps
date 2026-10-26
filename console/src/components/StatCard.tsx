// 统计卡片：数值 + 标签 + 可选强调语义与附注（数据一律来自真实 API）。
export function StatCard(props: {
  label: string;
  value: string | number;
  tone?: "ok" | "err" | "run" | "wait" | "neutral";
  note?: string;
  icon?: React.ReactNode;
}) {
  return (
    <div className={"stat-card" + (props.tone ? ` tone-${props.tone}` : "")}>
      <div className="stat-top">
        <span className="stat-label">{props.label}</span>
        {props.icon && <span className="stat-ic">{props.icon}</span>}
      </div>
      <div className="stat-value">{props.value}</div>
      {props.note && <div className="stat-note">{props.note}</div>}
    </div>
  );
}
