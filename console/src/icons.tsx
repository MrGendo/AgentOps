// 统一线性图标：所有 SVG 显式声明 width/height 属性，
// 即使样式表未加载也不会撑破布局（巨型图标问题的结构性防御）。
export type IconName =
  | "overview" | "tasks" | "chart" | "pulse" | "refresh" | "plus"
  | "cancel" | "retry" | "search" | "close" | "doc" | "warn" | "check" | "clock";

interface IconProps {
  name: IconName;
  size?: number;
  className?: string;
}

const PATHS: Record<IconName, string> = {
  overview: "M4 13h6V4H4v9Zm0 7h6v-4H4v4Zm10 0h6V11h-6v9Zm0-13v4h6V4h-6Z",
  tasks: "M9 5H7a2 2 0 0 0-2 2v12a2 2 0 0 0 2 2h10a2 2 0 0 0 2-2V7a2 2 0 0 0-2-2h-2M9 5a2 2 0 0 0 2 2h2a2 2 0 0 0 2-2M9 5a2 2 0 0 1 2-2h2a2 2 0 0 1 2 2m-6 9 2 2 4-4",
  chart: "M4 20V10m6 10V4m6 16v-7",
  pulse: "M3 12h4l2-6 4 12 2-6h6",
  refresh: "M4 4v5h5M20 20v-5h-5M5 9a7 7 0 0 1 12-3l2 2M19 15a7 7 0 0 1-12 3l-2-2",
  plus: "M12 5v14M5 12h14",
  cancel: "M6 6l12 12M18 6 6 18",
  retry: "M4 4v5h5M5 9a7 7 0 0 1 12-3l2 2M20 20v-5h-5m4 0-2 2M19 15a7 7 0 0 1-12 3l-2-2",
  search: "M11 4a7 7 0 1 0 0 14 7 7 0 0 0 0-14Zm5 12 4 4",
  close: "M6 6l12 12M18 6 6 18",
  doc: "M7 3h7l4 4v14H7V3Zm7 0v4h4M9 12h6M9 16h6",
  warn: "M12 4 2 20h20L12 4Zm0 7v4m0 3v.5",
  check: "M5 13l4 4L19 7",
  clock: "M12 4a8 8 0 1 0 0 16 8 8 0 0 0 0-16Zm0 3v5l3 3",
};

export function Icon({ name, size = 16, className }: IconProps) {
  return (
    <svg
      viewBox="0 0 24 24"
      width={size}
      height={size}
      className={className}
      fill="none"
      stroke="currentColor"
      strokeWidth={1.8}
      strokeLinecap="round"
      strokeLinejoin="round"
      aria-hidden="true"
    >
      <path d={PATHS[name]} />
    </svg>
  );
}
