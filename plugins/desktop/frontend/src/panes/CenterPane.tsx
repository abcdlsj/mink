import { useEffect, useMemo, useState } from "react";
import { useStore } from "@/lib/store";
import { ChannelHeader } from "./ChannelHeader";
import { Composer } from "./Composer/Composer";
import { AgentDetailPane } from "./AgentDetailPane";
import { AgentsPanel } from "./AgentsPanel";
import { EmptyState } from "./EmptyState";
import { MessageSelectionBar } from "./Message/MessageSelectionBar";
import { MessageStream } from "./Message/MessageStream";
import { renderableMessage } from "./Message/message-helpers";
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
  const [selecting, setSelecting] = useState(false);
  const [selectedIDs, setSelectedIDs] = useState<Set<string>>(() => new Set());

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
  const visibleMessages = useMemo(
    () => (detail?.messages || []).filter(renderableMessage),
    [detail?.messages],
  );
  const selectedMessages = useMemo(
    () => visibleMessages.filter((m) => selectedIDs.has(m.id)),
    [selectedIDs, visibleMessages],
  );
  useEffect(() => {
    setSelecting(false);
    setSelectedIDs(new Set());
  }, [scope]);
  useEffect(() => {
    setSelectedIDs((prev) => {
      const visible = new Set(visibleMessages.map((m) => m.id));
      const next = new Set([...prev].filter((id) => visible.has(id)));
      return next.size === prev.size ? prev : next;
    });
  }, [visibleMessages]);

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
      <div>
        <ChannelHeader
          scope={scope}
          selecting={selecting}
          onStartSelection={() => setSelecting(true)}
        />
        <MessageSelectionBar
          active={selecting}
          selectedCount={selectedIDs.size}
          messages={selectedMessages}
          sourceLabel={sourceLabel}
          title={detail.item.title}
          onCancel={() => {
            setSelecting(false);
            setSelectedIDs(new Set());
          }}
          onSelectAll={() => setSelectedIDs(new Set(visibleMessages.map((m) => m.id)))}
        />
      </div>

      <div ref={scrollRef} onScroll={onScroll} className="overflow-y-auto px-3 pb-4 pt-4 md:px-5 md:pb-5 md:pt-5">
        <div className="w-full max-w-[1040px]">
          <MessageStream
            messages={detail.messages}
            empty={<EmptyState />}
            threadStartsEnabled
            selecting={selecting}
            selectedIDs={selectedIDs}
            onToggleSelected={(id) => setSelectedIDs((prev) => {
              const next = new Set(prev);
              if (next.has(id)) next.delete(id);
              else next.add(id);
              return next;
            })}
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
