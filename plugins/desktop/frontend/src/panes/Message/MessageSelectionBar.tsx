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
  onStart,
  onCancel,
  onSelectAll,
}: {
  active: boolean;
  selectedCount: number;
  messages: MessageView[];
  sourceLabel: string;
  title?: string;
  hrefFor: (m: MessageView) => string;
  onStart: () => void;
  onCancel: () => void;
  onSelectAll: () => void;
}) {
  const copy = async () => {
    if (messages.length === 0) return;
    const text = serializeMessagesForCopy({ title, sourceLabel, messages, hrefFor });
    await navigator.clipboard.writeText(text);
  };

  if (!active) {
    return (
      <div className="sticky top-0 z-10 mb-3 flex justify-end bg-panel/95 pb-2">
        <button
          type="button"
          onClick={onStart}
          className="border border-border-soft bg-panel-2 px-2.5 py-1 font-mono text-[11px] font-semibold uppercase tracking-[0.4px] text-text-muted hover:border-border hover:text-text"
        >
          Select messages
        </button>
      </div>
    );
  }

  return (
    <div className="sticky top-0 z-10 mb-3 flex flex-wrap items-center gap-2 border border-border bg-panel-2 px-2.5 py-2 shadow-card">
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
            : "border-accent bg-accent text-bg hover:brightness-95",
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
