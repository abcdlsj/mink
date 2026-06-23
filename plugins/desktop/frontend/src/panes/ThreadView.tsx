import { useEffect, useState } from "react";
import { Trash2 } from "lucide-react";
import { ConfirmDialog } from "@/components/ConfirmDialog";
import { AgentGear } from "./AgentGear";
import { Composer } from "./Composer/Composer";
import { MessageRow } from "./Message/MessageRow";
import { MessageStream } from "./Message/MessageStream";
import { useStore } from "@/lib/store";
import { useMessageAutoScroll } from "./useMessageAutoScroll";

export function ThreadView() {
  const threadDetail = useStore((s) => s.threadDetail);
  const channels = useStore((s) => s.channels);
  const activeChannel = useStore((s) => s.activeChannel);
  const activeAnchor = useStore((s) => s.activeAnchor);
  const closeThread = useStore((s) => s.closeThread);
  const deleteConversation = useStore((s) => s.deleteConversation);
  const channel = channels.find((c) => c.id === activeChannel);
  const replies = threadDetail?.replies || [];
  const scope = threadDetail ? `thread:${threadDetail.space_id}:${threadDetail.parent_id}` : "thread:none";
  const { scrollRef, onScroll } = useMessageAutoScroll(replies, scope);
  const [deleteOpen, setDeleteOpen] = useState(false);
  const [deleteBusy, setDeleteBusy] = useState(false);
  const [deleteErr, setDeleteErr] = useState<string | null>(null);

  useEffect(() => {
    if (!activeAnchor?.startsWith("message:")) return;
    const id = activeAnchor.slice("message:".length);
    window.requestAnimationFrame(() => {
      document.getElementById("message-" + id)?.scrollIntoView({
        block: "center",
      });
    });
  }, [activeAnchor, threadDetail?.parent?.id, replies.length]);

  useEffect(() => {
    setDeleteOpen(false);
    setDeleteBusy(false);
    setDeleteErr(null);
  }, [scope]);

  if (!threadDetail) return null;
  const root = threadDetail.parent;

  const submitDelete = async () => {
    if (deleteBusy) return;
    setDeleteBusy(true);
    setDeleteErr(null);
    try {
      await deleteConversation({
        kind: "thread",
        id: threadDetail.space_id,
        parentMessageID: threadDetail.parent_id,
      });
      setDeleteOpen(false);
    } catch (e) {
      setDeleteErr(e instanceof Error ? e.message : String(e));
    } finally {
      setDeleteBusy(false);
    }
  };

  if (threadDetail.unsupported) {
    return (
      <main className="h-full min-w-0 grid grid-rows-[auto_1fr] bg-panel">
        <div className="flex items-center gap-3 border-b-hard border-border px-5 py-3">
          <button onClick={() => closeThread()} className="border border-border bg-panel-2 px-2 py-0.5 text-[12px] text-text-muted hover:bg-accent hover:text-text">
            ← Back
          </button>
          <div className="text-[13px] font-semibold text-text">Thread</div>
        </div>
        <div className="overflow-y-auto px-3 py-6 text-[13px] text-text-muted md:px-5 md:py-8">
          {threadDetail.unsupported_hint || "Threads are not supported here."}
        </div>
      </main>
    );
  }
  if (threadDetail.not_found) {
    return (
      <main className="h-full min-w-0 grid grid-rows-[auto_1fr] bg-panel">
        <div className="flex items-center gap-3 border-b-hard border-border px-5 py-3">
          <button onClick={() => closeThread()} className="border border-border bg-panel-2 px-2 py-0.5 text-[12px] text-text-muted hover:bg-accent hover:text-text">
            ← Back to {channel ? "#" + channel.name : "channel"}
          </button>
          <div className="text-[13px] font-semibold text-text">Thread</div>
        </div>
        <div className="overflow-y-auto px-3 py-6 text-[13px] text-text-muted md:px-5 md:py-8">
          Thread not found.
        </div>
      </main>
    );
  }

  return (
    <main className="h-full min-w-0 grid grid-rows-[auto_1fr_auto] bg-panel">
      <div className="flex min-w-0 items-center gap-2 border-b-hard border-border px-3 py-2">
        <button
          onClick={() => closeThread()}
          className="shrink-0 border border-border bg-panel-2 px-2 py-0.5 text-[12px] text-text-muted hover:bg-accent hover:text-text"
          aria-label={channel ? "Back to " + channel.name : "Back to channel"}
        >
          ←
        </button>
        <div className="min-w-0 truncate font-display text-[13px] font-extrabold uppercase text-text">Thread</div>
        <div className="shrink-0 font-mono text-[11px] text-text-muted">
          {replies.length === 1 ? "1 reply" : replies.length + " replies"}
        </div>
        <AgentGear scope={{ kind: "thread", detail: threadDetail }} agents={useStore.getState().agents} />
        <button
          type="button"
          onClick={() => {
            setDeleteErr(null);
            setDeleteOpen(true);
          }}
          disabled={deleteBusy}
          className="ml-auto inline-flex size-7 shrink-0 items-center justify-center border border-border bg-panel-2 text-text-muted hover:bg-error hover:text-bg"
          aria-label="Delete thread"
        >
          <Trash2 className="size-3.5" />
        </button>
      </div>
      <div ref={scrollRef} onScroll={onScroll} className="overflow-y-auto px-3 pb-4 pt-4 md:px-5 md:pb-5">
        <div className="mx-auto max-w-[880px]">
          {root && (
            <div className="mb-4 border-b border-border-soft border-l-2 border-l-border pl-4 pb-3">
              <div className="mb-1 inline-flex border border-tool-border bg-tool-bg px-1.5 py-px text-[11px] font-medium uppercase text-tool">Root message · context only</div>
              <MessageRow m={root} compact={false} />
            </div>
          )}
          {replies.length === 0 && (
            <div className="py-4 text-[12.5px] text-text-muted">No replies yet. Send the first reply below.</div>
          )}
          <MessageStream
            messages={replies}
            empty={null}
            filterRenderable={false}
            compactAcrossThreadLinks
          />
        </div>
      </div>
      <Composer />
      <ConfirmDialog
        open={deleteOpen}
        title="Delete this thread?"
        body="This removes thread replies and thread-scoped model context."
        confirmLabel="Delete thread"
        danger
        busy={deleteBusy}
        error={deleteErr}
        onConfirm={() => void submitDelete()}
        onCancel={() => {
          if (deleteBusy) return;
          setDeleteOpen(false);
          setDeleteErr(null);
        }}
      />
    </main>
  );
}
