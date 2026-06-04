import { useEffect, useRef, useState } from "react";
import { Hash, MessageSquare, AtSign, Square } from "lucide-react";
import { useStore } from "@/lib/store";
import { Button } from "@/components/ui/button";
import { AgentGear } from "./AgentGear";
import { Composer } from "./Composer/Composer";
import { EmptyState } from "./EmptyState";
import { MessageRow } from "./Message/MessageRow";
import { MessageStream } from "./Message/MessageStream";

export function CenterPane() {
  const view = useStore((s) => s.view);
  const detail = useStore((s) => s.detail);
  const channels = useStore((s) => s.channels);
  const agents = useStore((s) => s.agents);
  const agentDMs = useStore((s) => s.agentDMs);
  const activeChannel = useStore((s) => s.activeChannel);
  const activeAgent = useStore((s) => s.activeAgent);
  const activeThread = useStore((s) => s.activeThread);
  const updateAgentChatTitle = useStore((s) => s.updateAgentChatTitle);

  const scrollRef = useRef<HTMLDivElement | null>(null);
  const lastScopeRef = useRef<string>("");
  const [editingTitle, setEditingTitle] = useState(false);
  const [titleDraft, setTitleDraft] = useState("");
  const [titleBusy, setTitleBusy] = useState(false);
  const [titleErr, setTitleErr] = useState<string | null>(null);

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

  useEffect(() => {
    setEditingTitle(false);
    setTitleDraft("");
    setTitleBusy(false);
    setTitleErr(null);
  }, [scope]);

  const threadDetail = useStore((s) => s.threadDetail);
  if (threadDetail) {
    return <ThreadView />;
  }

  if (!detail) {
    return (
      <main className="h-full min-w-0 grid grid-rows-[auto_1fr_auto] bg-panel">
        <div className="border-b-hard border-border px-5 py-4" />
        <div className="overflow-y-auto px-5 py-6 text-[12.5px] text-text-muted">
          Pick a channel, agent, or thread to start.
        </div>
        <Composer />
      </main>
    );
  }

  const item = detail.item;
  const channel = channels.find((c) => c.id === activeChannel);
  let titleText = item.title;
  let metaText = "";
  let TitleIcon = MessageSquare;
  let listeningHint = "";
  if (view === "channel") {
    TitleIcon = Hash;
    titleText = channel?.name || "channel";
    metaText = item.running ? "agents running" : "";
    listeningHint = listeningSummary(channel, agents);
  } else if (view === "thread") {
    TitleIcon = MessageSquare;
    metaText = channel ? `in #${channel.name}` : "";
  } else if (view === "agent") {
    TitleIcon = AtSign;
    titleText = titleText.replace(/^@/, "");
    metaText = detail.summary || "";
  }

  const editableAgentChat = view === "agent" && !!activeAgent && agentDMs.some((dm) => dm.id === activeAgent);
  const beginTitleEdit = () => {
    if (!editableAgentChat) return;
    setTitleDraft(titleText === "New chat" ? "" : titleText);
    setTitleErr(null);
    setEditingTitle(true);
  };
  const submitTitleEdit = async () => {
    if (!editableAgentChat || !activeAgent || titleBusy) return;
    const next = titleDraft.trim();
    if (!next) {
      setTitleErr("Title is required.");
      return;
    }
    if (next === titleText) {
      setEditingTitle(false);
      return;
    }
    setTitleBusy(true);
    setTitleErr(null);
    try {
      await updateAgentChatTitle(activeAgent, next);
      setEditingTitle(false);
    } catch (e) {
      setTitleErr(e instanceof Error ? e.message : String(e));
    } finally {
      setTitleBusy(false);
    }
  };
  const cancelTitleEdit = () => {
    setEditingTitle(false);
    setTitleDraft("");
    setTitleErr(null);
  };
  const showStop = item.running && view === "thread";

  return (
    <main className="h-full min-w-0 grid grid-rows-[auto_1fr_auto] bg-panel">
      <div className="flex items-end justify-between border-b-hard border-border bg-panel px-5 pb-3.5 pt-4">
        <div>
          <h2 className="flex items-center gap-2 font-display text-[18px] font-black text-text">
            <span className="inline-flex size-7 items-center justify-center border-2 border-border bg-accent">
              <TitleIcon className="size-[17px] text-text" />
            </span>
            {editableAgentChat && editingTitle ? (
              <span className="inline-flex min-w-[220px] flex-col gap-1">
                <input
                  value={titleDraft}
                  onChange={(e) => {
                    setTitleDraft(e.target.value);
                    if (titleErr) setTitleErr(null);
                  }}
                  onBlur={() => void submitTitleEdit()}
                  onKeyDown={(e) => {
                    if (e.key === "Enter") {
                      e.preventDefault();
                      void submitTitleEdit();
                    } else if (e.key === "Escape") {
                      e.preventDefault();
                      cancelTitleEdit();
                    }
                  }}
                  disabled={titleBusy}
                  autoFocus
                  className="h-8 border-hard border-border bg-bg px-2 font-display text-[18px] font-black text-text outline-none shadow-card disabled:opacity-70"
                />
                {titleErr && <span className="font-mono text-[10.5px] font-medium text-error">{titleErr}</span>}
              </span>
            ) : editableAgentChat ? (
              <button
                type="button"
                onClick={beginTitleEdit}
                className="min-w-0 truncate border border-transparent px-1 text-left hover:border-border hover:bg-bg"
                title="Click to rename"
              >
                {titleText}
              </button>
            ) : (
              <span>{titleText}</span>
            )}
            {view === "channel" && channel && (
              <AgentGear scope={{ kind: "channel", channel }} agents={agents} />
            )}
          </h2>
          {(metaText || listeningHint) && (
            <div className="mt-1 font-mono text-[11.5px] text-text-muted">
              {metaText}
              {metaText && listeningHint && " · "}
              {listeningHint}
            </div>
          )}
        </div>
        {showStop && (
          <Button variant="danger" size="sm" onClick={() => void useStore.getState().stop()}>
            <Square className="size-3" />
            <span>Stop run</span>
          </Button>
        )}
      </div>

      <div ref={scrollRef} className="overflow-y-auto px-5 pb-5 pt-5">
        <div className="mx-auto max-w-[880px]">
          <MessageStream messages={detail.messages} empty={<EmptyState />} />
        </div>
      </div>

      <Composer />
    </main>
  );
}

function listeningSummary(
  ch: import("@/lib/types").ChannelItem | undefined,
  agents: import("@/lib/types").AgentItem[],
): string {
  if (!ch) return "";
  const joined = ch.agents || [];
  if (joined.length === 0) return "";
  const head = joined.length + " agent" + (joined.length === 1 ? "" : "s");
  const modes = ch.agent_modes || {};
  const visible = joined.slice(0, 2).map((id) => {
    const display = agents.find((a) => a.id === id)?.display || id;
    const mode = modes[id] === "listen" ? "listening" : "mention only";
    return `${display} ${mode}`;
  });
  let tail = visible.join(" · ");
  if (joined.length > 2) tail += ` · +${joined.length - 2}`;
  return `${head} · ${tail}`;
}

function ThreadView() {
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
        <div className="overflow-y-auto px-5 py-8 text-[13px] text-text-muted">
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
        <div className="overflow-y-auto px-5 py-8 text-[13px] text-text-muted">
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
      <div className="overflow-y-auto px-5 pt-4 pb-5">
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
