import { useState } from "react";
import { Hash, MessageSquare, AtSign, Square } from "lucide-react";
import { useStore } from "@/lib/store";
import { Button } from "@/components/ui/button";
import { Identicon } from "@/components/Identicon";
import { EventBlock } from "@/components/EventBlock";
import { Dot } from "./LeftPane";
import { cn, relTime } from "@/lib/utils";

export function CenterPane() {
  const view = useStore((s) => s.view);
  const detail = useStore((s) => s.detail);
  const channels = useStore((s) => s.channels);
  const activeChannel = useStore((s) => s.activeChannel);

  if (!detail) {
    return (
      <main className="h-full grid grid-rows-[auto_1fr_auto] bg-panel min-w-0">
        <div className="border-b border-border-soft px-8 py-4" />
        <div className="overflow-y-auto px-8 py-6 text-text-faint text-[12.5px]">
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
  if (view === "channel") {
    TitleIcon = Hash;
    titleText = channel?.name || "channel";
    metaText = item.running ? "agents running" : "";
  } else if (view === "thread") {
    TitleIcon = MessageSquare;
    metaText = channel ? `in #${channel.name}` : "";
  } else if (view === "agent") {
    TitleIcon = AtSign;
    titleText = titleText.replace(/^@/, "");
    metaText = detail.summary || "";
  }

  const showStop = item.running && view === "thread";

  return (
    <main className="h-full grid grid-rows-[auto_1fr_auto] bg-panel min-w-0">
      <div className="flex items-end justify-between border-b border-border-soft px-8 pt-4 pb-3.5">
        <div>
          <h2 className="flex items-center gap-1.5 text-[15px] font-semibold text-text">
            <TitleIcon className="size-4 text-text-muted" />
            <span>{titleText}</span>
          </h2>
          {metaText && <div className="mt-0.5 text-[12px] text-text-muted">{metaText}</div>}
        </div>
        {showStop && (
          <Button variant="danger" size="sm" onClick={() => void useStore.getState().stop()}>
            <Square className="size-3" />
            <span>Stop run</span>
          </Button>
        )}
      </div>

      <div className="overflow-y-auto px-8 pt-5 pb-6">
        <div className="mx-auto max-w-[800px]">
          {detail.messages.length === 0 ? (
            <EmptyState />
          ) : (
            detail.messages.map((m, i) => {
              const prev = detail.messages[i - 1];
              const sameAuthor =
                prev && prev.role === m.role && (prev.author_id || "") === (m.author_id || "");
              const close =
                prev && new Date(m.time).getTime() - new Date(prev.time).getTime() < 5 * 60 * 1000;
              const compact =
                sameAuthor && close && !m.thread_id && !(m.events && m.events.length);
              return <MessageRow key={m.id} m={m} compact={!!compact} />;
            })
          )}
        </div>
      </div>

      <Composer />
    </main>
  );
}

function EmptyState() {
  const view = useStore((s) => s.view);
  const detail = useStore((s) => s.detail);
  const agents = useStore((s) => s.agents);
  const activeAgent = useStore((s) => s.activeAgent);
  const threads = useStore((s) => s.threads);
  const channels = useStore((s) => s.channels);
  const openThread = useStore((s) => s.openThread);

  if (view !== "agent") {
    return (
      <div className="text-text-faint text-[12.5px] py-10 text-center">No messages yet.</div>
    );
  }
  const ag = agents.find((a) => a.id === activeAgent);
  const recent = threads.slice(0, 3);

  return (
    <div className="py-6">
      <div className="flex items-center gap-3 mb-2">
        <div className="size-9 rounded-md border border-border-soft bg-panel overflow-hidden">
          <Identicon seed={ag?.id || activeAgent || "agent"} kind="agent" />
        </div>
        <div>
          <div className="text-[15px] font-semibold text-text">
            {detail?.item?.title || "@" + (ag?.display || "")}
          </div>
          {ag?.role && <div className="text-[12px] text-text-muted mt-0.5">{ag.role}</div>}
        </div>
      </div>
      <div className="text-[12.5px] text-text-faint leading-relaxed mb-6">
        Send a message to start a direct conversation. Replies stay private to you and {ag?.display || "this agent"}.
      </div>
      {recent.length > 0 && (
        <div>
          <div className="text-[10.5px] uppercase tracking-[0.7px] text-text-faint mb-2 font-semibold">
            Recently with {ag?.display || "this agent"}
          </div>
          <div className="flex flex-col gap-1">
            {recent.map((t) => {
              const ch = channels.find((c) => c.id === t.channel_id);
              return (
                <button
                  key={t.id}
                  onClick={() => void openThread(t.id)}
                  className="w-full text-left flex items-center justify-between gap-2 px-2 py-1.5 rounded-sm hover:bg-panel-2 cursor-pointer"
                >
                  <span className="flex items-center gap-1.5 text-[12.5px] text-text min-w-0">
                    {t.has_running && <Dot status="running" />}
                    <span className="truncate">{t.title}</span>
                  </span>
                  <span className="text-[11px] text-text-faint shrink-0">
                    {ch ? `#${ch.name} · ` : ""}
                    {relTime(t.updated_at)}
                  </span>
                </button>
              );
            })}
          </div>
        </div>
      )}
    </div>
  );
}

function MessageRow({ m, compact }: { m: import("@/lib/types").MessageView; compact: boolean }) {
  const agents = useStore((s) => s.agents);
  const ag = agents.find((a) => a.id === m.author_id);
  const seed = m.role === "user" ? "user" : m.author_id || m.author_name || "agent";
  const kind = m.role === "user" ? "user" : "agent";

  const events = m.events || [];

  return (
    <div
      className={cn(
        "grid grid-cols-[30px_1fr] gap-3.5",
        compact ? "-mt-4 mb-1" : "mb-6",
      )}
    >
      <div
        className={cn(
          "size-[30px] rounded-md border border-border-soft bg-panel overflow-hidden mt-px shadow-[0_1px_0_rgba(31,41,51,0.02)]",
          compact && "invisible",
        )}
      >
        <Identicon seed={seed} kind={kind} />
      </div>
      <div>
        {!compact && (
          <div className="flex items-baseline gap-1.5 mb-0.5">
            <span className="text-[12.5px] font-semibold text-text-muted tracking-[0.1px]">
              {m.role === "user" ? "You" : m.author_name || "Sumi"}
            </span>
            {m.role !== "user" && ag?.role && (
              <span
                className="text-[10.5px] text-text-faint border border-border-soft rounded-[3px] px-1.5 py-px font-medium max-w-[180px] truncate"
                title={ag.role}
              >
                {ag.role}
              </span>
            )}
            <span className="text-[11px] text-text-faint">{relTime(m.time)}</span>
          </div>
        )}
        {m.reasoning && m.role !== "user" && <ReasoningPreface text={m.reasoning} />}
        {m.content && (
          <div className="text-[14px] text-text leading-[1.7] whitespace-pre-wrap">{m.content}</div>
        )}
        {events.length > 0 && (
          <div className="mt-2 flex flex-col gap-1">
            {events.map((ev, i) => (
              <EventBlock key={i} ev={ev} />
            ))}
          </div>
        )}
        {m.thread_id && m.thread_summary && (
          <ThreadLink threadId={m.thread_id} summary={m.thread_summary} />
        )}
      </div>
    </div>
  );
}

function ReasoningPreface({ text }: { text: string }) {
  const [open, setOpen] = useState(false);
  const lineCount = (text.match(/\n/g)?.length ?? 0) + 1;
  const isLong = lineCount > 5 || text.length > 320;
  const display = !isLong || open ? text : text.replace(/\n+/g, " ").slice(0, 320) + "…";
  return (
    <div className="text-[12px] text-text-muted leading-[1.55] whitespace-pre-wrap mb-1.5 max-w-prose">
      {display}
      {isLong && (
        <>
          {" "}
          <button
            onClick={() => setOpen((v) => !v)}
            className="text-[11.5px] text-text-faint hover:text-text-muted underline underline-offset-2 cursor-pointer"
          >
            {open ? "Show less thinking" : "Show more thinking"}
          </button>
        </>
      )}
    </div>
  );
}

function ThreadLink({ threadId, summary }: { threadId: string; summary: string }) {
  const openThread = useStore((s) => s.openThread);
  return (
    <button
      onClick={() => void openThread(threadId)}
      className="mt-2.5 inline-flex items-center gap-1.5 px-1.5 py-0.5 rounded-sm text-[12px] text-text-muted hover:text-text hover:bg-panel-2"
    >
      <Dot status="running" />
      <span>{summary}</span>
    </button>
  );
}

function Composer() {
  const view = useStore((s) => s.view);
  const channels = useStore((s) => s.channels);
  const activeChannel = useStore((s) => s.activeChannel);
  const agents = useStore((s) => s.agents);
  const activeAgent = useStore((s) => s.activeAgent);
  const personas = useStore((s) => s.personas);
  const models = useStore((s) => s.models);
  const sending = useStore((s) => s.sending);
  const send = useStore((s) => s.send);

  const [input, setInput] = useState("");
  const [persona, setPersona] = useState("");

  let placeholder = "Message...";
  if (view === "channel") {
    const ch = channels.find((c) => c.id === activeChannel);
    placeholder = `Message #${ch?.name || "channel"}...`;
  } else if (view === "thread") {
    placeholder = "Reply in thread...";
  } else if (view === "agent") {
    const ag = agents.find((a) => a.id === activeAgent);
    placeholder = `Message @${ag?.display || "agent"}...`;
  }

  const trimmed = input.trim();
  const canSend = trimmed.length > 0 && !sending;

  const handleSend = async () => {
    if (!canSend) return;
    const text = trimmed;
    setInput("");
    await send(text, persona || undefined);
  };

  const handleKey = (e: React.KeyboardEvent<HTMLTextAreaElement>) => {
    if ((e.metaKey || e.ctrlKey) && e.key === "Enter") {
      e.preventDefault();
      void handleSend();
    }
  };

  return (
    <div className="border-t border-border-soft px-8 pb-5 pt-3.5 bg-panel">
      <div className="mx-auto max-w-[800px]">
        <textarea
          rows={2}
          placeholder={placeholder}
          value={input}
          onChange={(e) => setInput(e.target.value)}
          onKeyDown={handleKey}
          disabled={sending}
          className="w-full min-h-[76px] resize-none rounded-md border border-border bg-panel px-3.5 py-3 text-[14px] leading-[1.55] text-text outline-none transition-[border,box-shadow] hover:border-border-strong focus:border-accent focus:ring-[3px] focus:ring-accent-bg disabled:opacity-70"
        />
        <div className="mt-2.5 flex items-center gap-2">
          <select
            value={persona}
            onChange={(e) => setPersona(e.target.value)}
            className="bg-transparent text-[12px] text-text-muted px-1.5 py-1 rounded-sm hover:bg-panel-2 hover:text-text outline-none cursor-pointer"
          >
            <option value="">Default agent</option>
            {personas.map((p) => (
              <option key={p.id} value={p.id}>
                {p.display}
              </option>
            ))}
          </select>
          <select className="bg-transparent text-[12px] text-text-muted px-1.5 py-1 rounded-sm hover:bg-panel-2 hover:text-text outline-none cursor-pointer">
            {models.map((m) => (
              <option key={m.name} value={m.name}>
                {m.model}
              </option>
            ))}
          </select>
          <span className="flex-1" />
          <Button variant="primary" disabled={!canSend} onClick={() => void handleSend()}>
            {sending ? "Sending…" : "Send"}
          </Button>
        </div>
      </div>
    </div>
  );
}
