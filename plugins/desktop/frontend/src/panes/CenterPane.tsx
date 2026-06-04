import { useEffect, useRef } from "react";
import { useStore } from "@/lib/store";
import { ChannelHeader } from "./ChannelHeader";
import { Composer } from "./Composer/Composer";
import { EmptyState } from "./EmptyState";
import { MessageStream } from "./Message/MessageStream";
import { ThreadView } from "./ThreadView";

export function CenterPane() {
  const view = useStore((s) => s.view);
  const detail = useStore((s) => s.detail);
  const activeChannel = useStore((s) => s.activeChannel);
  const activeAgent = useStore((s) => s.activeAgent);
  const activeThread = useStore((s) => s.activeThread);

  const scrollRef = useRef<HTMLDivElement | null>(null);
  const lastScopeRef = useRef<string>("");

  const messageCount = detail?.messages.length ?? 0;
  const scope = `${view}:${activeChannel || ""}:${activeThread || ""}:${activeAgent || ""}`;

  useEffect(() => {
    const el = scrollRef.current;
    if (!el) return;
    if (lastScopeRef.current !== scope) {
      el.scrollTop = el.scrollHeight;
      lastScopeRef.current = scope;
      return;
    }
    const distanceFromBottom = el.scrollHeight - el.scrollTop - el.clientHeight;
    if (distanceFromBottom < 120) {
      el.scrollTop = el.scrollHeight;
    }
  }, [scope, messageCount]);

  const threadDetail = useStore((s) => s.threadDetail);
  if (threadDetail) {
    return <ThreadView />;
  }

  if (!detail) {
    return (
      <main className="h-full min-w-0 grid grid-rows-[auto_1fr_auto] bg-panel">
        <ChannelHeader scope={scope} />
        <div className="overflow-y-auto px-5 py-6 text-[12.5px] text-text-muted">
          Pick a channel, agent, or thread to start.
        </div>
        <Composer />
      </main>
    );
  }

  return (
    <main className="h-full min-w-0 grid grid-rows-[auto_1fr_auto] bg-panel">
      <ChannelHeader scope={scope} />

      <div ref={scrollRef} className="overflow-y-auto px-5 pb-5 pt-5">
        <div className="mx-auto max-w-[880px]">
          <MessageStream messages={detail.messages} empty={<EmptyState />} />
        </div>
      </div>

      <Composer />
    </main>
  );
}
