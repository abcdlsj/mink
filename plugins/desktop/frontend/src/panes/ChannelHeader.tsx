import { useEffect, useState } from "react";
import { AtSign, Hash, MessageSquare, Trash2 } from "lucide-react";
import type { AgentItem, ChannelItem } from "@/lib/types";
import { useStore } from "@/lib/store";
import { ConfirmDialog } from "@/components/ConfirmDialog";
import { AgentGear } from "./AgentGear";

export function ChannelHeader({ scope }: { scope: string }) {
  const view = useStore((s) => s.view);
  const detail = useStore((s) => s.detail);
  const channels = useStore((s) => s.channels);
  const agents = useStore((s) => s.agents);
  const agentDMs = useStore((s) => s.agentDMs);
  const activeDirect = useStore((s) => s.activeDirect);
  const activeChannel = useStore((s) => s.activeChannel);
  const activeAgentSpace = useStore((s) => s.activeAgentSpace);
  const updateAgentChatTitle = useStore((s) => s.updateAgentChatTitle);
  const updateDirectChatTitle = useStore((s) => s.updateDirectChatTitle);
  const deleteConversation = useStore((s) => s.deleteConversation);
  const [editingTitle, setEditingTitle] = useState(false);
  const [titleDraft, setTitleDraft] = useState("");
  const [titleBusy, setTitleBusy] = useState(false);
  const [titleErr, setTitleErr] = useState<string | null>(null);
  const [deleteBusy, setDeleteBusy] = useState(false);
  const [deleteOpen, setDeleteOpen] = useState(false);
  const [deleteErr, setDeleteErr] = useState<string | null>(null);

  useEffect(() => {
    setEditingTitle(false);
    setTitleDraft("");
    setTitleBusy(false);
    setTitleErr(null);
    setDeleteOpen(false);
    setDeleteErr(null);
    setDeleteBusy(false);
  }, [scope]);

  if (!detail) return <div className="border-b-hard border-border px-5 py-4" />;

  const item = detail.item;
  const channel = channels.find((c) => c.id === activeChannel);
  let titleText = item.title;
  let metaText = "";
  let TitleIcon = MessageSquare;
  let listeningHint = "";
  let objectType = "Direct Message";
  const isNamedAgentChat = view === "agent" && !!activeAgentSpace && agentDMs.some((dm) => dm.id === activeAgentSpace);
  const isEditableDirect = view === "direct" && !!activeDirect && item.title !== "Sumi";
  if (view === "channel") {
    TitleIcon = Hash;
    titleText = channel?.name || "channel";
    objectType = "Channel";
    listeningHint = listeningSummary(channel, agents);
  } else if (view === "direct") {
    TitleIcon = MessageSquare;
    objectType = "Direct Message";
    metaText = channel ? `in #${channel.name}` : "";
  } else if (view === "agent") {
    TitleIcon = AtSign;
    titleText = titleText.replace(/^@/, "");
    const personaDisplay =
      detail.item.persona_name ||
      agents.find((a) => a.id === detail.item.persona_id)?.display ||
      titleText;
    objectType = isNamedAgentChat ? "Agent Chat" : "Default Agent DM";
    metaText = `@${personaDisplay}`;
    if (detail.summary) metaText += ` · ${detail.summary}`;
  }

  const editable = isNamedAgentChat || isEditableDirect;
  const deleteTarget =
    view === "channel" && detail.item.id
      ? { kind: "channel" as const, id: detail.item.id, label: "#" + (channel?.name || item.title || "channel"), type: "channel" }
      : view === "direct" && detail.item.id
        ? { kind: "direct_chat" as const, id: detail.item.id, label: item.title || "direct chat", type: "direct message" }
        : view === "agent" && detail.item.id
          ? { kind: "agent_dm" as const, id: detail.item.id, label: titleText || "agent chat", type: objectType.toLowerCase() }
          : null;
  const submitDelete = async () => {
    if (!deleteTarget || deleteBusy) return;
    setDeleteBusy(true);
    setDeleteErr(null);
    try {
      await deleteConversation(deleteTarget);
      setDeleteOpen(false);
    } catch (e) {
      setDeleteErr(e instanceof Error ? e.message : String(e));
    } finally {
      setDeleteBusy(false);
    }
  };
  const beginTitleEdit = () => {
    if (!editable) return;
    setTitleDraft(titleText === "New chat" ? "" : titleText);
    setTitleErr(null);
    setEditingTitle(true);
  };
  const submitTitleEdit = async () => {
    if (!editable || titleBusy) return;
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
      if (isNamedAgentChat && activeAgentSpace) {
        await updateAgentChatTitle(activeAgentSpace, next);
      } else if (isEditableDirect && activeDirect) {
        await updateDirectChatTitle(activeDirect, next);
      }
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
  return (
    <>
    <div className="flex items-end justify-between border-b-hard border-border bg-panel px-5 pb-3.5 pt-4">
      <div>
        <h2 className="flex items-center gap-2 font-display text-[19px] font-extrabold leading-tight text-text">
          <span className="inline-flex size-7 items-center justify-center border-2 border-border bg-accent">
            <TitleIcon className="size-[14px] text-text" />
          </span>
          {editable && editingTitle ? (
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
                className="h-8 border-hard border-border bg-bg px-2 font-display text-[19px] font-extrabold text-text outline-none shadow-card disabled:opacity-70"
              />
              {titleErr && <span className="font-mono text-[10.5px] font-medium text-error">{titleErr}</span>}
            </span>
          ) : editable ? (
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
          <span className="border border-border-soft bg-panel-2 px-1.5 py-px font-mono text-[10.5px] font-semibold uppercase tracking-[0.2px] text-text-muted">
            {objectType}
          </span>
          {view === "channel" && channel && (
            <AgentGear scope={{ kind: "channel", channel }} agents={agents} />
          )}
        </h2>
        {(metaText || listeningHint) && (
          <div className="mt-1 font-mono text-[11px] text-text-muted">
            {metaText}
            {metaText && listeningHint && " · "}
            {listeningHint}
          </div>
        )}
      </div>
      {deleteTarget && (
        <button
          type="button"
          onClick={() => {
            setDeleteErr(null);
            setDeleteOpen(true);
          }}
          disabled={deleteBusy}
          className="inline-flex items-center gap-1.5 border border-border bg-panel-2 px-2.5 py-1.5 font-mono text-[11px] font-semibold uppercase text-text-muted hover:bg-error hover:text-bg disabled:cursor-not-allowed disabled:opacity-60"
          title="Delete local conversation history and runtime context"
        >
          <Trash2 className="size-3.5" />
          {deleteBusy ? "Deleting" : "Delete"}
        </button>
      )}
    </div>
    {deleteTarget && (
      <ConfirmDialog
        open={deleteOpen}
        title={`Delete ${deleteTarget.label}?`}
        body={`This removes local chat history and model context for this ${deleteTarget.type}.`}
        confirmLabel="Delete"
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
    )}
    </>
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
