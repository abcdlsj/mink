export function ManagementLoading({ label }: { label: string }) {
  return (
    <div className="management-feedback" role="status">
      <span className="status-dot loading" />
      <h2>{label}</h2>
    </div>
  );
}

export function ManagementError({
  message,
  onRetry,
}: {
  message?: string;
  onRetry: () => void;
}) {
  return (
    <div className="management-feedback error" role="alert">
      <span className="offline-mark">!</span>
      <h2>Facts unavailable</h2>
      <p>{message ?? "Could not load facts from the Server."}</p>
      <button className="secondary-action" type="button" onClick={onRetry}>
        Retry
      </button>
    </div>
  );
}

export function ManagementEmpty({
  icon,
  title,
  detail,
}: {
  icon: React.ReactNode;
  title: string;
  detail: string;
}) {
  return (
    <div className="management-feedback empty">
      <span className="empty-icon">{icon}</span>
      <h2>{title}</h2>
      <p>{detail}</p>
    </div>
  );
}

export function InlineNotice({
  tone,
  title,
  detail,
  action,
  onAction,
}: {
  tone: "warning" | "danger" | "success";
  title: string;
  detail: string;
  action?: string;
  onAction?: () => void;
}) {
  return (
    <div
      className={`inline-notice ${tone}`}
      role={tone === "danger" ? "alert" : "status"}
    >
      <div>
        <strong>{title}</strong>
        <p>{detail}</p>
      </div>
      {action && onAction && (
        <button type="button" onClick={onAction}>
          {action}
        </button>
      )}
    </div>
  );
}

export function Fact({
  label,
  value,
  mono = false,
}: {
  label: string;
  value: string;
  mono?: boolean;
}) {
  return (
    <div>
      <dt>{label}</dt>
      <dd className={mono ? "mono" : ""}>{value}</dd>
    </div>
  );
}
