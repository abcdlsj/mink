import { useEffect } from "react";
import { useStore } from "@/lib/store";
import { ChannelHeader } from "./ChannelHeader";
import { Composer } from "./Composer/Composer";
import { AgentDetailPane } from "./AgentDetailPane";
import { AgentsPanel } from "./AgentsPanel";
import { EmptyState } from "./EmptyState";
import { MessageStream } from "./Message/MessageStream";
import { TaskBoard } from "./TaskBoard";
import { ThreadView } from "./ThreadView";
import { useMessageAutoScroll } from "./useMessageAutoScroll";

export function CenterPane({ preferThreadView = false }: { preferThreadView?: boolean }) {
  const view = useStore((s) => s.view);
  const detail = useStore((s) => s.detail);
  const activeChannel = useStore((s) => s.activeChannel);
  const activeDirect = useStore((s) => s.activeDirect);
  const activeAgentSpace = useStore((s) => s.activeAgentSpace);
  const activeThread = useStore((s) => s.activeThread);
  const activeAnchor = useStore((s) => s.activeAnchor);

  const messageCount = detail?.messages.length ?? 0;
  const scope = preferThreadView
    ? `${view}:${activeChannel || ""}:${activeDirect || ""}:${activeThread || ""}:${activeAgentSpace || ""}`
    : `${view}:${activeChannel || ""}:${activeDirect || ""}:${activeAgentSpace || ""}`;
  const { scrollRef, onScroll } = useMessageAutoScroll(detail?.messages || [], scope);

  useEffect(() => {
    if (!activeAnchor?.startsWith("message:")) return;
    const id = activeAnchor.slice("message:".length);
    window.requestAnimationFrame(() => {
      document.getElementById("message-" + id)?.scrollIntoView({
        block: "center",
      });
    });
  }, [activeAnchor, messageCount]);

  const threadDetail = useStore((s) => s.threadDetail);
  const sourceLabel = detail ? sourceLabelForDetail(view, detail.item.title, detail.item.id) : "current conversation";
  if (view === "tasks") {
    return <TaskBoard />;
  }

  if (view === "agents") {
    return <AgentsPanel />;
  }

  if (view === "agent_detail") {
    return <AgentDetailPane />;
  }

  if (threadDetail && preferThreadView) {
    return <ThreadView />;
  }

  if (!detail) {
    return (
      <main className="h-full min-w-0 grid grid-rows-[auto_1fr_auto] bg-panel">
        <ChannelHeader scope={scope} />
        <div className="overflow-y-auto px-3 py-5 text-[12.5px] text-text-muted md:px-5 md:py-6">
          Pick a channel, agent, or thread to start.
        </div>
        <Composer forceMainScope={!preferThreadView} />
      </main>
    );
  }

  return (
    <main className="h-full min-w-0 grid grid-rows-[auto_1fr_auto] bg-panel">
      <ChannelHeader scope={scope} />

      <div ref={scrollRef} onScroll={onScroll} className="overflow-y-auto px-3 pb-4 pt-4 md:px-5 md:pb-5 md:pt-5">
        <div className="w-full max-w-[1040px]">
          <MessageStream
            messages={detail.messages}
            empty={<EmptyState />}
            threadStartsEnabled
            sourceLabel={sourceLabel}
            title={detail.item.title}
          />
        </div>
      </div>

      <Composer forceMainScope={!preferThreadView} />
    </main>
  );
}

function sourceLabelForDetail(view: string, title: string, id: string): string {
  if (view === "channel") return "#" + (title || id);
  if (view === "direct") return "dm:" + (title || id);
  if (view === "agent") return "agent:" + (title || id);
  return title || id;
}
