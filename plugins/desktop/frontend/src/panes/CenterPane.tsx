import { useEffect, useRef, useState } from "react";
import { Hash, MessageSquare, AtSign, Square } from "lucide-react";
import { useStore } from "@/lib/store";
import { Button } from "@/components/ui/button";
import { Identicon } from "@/components/Identicon";
import { EventBlock } from "@/components/EventBlock";
import { Markdown } from "@/components/Markdown";
import { Dot } from "./LeftPane";
import { cn, relTime } from "@/lib/utils";

export function CenterPane() {
  const view = useStore((s) => s.view);
  const detail = useStore((s) => s.detail);
  const channels = useStore((s) => s.channels);
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
          <h2 className="flex items-center gap-1.5 text-[16px] font-display font-semibold text-text tracking-[-0.2px]">
            <TitleIcon className="size-[18px] text-text-muted" />
            <span>{titleText}</span>
          </h2>
          {metaText && <div className="mt-1 text-[12px] text-text-faint">{metaText}</div>}
        </div>
        {showStop && (
          <Button variant="danger" size="sm" onClick={() => void useStore.getState().stop()}>
            <Square className="size-3" />
            <span>Stop run</span>
          </Button>
        )}
      </div>

      <div ref={scrollRef} className="overflow-y-auto px-8 pt-5 pb-6">
        <div className="mx-auto max-w-[800px]">
          {(() => {
            const visible = detail.messages.filter(renderableMessage);
            if (visible.length === 0) return <EmptyState />;
            return visible.map((m, i) => {
              const prev = visible[i - 1];
              const sameAuthor =
                prev && prev.role === m.role && (prev.author_id || "") === (m.author_id || "");
              const close =
                prev && new Date(m.time).getTime() - new Date(prev.time).getTime() < 5 * 60 * 1000;
              const compact =
                sameAuthor && close && !m.thread_id && !(m.events && m.events.length);
              return <MessageRow key={m.id} m={m} compact={!!compact} />;
            });
          })()}
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
  const activeChannel = useStore((s) => s.activeChannel);
  const threads = useStore((s) => s.threads);
  const channels = useStore((s) => s.channels);
  const openThread = useStore((s) => s.openThread);

  if (view === "channel") {
    const ch = channels.find((c) => c.id === activeChannel);
    return (
      <div className="text-text-faint text-[13px] py-12 text-center">
        Start in #{ch?.name || "channel"}.
      </div>
    );
  }
  if (view === "thread") {
    return (
      <div className="text-text-faint text-[13px] py-12 text-center">
        Reply in this thread.
      </div>
    );
  }

  const ag = agents.find((a) => a.id === activeAgent);
  const recent = threads.slice(0, 3);

  return (
    <div className="py-6">
      <div className="flex items-center gap-3 mb-2">
        <div className="size-10 rounded-md border border-border-soft bg-panel overflow-hidden">
          <Identicon seed={ag?.id || activeAgent || "agent"} kind="agent" />
        </div>
        <div>
          <div className="font-display text-[16px] font-semibold text-text tracking-[-0.2px]">
            {detail?.item?.title || "@" + (ag?.display || "")}
          </div>
          {ag?.role && <div className="text-[12.5px] text-text-muted mt-0.5">{ag.role}</div>}
        </div>
      </div>
      <div className="text-[13px] text-text-faint leading-[1.6] mb-6">
        Message {ag?.display || "this agent"} directly.
      </div>
      {recent.length > 0 && (
        <div>
          <div className="font-display text-[10px] uppercase tracking-[0.9px] text-text-whisper mb-2 font-semibold">
            Recently with {ag?.display || "this agent"}
          </div>
          <div className="flex flex-col gap-1">
            {recent.map((t) => {
              const ch = channels.find((c) => c.id === t.channel_id);
              return (
                <button
                  key={t.id}
                  onClick={() => void openThread(t.id)}
                  className="w-full text-left flex items-center justify-between gap-2 px-2 py-1.5 rounded-sm cursor-pointer text-text-muted hover:text-text"
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
  const view = useStore((s) => s.view);
  const activeAgent = useStore((s) => s.activeAgent);

  const dmAgent = view === "agent" && m.role !== "user"
    ? agents.find((a) => a.id === activeAgent)
    : undefined;

  const ag = dmAgent || agents.find((a) => a.id === m.author_id);
  const seed = m.role === "user"
    ? "user"
    : (dmAgent?.id || m.author_id || m.author_name || "agent");
  const kind = m.role === "user" ? "user" : "agent";
  const displayName = m.role === "user"
    ? "You"
    : (dmAgent?.display || m.author_name || ag?.display || "Sumi");

  const events = m.events || [];
  const collabEvents = events.filter((e) => e.kind === "mention" || e.kind === "delegate");
  const toolEvents = events.filter((e) => e.kind === "tool_call");
  const noticeEvents = events.filter((e) => e.kind === "service_notice");
  const shouldFoldTools = toolEvents.length > 1;

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
          <div className="flex items-baseline gap-2 mb-1">
            <span className="font-display text-[13px] font-semibold text-text tracking-[-0.1px]">
              {displayName}
            </span>
            {m.role !== "user" && ag?.role && (
              <span
                className="font-display text-[10.5px] tracking-[0.3px] text-text-faint font-medium"
                title={ag.role}
              >
                {shortRole(ag.role)}
              </span>
            )}
            <span className="text-[11px] text-text-whisper tabular-nums">{relTime(m.time)}</span>
          </div>
        )}
        {m.reasoning && m.role !== "user" && <ReasoningPreface text={m.reasoning} />}
        {m.content && (
          m.role === "user" ? (
            <div
              className={cn(
                "text-[14px] text-text leading-[1.68] whitespace-pre-wrap max-w-[70ch]",
                m.reasoning && "mt-2",
              )}
            >
              {m.content}
            </div>
          ) : (
            <Markdown
              className={cn(
                "text-[14px] text-text leading-[1.68] max-w-[70ch]",
                m.reasoning && "mt-2",
              )}
            >
              {stripCollabLeak(m.content)}
            </Markdown>
          )
        )}
        {events.length > 0 && (
          <div className="mt-2 flex flex-col gap-1">
            {collabEvents.map((ev, i) => (
              <EventBlock key={"c" + i} ev={ev} />
            ))}
            {shouldFoldTools ? (
              <ToolFold events={toolEvents} />
            ) : (
              toolEvents.map((ev, i) => <EventBlock key={"t" + i} ev={ev} />)
            )}
            {noticeEvents.map((ev, i) => (
              <EventBlock key={"n" + i} ev={ev} />
            ))}
          </div>
        )}
        {m.thread_id && m.thread_summary && (
          <ThreadLink threadId={m.thread_id} summary={m.thread_summary} />
        )}
        {m.thread_info && <ThreadSummaryRow info={m.thread_info} />}
        {m.task_accessory && <TaskAccessoryRow info={m.task_accessory} />}
      </div>
    </div>
  );
}

function ReasoningPreface({ text }: { text: string }) {
  const [open, setOpen] = useState(false);
  const flat = text.replace(/\s+/g, " ").trim();
  const isLong = flat.length > 280;
  const collapsed = isLong ? flat.slice(0, 280) + "…" : flat;
  return (
    <div className="text-[11.5px] text-text-faint leading-[1.5] mb-1.5 max-w-[68ch] tracking-[-0.05px]">
      {open ? (
        <Markdown variant="lite" className="whitespace-pre-wrap">
          {text}
        </Markdown>
      ) : (
        <span className="whitespace-pre-wrap">{collapsed}</span>
      )}
      {isLong && (
        <>
          {" "}
          <button
            onClick={() => setOpen((v) => !v)}
            className="text-[11px] text-text-whisper hover:text-text-faint underline underline-offset-2 cursor-pointer"
          >
            {open ? "Show less" : "Show full thinking"}
          </button>
        </>
      )}
    </div>
  );
}

function renderableMessage(m: import("@/lib/types").MessageView): boolean {
  if (m.is_thread_reply) return false;
  if (m.content && m.content.trim() !== "") return true;
  if (m.reasoning && m.reasoning.trim() !== "") return true;
  if (m.events && m.events.length > 0) return true;
  if (m.thread_id && m.thread_summary) return true;
  if (m.thread_info) return true;
  return false;
}

function shortRole(role: string): string {
  const trimmed = role.trim().replace(/[.。!?！？]$/, "");
  if (!trimmed) return "";
  const firstWord = trimmed.split(/[\s—·,、/]+/)[0] || trimmed;
  const word = firstWord.length <= 14 ? firstWord : firstWord.slice(0, 14) + "…";
  return titleCase(word);
}

function stripCollabLeak(text: string): string {
  // Collab task ids and scheduling acks belong on the delegate event row,
  // not in the assistant prose. Strip the most common leak patterns.
  let out = text;
  out = out.replace(/[（(]\s*task_id\s*=\s*[A-Za-z0-9_-]+\s*[）)]/g, "");
  out = out.replace(/[,，]?\s*task_id\s*=\s*[A-Za-z0-9_-]+/g, "");
  out = out.replace(/\bscheduled\s+next\s+team\s+turn\s+for\s+\S+.*$/gim, "");
  return out.replace(/[ \t]+\n/g, "\n").replace(/\n{3,}/g, "\n\n").trim();
}

function titleCase(s: string): string {
  if (!/[a-zA-Z]/.test(s)) return s;
  return s
    .toLowerCase()
    .replace(/(?:^|[\s-])(\p{L})/gu, (m) => m.toUpperCase());
}

function ToolFold({ events }: { events: import("@/lib/types").EventBlock[] }) {
  const [open, setOpen] = useState(false);
  const totalMs = events.reduce((sum, e) => sum + (e.duration_ms || 0), 0);
  const anyRunning = events.some((e) => e.status === "running");
  const anyError = events.some((e) => e.status === "error");
  const status = anyRunning ? "running" : anyError ? "error" : "done";
  const label =
    "Used " + events.length + " tools · " + (anyRunning ? "running" : (totalMs >= 1000 ? Math.round(totalMs / 100) / 10 + "s" : totalMs + "ms"));
  if (open) {
    return (
      <div className="flex flex-col gap-1">
        <button
          onClick={() => setOpen(false)}
          className={cn(
            "self-start text-[12px] underline underline-offset-2 cursor-pointer",
            status === "error" ? "text-error" : status === "running" ? "text-running" : "text-text-muted",
          )}
        >
          Hide {events.length} tool details
        </button>
        {events.map((ev, i) => (
          <EventBlock key={i} ev={ev} />
        ))}
      </div>
    );
  }
  return (
    <button
      onClick={() => setOpen(true)}
      className={cn(
        "self-start text-[12px] cursor-pointer",
        status === "error" ? "text-error" : status === "running" ? "text-running" : "text-text-muted",
      )}
    >
      <span>{label}</span>
      <span className="text-text-faint"> · </span>
      <span className="underline underline-offset-2 text-text-faint">view details</span>
      {anyRunning && <span className="ml-1.5 inline-block size-1.5 rounded-full bg-running align-middle" />}
    </button>
  );
}

function ThreadLink({ threadId, summary }: { threadId: string; summary: string }) {
  const openThread = useStore((s) => s.openThread);
  return (
    <button
      onClick={() => void openThread(threadId)}
      className="mt-2.5 inline-flex items-center gap-1.5 px-1.5 py-0.5 rounded-sm text-[12px] text-text-muted hover:text-text"
    >
      <Dot status="running" />
      <span>{summary}</span>
    </button>
  );
}

function ThreadSummaryRow({ info }: { info: import("@/lib/types").ThreadSummary }) {
  const openThread = useStore((s) => s.openThread);
  const replyLabel = info.reply_count === 1 ? "1 reply" : info.reply_count + " replies";
  const last = info.last_reply_author ? "last by " + info.last_reply_author : "";
  const when = relTime(info.last_reply_time);
  const segments = [replyLabel, last, when].filter((s) => s !== "");
  return (
    <button
      onClick={() => void openThread(info.parent_id)}
      className="mt-1.5 inline-flex items-center gap-1.5 text-[11.5px] text-text-faint hover:text-text-muted underline-offset-2 hover:underline"
    >
      {info.has_running_worker && <Dot status="running" />}
      <span>{segments.join(" · ")}</span>
    </button>
  );
}

function TaskAccessoryRow({ info }: { info: import("@/lib/types").TaskAccessoryInfo }) {
  const expandTaskInRail = useStore((s) => s.expandTaskInRail);
  const expandedTaskID = useStore((s) => s.expandedTaskID);
  const taskInScope = useTaskInActiveRail(info.task_id);
  const label = taskAccessoryLabel(info);
  const isRunning = info.status === "running" || info.status === "queued";
  const opened = expandedTaskID === info.task_id;
  const onClick = (e: React.MouseEvent) => {
    e.stopPropagation();
    if (!taskInScope) return;
    expandTaskInRail(info.task_id);
  };
  return (
    <button
      type="button"
      onClick={onClick}
      title={taskInScope ? undefined : "Task is outside current view"}
      className={cn(
        "mt-1.5 inline-flex items-center gap-1.5 text-[11.5px] cursor-pointer text-left",
        info.terminal ? "text-text-faint" : "text-text-muted",
        taskInScope ? "hover:text-text" : "cursor-help",
        opened && "text-text",
      )}
    >
      {isRunning && <Dot status="running" />}
      <span>{label}</span>
    </button>
  );
}

function useTaskInActiveRail(taskID: string): boolean {
  const participants = useStore((s) => s.participants);
  const threadDetail = useStore((s) => s.threadDetail);
  if (threadDetail && !threadDetail.unsupported && !threadDetail.not_found) {
    return (threadDetail.recent_runs || []).some((r) => r.id === taskID);
  }
  return (participants?.recent_runs || []).some((r) => r.id === taskID);
}

function taskAccessoryLabel(info: import("@/lib/types").TaskAccessoryInfo): string {
  const who = info.worker_display || info.worker_id || "worker";
  switch (info.status) {
    case "queued":
      return who + " · queued";
    case "running":
      return who + " · working...";
    case "finished":
      return who + " · finished";
    case "failed":
      return info.short_outcome
        ? who + " · failed: " + info.short_outcome
        : who + " · failed";
    case "canceled":
      return info.short_outcome
        ? who + " · canceled: " + info.short_outcome
        : who + " · canceled";
    case "no_output":
      return who + " · finished with no output";
    default:
      return who + " · " + info.status;
  }
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
      <main className="h-full grid grid-rows-[auto_1fr] bg-panel min-w-0">
        <div className="border-b border-border-soft px-8 py-3 flex items-center gap-3">
          <button onClick={() => closeThread()} className="text-[12px] text-text-muted hover:text-text">
            ← Back
          </button>
          <div className="text-[13px] text-text">Thread</div>
        </div>
        <div className="overflow-y-auto px-8 py-8 text-text-faint text-[13px]">
          {threadDetail.unsupported_hint || "Threads are not supported here."}
        </div>
      </main>
    );
  }
  if (threadDetail.not_found) {
    return (
      <main className="h-full grid grid-rows-[auto_1fr] bg-panel min-w-0">
        <div className="border-b border-border-soft px-8 py-3 flex items-center gap-3">
          <button onClick={() => closeThread()} className="text-[12px] text-text-muted hover:text-text">
            ← Back to {channel ? "#" + channel.name : "channel"}
          </button>
          <div className="text-[13px] text-text">Thread</div>
        </div>
        <div className="overflow-y-auto px-8 py-8 text-text-faint text-[13px]">
          Thread not found.
        </div>
      </main>
    );
  }

  const root = threadDetail.parent;
  const replies = threadDetail.replies || [];
  return (
    <main className="h-full grid grid-rows-[auto_1fr_auto] bg-panel min-w-0">
      <div className="border-b border-border-soft px-8 py-3 flex items-center gap-3">
        <button onClick={() => closeThread()} className="text-[12px] text-text-muted hover:text-text">
          ← Back to {channel ? "#" + channel.name : "channel"}
        </button>
        <div className="text-[13px] text-text font-medium">Thread</div>
        <div className="text-[12px] text-text-faint">
          {replies.length === 1 ? "1 reply" : replies.length + " replies"}
        </div>
      </div>
      <div className="overflow-y-auto px-8 pt-4 pb-6">
        <div className="mx-auto max-w-[800px]">
          {root && (
            <div className="border-l-2 border-l-border-soft pl-4 mb-4 pb-3 border-b border-border-soft">
              <div className="text-[11px] uppercase tracking-wide text-text-faint mb-1">Root message · context only</div>
              <MessageRow m={root} compact={false} />
            </div>
          )}
          {replies.length === 0 && (
            <div className="text-[12.5px] text-text-faint py-4">No replies yet. Send the first reply below.</div>
          )}
          {replies.map((m, i) => {
            const prev = i > 0 ? replies[i - 1] : null;
            const sameAuthor = prev && prev.role === m.role && (prev.author_id || "") === (m.author_id || "");
            const close = prev && new Date(m.time).getTime() - new Date(prev.time).getTime() < 5 * 60 * 1000;
            const compact = sameAuthor && close && !(m.events && m.events.length);
            return <MessageRow key={m.id} m={m} compact={!!compact} />;
          })}
        </div>
      </div>
      <Composer />
    </main>
  );
}

function Composer() {
  const view = useStore((s) => s.view);
  const channels = useStore((s) => s.channels);
  const activeChannel = useStore((s) => s.activeChannel);
  const activeThread = useStore((s) => s.activeThread);
  const agents = useStore((s) => s.agents);
  const activeAgent = useStore((s) => s.activeAgent);
  const detail = useStore((s) => s.detail);
  const models = useStore((s) => s.models);
  const sending = useStore((s) => s.sending);
  const send = useStore((s) => s.send);

  const [input, setInput] = useState("");
  const [persona, setPersona] = useState("");
  const [mentionState, setMentionState] = useState<{ start: number; query: string } | null>(null);
  const [mentionIndex, setMentionIndex] = useState(0);
  const textareaRef = useRef<HTMLTextAreaElement | null>(null);

  const updateMentionState = (value: string, caret: number) => {
    const before = value.slice(0, caret);
    const at = before.lastIndexOf("@");
    if (at < 0) {
      setMentionState(null);
      return;
    }
    const prevChar = at === 0 ? "" : before[at - 1];
    if (prevChar !== "" && !/\s/.test(prevChar)) {
      setMentionState(null);
      return;
    }
    const query = before.slice(at + 1);
    if (/\s/.test(query)) {
      setMentionState(null);
      return;
    }
    setMentionState({ start: at, query });
    setMentionIndex(0);
  };

  const closeMention = () => {
    setMentionState(null);
    setMentionIndex(0);
  };

  const mentionCandidates = (() => {
    if (!mentionState) return [] as { id: string; display: string }[];
    const q = mentionState.query.toLowerCase();
    return agents
      .filter((a) => {
        if (q === "") return true;
        return (
          a.id.toLowerCase().includes(q) || (a.display || "").toLowerCase().includes(q)
        );
      })
      .slice(0, 6);
  })();

  const acceptMention = (idx: number) => {
    if (!mentionState) return;
    const choice = mentionCandidates[idx];
    if (!choice) return;
    const before = input.slice(0, mentionState.start);
    const afterStart = mentionState.start + 1 + mentionState.query.length;
    const tail = input.slice(afterStart);
    const inserted = "@" + choice.id + (tail.startsWith(" ") ? "" : " ");
    const next = before + inserted + tail;
    const caret = before.length + inserted.length;
    setInput(next);
    closeMention();
    requestAnimationFrame(() => {
      const ta = textareaRef.current;
      if (ta) {
        ta.focus();
        ta.setSelectionRange(caret, caret);
      }
    });
  };
  const inferredPersona = (() => {
    if (view === "agent" && activeAgent) return activeAgent;
    if (view === "thread" && detail) {
      for (let i = detail.messages.length - 1; i >= 0; i--) {
        const m = detail.messages[i];
        if (m.role !== "user" && m.author_id) return m.author_id;
      }
    }
    return "";
  })();

  useEffect(() => {
    setPersona(inferredPersona);
  }, [view, activeAgent, activeChannel, activeThread, inferredPersona]);

  const threadDetail = useStore((s) => s.threadDetail);
  let placeholder = "Message...";
  if (threadDetail && !threadDetail.unsupported && !threadDetail.not_found) {
    placeholder = "Reply to thread...";
  } else if (view === "channel") {
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
  // P3.8/polish: surface a route hint only when the user looks
  // committed to the message — at least 5 typed characters — and
  // they're in a router-managed view without a '@'. Agent DM is
  // excluded; DM has its bound agent.
  const usesRouting = view === "channel" || view === "thread";
  const hasMention = /(^|\s)@/.test(input);
  const showRouteHint = usesRouting && trimmed.length >= 5 && !hasMention;

  const handleSend = async () => {
    if (!canSend) return;
    const text = trimmed;
    setInput("");
    await send(text, persona || undefined);
  };

  const handleKey = (e: React.KeyboardEvent<HTMLTextAreaElement>) => {
    if (mentionState && mentionCandidates.length > 0) {
      if (e.key === "ArrowDown") {
        e.preventDefault();
        setMentionIndex((i) => (i + 1) % mentionCandidates.length);
        return;
      }
      if (e.key === "ArrowUp") {
        e.preventDefault();
        setMentionIndex((i) => (i - 1 + mentionCandidates.length) % mentionCandidates.length);
        return;
      }
      if (e.key === "Enter" && !(e.metaKey || e.ctrlKey)) {
        e.preventDefault();
        acceptMention(mentionIndex);
        return;
      }
      if (e.key === "Tab") {
        e.preventDefault();
        acceptMention(mentionIndex);
        return;
      }
      if (e.key === "Escape") {
        e.preventDefault();
        closeMention();
        return;
      }
    }
    if ((e.metaKey || e.ctrlKey) && e.key === "Enter") {
      e.preventDefault();
      void handleSend();
    }
  };

  const handleChange = (e: React.ChangeEvent<HTMLTextAreaElement>) => {
    const value = e.target.value;
    setInput(value);
    updateMentionState(value, e.target.selectionStart ?? value.length);
  };

  const handleSelect = (e: React.SyntheticEvent<HTMLTextAreaElement>) => {
    const ta = e.currentTarget;
    updateMentionState(ta.value, ta.selectionStart ?? ta.value.length);
  };

  return (
    <div className="border-t border-border-soft px-8 pb-5 pt-3.5 bg-panel">
      <div className="mx-auto max-w-[800px]">
        {showRouteHint && (
          <div className="mb-2 text-[11.5px] text-text-faint">
            Mention an agent to route this message, or open a Direct Chat / DM.
          </div>
        )}
        <div className="relative">
          <textarea
            ref={textareaRef}
            rows={2}
            placeholder={placeholder}
            value={input}
            onChange={handleChange}
            onSelect={handleSelect}
            onKeyDown={handleKey}
            onBlur={() => {
              // dismiss after a short delay to let click on suggestion register
              setTimeout(() => closeMention(), 120);
            }}
            disabled={sending}
            className="w-full min-h-[76px] resize-none rounded-md border border-border bg-panel px-3.5 py-3 text-[14px] leading-[1.55] text-text outline-none transition-[border,box-shadow] hover:border-border-strong focus:border-accent focus:ring-[3px] focus:ring-accent-bg disabled:opacity-70"
          />
          {mentionState &&
            (mentionCandidates.length > 0 ? (
              <div className="absolute left-0 bottom-full mb-1.5 z-30 w-[280px] max-h-[260px] overflow-y-auto rounded-md border border-border bg-panel shadow-[0_-8px_24px_rgba(31,41,51,0.10)] py-1 text-[13px]">
                {mentionCandidates.map((a, i) => (
                  <button
                    key={a.id}
                    type="button"
                    onMouseDown={(e) => {
                      // mouseDown so blur doesn't fire first
                      e.preventDefault();
                      acceptMention(i);
                    }}
                    onMouseEnter={() => setMentionIndex(i)}
                    className={cn(
                      "flex w-full items-center gap-2 px-3 py-1.5 text-left",
                      i === mentionIndex ? "bg-panel-2" : "hover:bg-panel-2",
                    )}
                  >
                    <span className="text-text-faint">@</span>
                    <span className="text-text">{a.id}</span>
                    {a.display && a.display !== a.id && (
                      <span className="ml-auto text-[11.5px] text-text-faint">{a.display}</span>
                    )}
                  </button>
                ))}
              </div>
            ) : (
              <div className="absolute left-0 bottom-full mb-1.5 z-30 w-[280px] rounded-md border border-border bg-panel shadow-[0_-8px_24px_rgba(31,41,51,0.10)] py-1.5 px-3 text-[12px] text-text-faint">
                No agent matches "{mentionState.query}"
              </div>
            ))}
        </div>
        <div className="mt-2.5 flex items-center gap-2">
          {view === "agent" ? (
            <span className="text-[12px] text-text-muted px-1.5 py-1">
              {(() => {
                const ag = agents.find((a) => a.id === activeAgent);
                return ag ? "@" + ag.display : "";
              })()}
            </span>
          ) : null}
          <select className="bg-transparent text-[12px] text-text-muted px-1.5 py-1 rounded-sm hover:text-text outline-none cursor-pointer">
            {models.map((m) => (
              <option key={m.name} value={m.name}>
                {m.model}
              </option>
            ))}
          </select>
          <span className="flex-1" />
          <Button
            variant="default"
            disabled={!canSend}
            onClick={() => void handleSend()}
            className={cn(
              "transition-colors",
              canSend && "border-border-strong text-text",
            )}
          >
            {sending ? "Sending…" : "Send"}
          </Button>
        </div>
      </div>
    </div>
  );
}
