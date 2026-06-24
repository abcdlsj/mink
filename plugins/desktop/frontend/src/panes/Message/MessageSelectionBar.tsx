import type { MessageView } from "@/lib/types";
import { cn } from "@/lib/utils";
import { serializeMessagesForCopy } from "./message-transcript";

export function MessageSelectionBar({
  active,
  selectedCount,
  messages,
  sourceLabel,
  title,
  hrefFor,
  onCancel,
  onSelectAll,
}: {
  active: boolean;
  selectedCount: number;
  messages: MessageView[];
  sourceLabel: string;
  title?: string;
  hrefFor?: (m: MessageView) => string;
  onCancel: () => void;
  onSelectAll: () => void;
}) {
  const copy = async () => {
    if (messages.length === 0) return;
    const text = serializeMessagesForCopy({ title, sourceLabel, messages, hrefFor: hrefFor || defaultHrefFor });
    await navigator.clipboard.writeText(text);
  };

  if (!active) {
    return null;
  }

  return (
    <div className="flex flex-wrap items-center gap-2 border-b-hard border-border bg-panel-2 px-5 py-2 shadow-card">
      <div className="font-mono text-[11px] uppercase tracking-[0.5px] text-text-muted">
        {selectedCount} selected
      </div>
      <button
        type="button"
        onClick={onSelectAll}
        className="border border-border-soft bg-panel px-2 py-0.5 font-mono text-[10.5px] font-semibold text-text-muted hover:border-border hover:text-text"
      >
        Select visible
      </button>
      <button
        type="button"
        onClick={() => void copy()}
        disabled={messages.length === 0}
        className={cn(
          "border px-2 py-0.5 font-mono text-[10.5px] font-semibold",
          messages.length === 0
            ? "cursor-not-allowed border-border-soft bg-panel text-text-whisper"
            : "border-action bg-action text-panel hover:brightness-95",
        )}
      >
        Copy transcript
      </button>
      <button
        type="button"
        onClick={onCancel}
        className="ml-auto border border-border-soft bg-panel px-2 py-0.5 font-mono text-[10.5px] font-semibold text-text-muted hover:border-border hover:text-text"
      >
        Done
      </button>
    </div>
  );
}

function defaultHrefFor(m: MessageView): string {
  if (typeof window === "undefined") return "#message-" + m.id;
  const url = new URL(window.location.href);
  url.searchParams.set("anchor", "message:" + m.id);
  return url.toString();
}
