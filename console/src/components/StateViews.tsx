import { Icon } from "../icons";

// 统一的加载 / 空 / 错误态
export function Loading({ text = "加载中…" }: { text?: string }) {
  return (
    <div className="state-view" role="status">
      <span className="spinner" aria-hidden="true" />
      <span className="muted">{text}</span>
    </div>
  );
}

export function EmptyState(props: { text: string; hint?: string }) {
  return (
    <div className="state-view empty">
      <Icon name="doc" size={28} className="empty-ic" />
      <div>{props.text}</div>
      {props.hint && <div className="muted small-text">{props.hint}</div>}
    </div>
  );
}

export function ErrorBanner({ message }: { message: string }) {
  return (
    <div className="banner red" role="alert">
      <Icon name="warn" size={15} />
      <span>{message}</span>
    </div>
  );
}
