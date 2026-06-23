import type { ThreadSummary } from "@/lib/types";
import { useStore } from "@/lib/store";
import { Dot } from "../LeftPane";
import { cn, relTime } from "@/lib/utils";

export function ThreadLink({ threadId, summary }: { threadId: string; summary: string }) {
  const openThread = useStore((s) => s.openThread);
  return (
    <button
      onClick={() => void openThread(threadId)}
      className="mt-2.5 inline-flex items-center gap-1.5 border border-border bg-panel-event px-1.5 py-0.5 text-[12px] text-text-muted hover:bg-accent hover:text-text"
    >
      <Dot status="running" />
      <span>{summary}</span>
    </button>
  );
}

export function ThreadSummaryRow({ info }: { info: ThreadSummary }) {
  const openThread = useStore((s) => s.openThread);
  const activeThread = useStore((s) => s.activeThread);
  const selected = activeThread === info.parent_id;
  const continueLabel = info.reply_count >= 2 ? "Open thread" : "Start thread";
  const replyLabel = info.reply_count === 1 ? "1 reply" : info.reply_count + " replies";
  const last = info.last_reply_author ? "last by " + info.last_reply_author : "";
  const when = relTime(info.last_reply_time);
  const segments = [replyLabel, last, when].filter((s) => s !== "");
  return (
    <button
      onClick={() => void openThread(info.parent_id)}
      className={cn(
        "mt-2 inline-grid max-w-full grid-cols-[auto_1fr_auto] items-center gap-2 border border-border-soft bg-panel-event px-2 py-1 text-left text-[11.5px] text-text-muted hover:border-border hover:bg-accent-bg hover:text-text",
        selected && "border-border bg-accent-bg text-text",
      )}
      title={segments.join(" · ")}
    >
      {info.has_running_worker && <Dot status="running" />}
      {!info.has_running_worker && <span className="font-mono text-[10px] text-text-faint">↳</span>}
      <span className="min-w-0 truncate font-medium">{continueLabel}</span>
      <span className="min-w-0 truncate font-normal text-text-faint">{segments.join(" · ")}</span>
    </button>
  );
}

export function ThreadStartRow({ messageID }: { messageID: string }) {
  const openThread = useStore((s) => s.openThread);
  return (
    <button
      onClick={() => void openThread(messageID)}
      className="mt-1.5 inline-flex items-center gap-1.5 border border-transparent bg-transparent px-1 py-0.5 font-mono text-[10.5px] uppercase text-text-faint opacity-70 hover:border-border-soft hover:bg-panel-event hover:text-text md:opacity-0 md:group-hover/message:opacity-100"
      title="Open a side thread for this message"
    >
      <span>↳</span>
      <span>Reply in thread</span>
    </button>
  );
}
