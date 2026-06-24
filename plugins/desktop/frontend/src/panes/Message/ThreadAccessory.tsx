import type { ThreadSummary } from "@/lib/types";
import { useStore } from "@/lib/store";
import { Dot } from "../LeftPane";
import { cn, relTime } from "@/lib/utils";

export function ThreadLink({ threadId, summary }: { threadId: string; summary: string }) {
  const openThread = useStore((s) => s.openThread);
  return (
    <button
      onClick={() => void openThread(threadId)}
      className="mt-2.5 inline-flex items-center gap-1.5 border border-tool-border bg-tool-bg px-1.5 py-0.5 text-[12px] text-tool hover:border-accent-border hover:bg-accent hover:text-text"
    >
      <Dot status="running" />
      <span>{summary}</span>
    </button>
  );
}

export function ThreadAction({ info, messageID }: { info?: ThreadSummary; messageID?: string }) {
  const openThread = useStore((s) => s.openThread);
  const activeThread = useStore((s) => s.activeThread);
  if (info) {
    const selected = activeThread === info.parent_id;
    const replyLabel = info.reply_count === 1 ? "1 reply" : info.reply_count + " replies";
    const last = info.last_reply_author ? "last by " + info.last_reply_author : "";
    const when = relTime(info.last_reply_time);
    const segments = [replyLabel, last, when].filter((s) => s !== "");
    return (
      <button
        type="button"
        onClick={() => void openThread(info.parent_id)}
        className={cn(
          "inline-flex h-5 items-center gap-1 border border-border-soft bg-transparent px-1.5 font-mono text-[10.5px] font-medium text-text-muted hover:border-border hover:bg-panel-event hover:text-text",
          selected && "border-agent-border bg-agent-bg text-agent",
        )}
        aria-label={"Open thread: " + segments.join(" · ")}
      >
        {info.has_running_worker ? <Dot status="running" /> : <span>↳</span>}
        <span>{info.reply_count}</span>
      </button>
    );
  }
  if (!messageID) return null;
  return (
    <button
      type="button"
      onClick={() => void openThread(messageID)}
      className="inline-flex size-5 items-center justify-center border border-transparent bg-transparent font-mono text-[12px] text-text-faint opacity-0 hover:border-border-soft hover:bg-panel-event hover:text-text focus:opacity-100 md:group-hover/message:opacity-100"
      aria-label="Reply in thread"
    >
      ↳
    </button>
  );
}
