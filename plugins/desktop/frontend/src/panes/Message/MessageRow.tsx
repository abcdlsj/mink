import { useMemo, useState } from "react";
import type { EventBlock as EventBlockData, MemoryCommitAttachment, MessageView } from "@/lib/types";
import { EventBlock, toolEventSummary } from "@/components/EventBlock";
import { Identicon } from "@/components/Identicon";
import { Markdown } from "@/components/Markdown";
import { renderMentions } from "@/components/Mention";
import { useStore } from "@/lib/store";
import { cn, relTime } from "@/lib/utils";
import { ReasoningPreface } from "./ReasoningPreface";
import { TaskAccessoryRow } from "./TaskAccessory";
import { TaskCandidate } from "./TaskCandidate";
import { ThreadAction, ThreadLink } from "./ThreadAccessory";
import { personaForActiveAgent, shortRole, stripCollabLeak } from "./message-helpers";
import { parseTranscriptAttachment, type SumiTranscript } from "./message-transcript";

const LONG_CONTENT_CHARS = 2600;
const LONG_CONTENT_LINES = 48;

export function MessageRow({
  m,
  compact,
  threadStartsEnabled = false,
  selecting = false,
  selected = false,
  highlighted = false,
  onToggleSelected,
}: {
  m: MessageView;
  compact: boolean;
  threadStartsEnabled?: boolean;
  selecting?: boolean;
  selected?: boolean;
  highlighted?: boolean;
  onToggleSelected?: () => void;
}) {
  const agents = useStore((s) => s.agents);
  const agentDMs = useStore((s) => s.agentDMs);
  const view = useStore((s) => s.view);
  const activeAgentSpace = useStore((s) => s.activeAgentSpace);
  const detail = useStore((s) => s.detail);
  const retryMessage = useStore((s) => s.retryMessage);
  const threadDetail = useStore((s) => s.threadDetail);
  const openCurrentRoute = useStore((s) => s.openCurrentRoute);

  const dmAgent = view === "agent" && m.role !== "user"
    ? personaForActiveAgent(agents, agentDMs, activeAgentSpace, detail?.item.persona_id)
    : undefined;

  const ag = dmAgent || agents.find((a) => a.id === m.author_id);
  const routedReply = m.auto_reply_reason && m.role !== "user"
    ? routedReplySignal(m.auto_reply_reason, ag?.id || m.author_id || "", ag?.display || m.author_name || "Agent", m.mentions)
    : null;
  const knownMentions = useMemo(
    () => new Set(agents.map((a) => a.id)),
    [agents],
  );
  const seed = m.role === "user"
    ? "user"
    : (dmAgent?.id || m.author_id || m.author_name || "agent");
  const kind = m.role === "user" ? "user" : "agent";
  const displayName = m.role === "user"
    ? "You"
    : m.role === "system"
      ? "System"
    : (dmAgent?.display || m.author_name || ag?.display || "Sumi");

  const events = m.events || [];
  const collabEvents = events.filter((e) => e.kind === "mention" || e.kind === "delegate");
  const toolEvents = events.filter((e) => e.kind === "tool_call");
  const noticeEvents = events.filter((e) => e.kind === "service_notice");
  const shouldFoldTools = toolEvents.length > 1;
  const spaceID = threadDetail?.space_id || detail?.item.id || "";
  const canStartThread =
    threadStartsEnabled &&
    view === "channel" &&
    m.role !== "system" &&
    !m.is_thread_reply &&
    !m.thread_id &&
    !m.thread_info &&
    (!!m.content?.trim() || events.length > 0);
  const threadAction = (m.thread_info || canStartThread)
    ? <ThreadAction info={m.thread_info} messageID={canStartThread ? m.id : undefined} />
    : null;

  if (m.role === "system") {
    return (
      <div
        id={"message-" + m.id}
        className={cn(
          "group/message grid gap-2",
          selecting ? "grid-cols-[22px_1fr]" : "grid-cols-1",
          compact ? "mb-2" : "mb-4",
          selected && "bg-accent-bg/60 outline outline-1 outline-accent-border",
          highlighted && "sumi-anchor-flash",
        )}
      >
        {selecting && (
          <label className="mt-1 flex justify-center">
            <input
              type="checkbox"
              checked={selected}
              onChange={onToggleSelected}
              className="size-3.5 accent-accent"
              aria-label={"Select system notice " + m.id}
            />
          </label>
        )}
        <div className="max-w-[820px] border border-border-soft border-l-4 border-l-text-faint bg-bg px-3 py-2 text-text">
          <div className="mb-1.5 flex items-center gap-2 font-mono text-[10.5px] uppercase tracking-[0.35px] text-text-faint">
            <span>System notice</span>
            <span className="ml-auto tabular-nums">{relTime(m.time)}</span>
          </div>
          {m.content && (
            <LongContent
              content={m.content}
              isUser={false}
              mentions={knownMentions}
            />
          )}
          {events.length > 0 && (
            <div className="mt-2.5 flex flex-col gap-1.5">
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
          {m.status === "failed" && (
            <div className="mt-2 border border-error-border bg-error-bg px-2 py-1 font-mono text-[10.5px] text-error">
              {m.error || "System action failed."}
            </div>
          )}
        </div>
      </div>
    );
  }

  return (
    <div
      id={"message-" + m.id}
      className={cn(
        "group/message grid gap-2.5 md:gap-3.5",
        selecting ? "grid-cols-[22px_28px_1fr] md:grid-cols-[22px_32px_1fr]" : "grid-cols-[28px_1fr] md:grid-cols-[32px_1fr]",
        compact ? "-mt-2.5 mb-2" : "mb-6 pb-1",
        selected && "bg-accent-bg/60 outline outline-1 outline-accent-border",
        highlighted && "sumi-anchor-flash",
      )}
    >
      {selecting && (
        <label className={cn("mt-1 flex justify-center", compact && "mt-0")}>
          <input
            type="checkbox"
            checked={selected}
            onChange={onToggleSelected}
            className="size-3.5 accent-accent"
            aria-label={"Select message " + m.id}
          />
        </label>
      )}
      <div
        className={cn(
          "mt-px size-7 overflow-hidden border border-border-soft bg-panel md:size-8",
          compact && "invisible",
        )}
      >
        <Identicon seed={seed} kind={kind} />
      </div>
      <div className={cn("relative min-w-0", compact && threadAction && "pr-7")}>
        {!compact && (
          <div className="mb-1 flex min-w-0 items-center gap-2">
            <div className="flex min-w-0 items-baseline gap-2">
              <span className="min-w-0 truncate font-display text-[13px] font-bold leading-tight text-text">
                {displayName}
              </span>
              {m.role !== "user" && ag?.role && (
                <span
                  className="shrink-0 border border-border-soft bg-transparent px-1 font-mono text-[10px] font-medium uppercase text-text-muted"
                  title={ag.role}
                >
                  {shortRole(ag.role)}
                </span>
              )}
            </div>
            <div className="ml-auto flex shrink-0 items-center gap-1.5">
              <span className="font-mono text-[11px] text-text-faint tabular-nums">{relTime(m.time)}</span>
              {threadAction}
            </div>
          </div>
        )}
        {compact && threadAction && (
          <div className="absolute right-0 top-0">
            {threadAction}
          </div>
        )}
        {m.reasoning && m.role !== "user" && <ReasoningPreface text={m.reasoning} />}
        {routedReply && (
          <div
            className="mb-1.5 flex flex-wrap gap-1"
            title={routedReply.detail}
            aria-label={routedReply.detail}
          >
            {routedReply.labels.map((label, i) => (
              <span
                key={label + i}
                className="inline-flex border border-border-soft bg-transparent px-1.5 py-px font-mono text-[10.5px] font-medium text-text-muted"
              >
                {label}
              </span>
            ))}
          </div>
        )}
        {m.content && (
          <LongContent
            content={m.role === "user" ? m.content : stripCollabLeak(m.content)}
            isUser={m.role === "user"}
            className={cn(m.reasoning && "mt-2")}
            mentions={knownMentions}
          />
        )}
        <QuotedTranscripts transcripts={(m.attachments || [])
          .filter((a) => a.kind === "quoted_transcript")
          .map((a) => parseTranscriptAttachment(a.data))
          .filter((t): t is SumiTranscript => !!t)}
          onJump={() => void openCurrentRoute()}
        />
        <MemoryCommitCards cards={(m.attachments || [])
          .filter((a) => a.kind === "memory_commit")
          .map((a) => parseMemoryCommitAttachment(a.data))
          .filter((card): card is MemoryCommitAttachment => !!card)}
        />
        {events.length > 0 && (
          <div className="mt-2.5 flex flex-col gap-1.5">
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
        {m.status === "pending" && (
          <div className="mt-2.5 inline-flex items-center gap-2 border border-running-border bg-running-bg px-2 py-1 text-[11.5px] text-running">
            <span className="inline-block size-1.5 rounded-full bg-running" />
            <span>Reply in progress.</span>
          </div>
        )}
        {m.status === "failed" && (
          <div className="mt-2.5 flex flex-wrap items-center gap-2 border border-error-border border-l-4 bg-error-bg px-2.5 py-2 text-[12px] text-error">
            <span className="leading-[18px]">
              {m.error || "Reply stopped before it finished."}
              <span className="ml-1 text-text-faint">Retry reruns this reply from the existing user message; it will not duplicate your message.</span>
            </span>
            {spaceID && (
              <button
                type="button"
                onClick={() => void retryMessage(spaceID, m.id)}
                className="border border-error-border bg-panel px-2 py-0.5 font-mono text-[10.5px] font-semibold text-error hover:bg-error-bg"
              >
                Retry
              </button>
            )}
          </div>
        )}
        {m.thread_id && m.thread_summary && (
          <ThreadLink threadId={m.thread_id} summary={m.thread_summary} />
        )}
        {!m.task_accessory && m.role === "user" && <TaskCandidate message={m} forceMainScope={threadStartsEnabled} />}
        {m.task_accessory && <TaskAccessoryRow info={m.task_accessory} />}
        {m.role !== "user" && m.usage && <UsageFooter usage={m.usage} />}
      </div>
    </div>
  );
}

function MemoryCommitCards({ cards }: { cards: MemoryCommitAttachment[] }) {
  if (cards.length === 0) return null;
  return (
    <div className="mt-2.5 grid max-w-[640px] gap-2">
      {cards.map((card, idx) => (
        <div
          key={idx}
          className={cn(
            "overflow-hidden border bg-panel-2 text-text shadow-card",
            card.status === "failed" ? "border-error-border" : "border-agent-border",
          )}
        >
          <div className="grid grid-cols-[6px_1fr] border-b border-border-soft bg-panel">
            <div className={card.status === "failed" ? "bg-error" : "bg-agent"} />
            <div className="min-w-0 px-2.5 py-2">
              <div className="flex min-w-0 items-center gap-2">
                <span
                  className={cn(
                    "shrink-0 font-mono text-[10.5px] font-extrabold uppercase tracking-[0.45px]",
                    card.status === "failed" ? "text-error" : "text-agent",
                  )}
                >
                  {card.status === "failed" ? "Memory failed" : "Memory saved"}
                </span>
                <span className="min-w-0 truncate text-[12.5px] font-bold text-text">
                  {card.title || "Untitled memory"}
                </span>
              </div>
              <div className="mt-0.5 flex flex-wrap items-center gap-1.5 font-mono text-[10.5px] text-text-muted">
                <span>{memoryScopeLabel(card)}</span>
                {card.kind && <span>· {card.kind}</span>}
                {card.confidence && <span>· {card.confidence}</span>}
                {card.memory_id && <span>· {card.memory_id}</span>}
              </div>
            </div>
          </div>
          <div className="grid gap-1.5 px-2.5 py-2">
            <div className="whitespace-pre-wrap break-words border border-border-soft bg-bg px-2 py-1.5 text-[12px] leading-[1.5]">
              {card.body || "(empty)"}
            </div>
            {card.reason && (
              <div className="font-mono text-[10.5px] text-text-faint">Reason: {card.reason}</div>
            )}
            {card.error && (
              <div className="border border-error-border bg-error-bg px-2 py-1 font-mono text-[10.5px] text-error">
                {card.error}
              </div>
            )}
          </div>
        </div>
      ))}
    </div>
  );
}

function parseMemoryCommitAttachment(data: string | undefined): MemoryCommitAttachment | null {
  if (!data) return null;
  try {
    const parsed = JSON.parse(data) as Partial<MemoryCommitAttachment>;
    if (typeof parsed.title !== "string" || typeof parsed.body !== "string") return null;
    return {
      status: parsed.status || "remembered",
      scope_kind: parsed.scope_kind || "global",
      scope_key: parsed.scope_key,
      title: parsed.title,
      body: parsed.body,
      kind: parsed.kind,
      reason: parsed.reason,
      confidence: parsed.confidence,
      memory_id: parsed.memory_id,
      notice: parsed.notice,
      error: parsed.error,
      created_by: parsed.created_by,
    };
  } catch {
    return null;
  }
}

function memoryScopeLabel(card: MemoryCommitAttachment): string {
  return card.scope_key ? `${card.scope_kind}:${card.scope_key}` : card.scope_kind;
}

function QuotedTranscripts({
  transcripts,
  onJump,
}: {
  transcripts: SumiTranscript[];
  onJump?: () => void;
}) {
  if (transcripts.length === 0) return null;
  return (
    <div className="mt-2.5 grid max-w-[640px] gap-2">
      {transcripts.map((t, idx) => (
        <TranscriptQuoteCard key={idx} transcript={t} onJump={onJump} />
      ))}
    </div>
  );
}

function TranscriptQuoteCard({
  transcript,
  onJump,
}: {
  transcript: SumiTranscript;
  onJump?: () => void;
}) {
  const [open, setOpen] = useState(false);
  const preview = open ? transcript.messages : transcript.messages.slice(0, 3);
  const hidden = transcript.messages.length - preview.length;
  const sourceLink = transcript.messages.find((m) => m.link)?.link;
  const range = transcriptTimeRange(transcript.messages);
  return (
    <div className="overflow-hidden border border-border bg-panel-2 text-text shadow-card">
      <div className="grid grid-cols-[6px_1fr] border-b border-border-soft bg-panel">
        <div className="bg-action" />
        <div className="min-w-0 px-2.5 py-2">
          <div className="flex min-w-0 items-center gap-2">
            <span className="shrink-0 font-mono text-[10.5px] font-extrabold uppercase tracking-[0.45px] text-action">
              Transcript quote
            </span>
            <span className="min-w-0 truncate text-[12.5px] font-bold text-text">
              {transcript.title}
            </span>
          </div>
          <div className="mt-0.5 flex min-w-0 flex-wrap items-center gap-1.5 font-mono text-[10.5px] text-text-muted">
            <span>{transcript.messages.length} messages</span>
            <span>·</span>
            {range && (
              <>
                <span>{range}</span>
                <span>·</span>
              </>
            )}
            {sourceLink ? (
              <a href={sourceLink} className="min-w-0 truncate text-action underline underline-offset-2 hover:text-text">
                {transcript.source}
              </a>
            ) : (
              <span className="min-w-0 truncate">{transcript.source}</span>
            )}
          </div>
        </div>
      </div>
      <div className="grid gap-1.5 px-2.5 py-2">
        {preview.map((m) => (
          <div key={m.id} className="grid grid-cols-[72px_1fr_auto] items-start gap-2 rounded-sm border border-border-soft bg-bg px-2 py-1.5 text-[12px] leading-[1.45]">
            <div className="min-w-0">
              <div className="truncate font-mono text-[10.5px] font-bold text-text">{m.sender}</div>
              <div className="truncate font-mono text-[10px] text-text-faint">{compactQuoteTime(m.time)}</div>
            </div>
            <div className="min-w-0 whitespace-pre-wrap break-words text-text">
              {quotePreviewText(m.content)}
            </div>
            {m.link && (
              <a
                href={m.link}
                onClick={(ev) => {
                  ev.preventDefault();
                  pushTranscriptLink(m.link);
                  onJump?.();
                }}
                className="shrink-0 border border-border-soft bg-panel px-1.5 py-px font-mono text-[10px] font-semibold uppercase text-text-muted opacity-80 hover:border-action hover:text-action"
              >
                jump
              </a>
            )}
          </div>
        ))}
        {hidden > 0 && (
          <button
            type="button"
            onClick={() => setOpen(true)}
            className="justify-self-start border border-border-soft bg-panel px-2 py-0.5 font-mono text-[10.5px] font-semibold text-text-muted hover:border-border hover:text-text"
          >
            Show {hidden} more
          </button>
        )}
        {open && transcript.messages.length > 3 && (
          <button
            type="button"
            onClick={() => setOpen(false)}
            className="justify-self-start border border-border-soft bg-panel px-2 py-0.5 font-mono text-[10.5px] font-semibold text-text-muted hover:border-border hover:text-text"
          >
            Collapse transcript
          </button>
        )}
      </div>
    </div>
  );
}

function pushTranscriptLink(link: string) {
  try {
    const next = new URL(link, window.location.href);
    if (next.origin !== window.location.origin) {
      window.location.href = link;
      return;
    }
    window.history.pushState({}, "", next.pathname + next.search + next.hash);
  } catch {
    window.location.href = link;
  }
}

function quotePreviewText(content: string): string {
  const text = content.trim() || "(empty)";
  if (text.length <= 180) return text;
  return text.slice(0, 180).trimEnd() + "…";
}

function compactQuoteTime(time: string): string {
  const parsed = new Date(time);
  if (Number.isNaN(parsed.getTime())) return time;
  return parsed.toLocaleString([], {
    month: "2-digit",
    day: "2-digit",
    hour: "2-digit",
    minute: "2-digit",
  });
}

function transcriptTimeRange(messages: SumiTranscript["messages"]): string {
  if (messages.length === 0) return "";
  const first = compactQuoteTime(messages[0].time);
  const last = compactQuoteTime(messages[messages.length - 1].time);
  if (!first) return last;
  if (!last || first === last) return first;
  return first + " - " + last;
}

function LongContent({
  content,
  isUser,
  className,
  mentions,
}: {
  content: string;
  isUser: boolean;
  className?: string;
  mentions: Set<string>;
}) {
  const [open, setOpen] = useState(false);
  const long = isLongContent(content);
  const visible = long && !open ? collapsedContent(content) : content;
  const hiddenChars = content.length - visible.length;
  return (
    <div className={cn("relative", className)}>
      {isUser ? (
        <div className="message-prose max-w-[820px] whitespace-pre-wrap break-words text-[14px] leading-[22px] text-text">
          {renderMentions(visible, mentions)}
        </div>
      ) : (
        <Markdown
          className="message-prose max-w-[820px] text-[14px] leading-[22px] text-text"
          mentions={mentions}
        >
          {visible}
        </Markdown>
      )}
      {long && (
        <div className={cn(
          "mt-2 flex items-center gap-2 border-t border-border-soft pt-2",
          !open && "relative",
        )}>
          <button
            type="button"
            onClick={() => setOpen((v) => !v)}
            className="border border-border-soft bg-panel-2 px-2 py-1 font-mono text-[11px] font-semibold text-text-muted hover:border-border hover:text-text"
          >
            {open ? "Collapse" : "Show full message"}
          </button>
          {!open && hiddenChars > 0 && (
            <span className="font-mono text-[10.5px] text-text-faint">
              {formatHiddenChars(hiddenChars)} hidden
            </span>
          )}
        </div>
      )}
    </div>
  );
}

function isLongContent(content: string): boolean {
  if (content.length > LONG_CONTENT_CHARS) return true;
  return content.split("\n").length > LONG_CONTENT_LINES;
}

function collapsedContent(content: string): string {
  if (!isLongContent(content)) return content;
  const lines = content.split("\n");
  const byLines = lines.length > LONG_CONTENT_LINES
    ? lines.slice(0, LONG_CONTENT_LINES).join("\n")
    : content;
  if (byLines.length <= LONG_CONTENT_CHARS) return byLines.trimEnd();
  return byLines.slice(0, LONG_CONTENT_CHARS).trimEnd();
}

function formatHiddenChars(n: number): string {
  if (n >= 1000) return (Math.round(n / 100) / 10) + "k chars";
  return n + " chars";
}

function ToolFold({ events }: { events: EventBlockData[] }) {
  const [open, setOpen] = useState(false);
  const totalMs = events.reduce((sum, e) => sum + (e.duration_ms || 0), 0);
  const anyRunning = events.some((e) => e.status === "running");
  const anyError = events.some((e) => e.status === "error");
  const status = anyRunning ? "running" : anyError ? "error" : "done";
  const actionSummary = foldedToolSummary(events);
  const label = anyError ? "Tool failed" : anyRunning ? "Using tools" : "Tool finished";
  const duration = totalMs ? (totalMs >= 1000 ? Math.round(totalMs / 100) / 10 + "s" : totalMs + "ms") : "";
  if (open) {
    return (
      <div className="flex flex-col gap-1">
        <button
          onClick={() => setOpen(false)}
          className={cn(
            "self-start cursor-pointer font-mono text-[11.5px] underline underline-offset-2",
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
        "self-start cursor-pointer text-[11.5px]",
        status === "error" ? "text-error" : "text-text-muted",
      )}
    >
      <span
        className={cn(
          "mr-1.5 inline-block size-1.5 rounded-full align-middle",
          status === "error" ? "bg-error" : status === "running" ? "bg-running" : "bg-text-faint",
        )}
      />
      <span>{label}</span>
      {actionSummary && (
        <>
          <span className="text-text-faint"> · </span>
          <span>{actionSummary}</span>
        </>
      )}
      {duration && (
        <>
          <span className="text-text-faint"> · </span>
          <span className="text-text-faint tabular-nums">{duration}</span>
        </>
      )}
      <span className="text-text-faint"> · </span>
      <span className="underline underline-offset-2 text-text-faint">view details</span>
    </button>
  );
}

function foldedToolSummary(events: EventBlockData[]): string {
  const parts = events
    .map((ev) => toolEventSummary(ev))
    .filter(Boolean)
    .filter((part, index, all) => all.indexOf(part) === index)
    .slice(0, 2);
  const text = parts.join("; ");
  if (text.length <= 120) return text;
  return text.slice(0, 119).trimEnd() + "…";
}

function UsageFooter({ usage }: { usage: NonNullable<MessageView["usage"]> }) {
  const parts: string[] = [];
  if (usage.model) parts.push(usage.model);
  if (usage.input || usage.output) {
    parts.push(formatTokens(usage.input) + " in / " + formatTokens(usage.output) + " out");
  } else if (usage.total) {
    parts.push(formatTokens(usage.total) + " tokens");
  }
  if (usage.cost_usd && usage.cost_usd > 0) {
    parts.push("$" + usage.cost_usd.toFixed(4));
  }
  if (parts.length === 0) return null;
  return (
    <div className="mt-1.5 text-[11px] font-mono text-text-faint">{parts.join(" · ")}</div>
  );
}

function formatTokens(n: number): string {
  if (n >= 1000) return (Math.round(n / 100) / 10) + "k";
  return String(n);
}

function routedReplySignal(reason: string, agentID: string, agentName: string, mentions?: string[]): { labels: string[]; detail: string } {
  const raw = reason.trim();
  const lower = raw.toLowerCase();
  const agentHandle = handle(agentID || agentName);
  if (lower.includes("listening")) {
    return {
      labels: ["listening"],
      detail: `${agentName} joined from listening.`,
    };
  }
  if (lower.startsWith("called by @")) {
    const caller = raw.slice("called by ".length);
    const next = (mentions || []).filter(Boolean);
    if (next.length > 0) {
      return {
        labels: [`${caller} → ${agentHandle}`, ...next.map((id) => `${agentHandle} → ${handle(id)}`)],
        detail: `${agentName} was called by ${caller} and called ${next.map(handle).join(", ")}.`,
      };
    }
    return {
      labels: [`${caller} → ${agentHandle}`, "ended"],
      detail: `${agentName} was called by ${caller}; no further agent was called.`,
    };
  }
  if (lower === "called by mention" || lower === "explicit mention") {
    return {
      labels: [`mention → ${agentHandle}`],
      detail: `${agentName} was called by mention.`,
    };
  }
  if (lower === "agent mention" || lower === "called by another agent") {
    return {
      labels: [`agent → ${agentHandle}`],
      detail: `${agentName} was called by another agent.`,
    };
  }
  return {
    labels: [raw],
    detail: `${agentName}: ${raw}.`,
  };
}

function handle(id: string): string {
  const value = id.trim();
  if (!value) return "@agent";
  return value.startsWith("@") ? value : "@" + value;
}
