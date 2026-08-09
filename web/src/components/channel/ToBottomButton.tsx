import { ArrowDown } from "lucide-react";

export function ToBottomButton({
  onClick,
  newMessageCount = 0,
}: {
  onClick: () => void;
  newMessageCount?: number;
}) {
  const label = newMessageCount > 0
    ? `${newMessageCount} new message${newMessageCount === 1 ? "" : "s"}. Go to latest message`
    : "Go to latest message";
  return (
    <button
      className="to-bottom-button"
      type="button"
      aria-label={label}
      title={label}
      onClick={onClick}
    >
      <ArrowDown aria-hidden="true" />
      <span>To bottom</span>
      {newMessageCount > 0 ? <b className="to-bottom-badge">{newMessageCount}</b> : null}
    </button>
  );
}
