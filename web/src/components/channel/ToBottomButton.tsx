import { ArrowDown } from "lucide-react";

export function ToBottomButton({ onClick }: { onClick: () => void }) {
  return (
    <button
      className="to-bottom-button"
      type="button"
      aria-label="Go to latest message"
      title="Go to latest message"
      onClick={onClick}
    >
      <ArrowDown aria-hidden="true" />
      <span>To bottom</span>
    </button>
  );
}
