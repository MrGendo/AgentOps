import { statusBadge } from "../format";

// 状态标签：复用 format.ts 的语义映射（测试已覆盖），圆点+文字不只依赖颜色。
export function StatusBadge(props: { status: string }) {
  const b = statusBadge(props.status as never);
  return <span className={b.className}>{b.label}</span>;
}
