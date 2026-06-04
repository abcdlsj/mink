import type { ThreadSummary } from "@/lib/types";
import { useStore } from "@/lib/store";
import { Dot } from "../LeftPane";
import { relTime } from "@/lib/utils";

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
  const continueLabel = info.reply_count >= 2 ? "Continue in thread →" : "Open thread →";
  const replyLabel = info.reply_count === 1 ? "1 reply" : info.reply_count + " replies";
  const last = info.last_reply_author ? "last by " + info.last_reply_author : "";
  const when = relTime(info.last_reply_time);
  const segments = [replyLabel, last, when].filter((s) => s !== "");
  return (
    <button
      onClick={() => void openThread(info.parent_id)}
      className="mt-1.5 inline-flex items-center gap-1.5 border border-border bg-accent-bg px-1.5 py-0.5 text-[11.5px] text-text hover:bg-accent underline-offset-2 hover:underline"
    >
      {info.has_running_worker && <Dot status="running" />}
      <span className="font-medium">{continueLabel}</span>
      <span className="text-text-faint font-normal">{segments.join(" · ")}</span>
    </button>
  );
}
