import { AgentGear } from "./AgentGear";
import { Composer } from "./Composer/Composer";
import { MessageRow } from "./Message/MessageRow";
import { MessageStream } from "./Message/MessageStream";
import { useStore } from "@/lib/store";

export function ThreadView() {
  const threadDetail = useStore((s) => s.threadDetail);
  const channels = useStore((s) => s.channels);
  const activeChannel = useStore((s) => s.activeChannel);
  const closeThread = useStore((s) => s.closeThread);
  const channel = channels.find((c) => c.id === activeChannel);

  if (!threadDetail) return null;
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

  const root = threadDetail.parent;
  const replies = threadDetail.replies || [];
  return (
    <main className="h-full min-w-0 grid grid-rows-[auto_1fr_auto] bg-panel">
      <div className="flex items-center gap-3 border-b-hard border-border px-5 py-3">
        <button onClick={() => closeThread()} className="border border-border bg-panel-2 px-2 py-0.5 text-[12px] text-text-muted hover:bg-accent hover:text-text">
          ← Back to {channel ? "#" + channel.name : "channel"}
        </button>
        <div className="text-[13px] font-black uppercase tracking-[0.5px] text-text">Thread</div>
        <div className="font-mono text-[12px] text-text-muted">
          {replies.length === 1 ? "1 reply" : replies.length + " replies"}
        </div>
        <AgentGear scope={{ kind: "thread", detail: threadDetail }} agents={useStore.getState().agents} />
      </div>
      <div className="overflow-y-auto px-3 pb-4 pt-4 md:px-5 md:pb-5">
        <div className="mx-auto max-w-[880px]">
          {root && (
            <div className="mb-4 border-b border-border-soft border-l-2 border-l-border pl-4 pb-3">
              <div className="mb-1 inline-flex border border-border bg-accent-bg px-1.5 py-px text-[11px] uppercase tracking-wide text-text">Root message · context only</div>
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
    </main>
  );
}
