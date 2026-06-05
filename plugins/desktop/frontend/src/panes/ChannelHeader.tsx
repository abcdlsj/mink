import { useEffect, useState } from "react";
import { AtSign, Hash, MessageSquare, Square } from "lucide-react";
import type { AgentItem, ChannelItem } from "@/lib/types";
import { Button } from "@/components/ui/button";
import { useStore } from "@/lib/store";
import { AgentGear } from "./AgentGear";

export function ChannelHeader({ scope }: { scope: string }) {
  const view = useStore((s) => s.view);
  const detail = useStore((s) => s.detail);
  const channels = useStore((s) => s.channels);
  const agents = useStore((s) => s.agents);
  const agentDMs = useStore((s) => s.agentDMs);
  const activeChannel = useStore((s) => s.activeChannel);
  const activeAgent = useStore((s) => s.activeAgent);
  const updateAgentChatTitle = useStore((s) => s.updateAgentChatTitle);
  const runtimeMeta = useStore((s) => s.runtimeMeta);
  const [editingTitle, setEditingTitle] = useState(false);
  const [titleDraft, setTitleDraft] = useState("");
  const [titleBusy, setTitleBusy] = useState(false);
  const [titleErr, setTitleErr] = useState<string | null>(null);

  useEffect(() => {
    setEditingTitle(false);
    setTitleDraft("");
    setTitleBusy(false);
    setTitleErr(null);
  }, [scope]);

  if (!detail) return <div className="border-b-hard border-border px-5 py-4" />;

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
  const personaForMeta = (() => {
    if (view !== "agent" || !activeAgent) return "";
    if (detail?.item.persona_id) return detail.item.persona_id;
    const dm = agentDMs.find((d) => d.id === activeAgent);
    return dm?.persona_id || activeAgent;
  })();
  const meta = personaForMeta ? runtimeMeta[personaForMeta] : undefined;
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
        {meta && <RuntimeMetaChip meta={meta} />}
      </div>
      {showStop && (
        <Button variant="danger" size="sm" onClick={() => void useStore.getState().stop()}>
          <Square className="size-3" />
          <span>Stop run</span>
        </Button>
      )}
    </div>
  );
}

function RuntimeMetaChip({ meta }: { meta: Record<string, string> }) {
  const parts: string[] = [];
  const runtime = meta.runtime;
  const version = meta.version;
  if (runtime) parts.push(version ? `${runtime} ${version}` : runtime);
  if (meta.model) parts.push(meta.model);
  if (meta.tools_count) parts.push(`${meta.tools_count} tools`);
  if (meta.mcp_servers_count) parts.push(`${meta.mcp_servers_count} mcp`);
  if (meta.permission_mode && meta.permission_mode !== "default") parts.push(meta.permission_mode);
  if (parts.length === 0) return null;
  return (
    <div className="mt-1.5 inline-flex max-w-full flex-wrap items-center gap-1 border border-border bg-panel-2 px-1.5 py-0.5 font-mono text-[10.5px] text-text-muted">
      {parts.map((p, i) => (
        <span key={i} className={i > 0 ? "before:mr-1 before:content-['·']" : ""}>{p}</span>
      ))}
    </div>
  );
}

function listeningSummary(ch: ChannelItem | undefined, agents: AgentItem[]): string {
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
