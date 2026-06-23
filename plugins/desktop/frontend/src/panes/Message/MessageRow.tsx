import { useMemo, useState } from "react";
import type { EventBlock as EventBlockData, MessageView } from "@/lib/types";
import { EventBlock, toolEventSummary } from "@/components/EventBlock";
import { Identicon } from "@/components/Identicon";
import { Markdown } from "@/components/Markdown";
import { renderMentions } from "@/components/Mention";
import { useStore } from "@/lib/store";
import { cn, relTime } from "@/lib/utils";
import { ReasoningPreface } from "./ReasoningPreface";
import { TaskAccessoryRow } from "./TaskAccessory";
import { TaskCandidate } from "./TaskCandidate";
import { ThreadLink, ThreadSummaryRow } from "./ThreadAccessory";
import { personaForActiveAgent, shortRole, stripCollabLeak } from "./message-helpers";

const LONG_CONTENT_CHARS = 2600;
const LONG_CONTENT_LINES = 48;

export function MessageRow({ m, compact }: { m: MessageView; compact: boolean }) {
  const agents = useStore((s) => s.agents);
  const agentDMs = useStore((s) => s.agentDMs);
  const view = useStore((s) => s.view);
  const activeAgentSpace = useStore((s) => s.activeAgentSpace);
  const detail = useStore((s) => s.detail);
  const retryMessage = useStore((s) => s.retryMessage);
  const threadDetail = useStore((s) => s.threadDetail);

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
    : (dmAgent?.display || m.author_name || ag?.display || "Sumi");

  const events = m.events || [];
  const collabEvents = events.filter((e) => e.kind === "mention" || e.kind === "delegate");
  const toolEvents = events.filter((e) => e.kind === "tool_call");
  const noticeEvents = events.filter((e) => e.kind === "service_notice");
  const shouldFoldTools = toolEvents.length > 1;
  const spaceID = threadDetail?.space_id || detail?.item.id || "";

  return (
    <div
      id={"message-" + m.id}
      className={cn(
        "grid grid-cols-[28px_1fr] gap-2.5 md:grid-cols-[32px_1fr] md:gap-3.5",
        compact ? "-mt-2.5 mb-2" : "mb-6 pb-1",
      )}
    >
      <div
        className={cn(
          "mt-px size-7 overflow-hidden border border-border-soft bg-panel md:size-8",
          compact && "invisible",
        )}
      >
        <Identicon seed={seed} kind={kind} />
      </div>
      <div className="min-w-0">
        {!compact && (
          <div className="mb-1 flex items-baseline gap-2">
            <span className="font-display text-[13px] font-bold leading-tight text-text">
              {displayName}
            </span>
            {m.role !== "user" && ag?.role && (
              <span
                className="border border-border-soft bg-transparent px-1 font-mono text-[10px] uppercase text-text-faint"
                title={ag.role}
              >
                {shortRole(ag.role)}
              </span>
            )}
            <span className="font-mono text-[11px] text-text-faint tabular-nums">{relTime(m.time)}</span>
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
                className="inline-flex border border-border-soft bg-transparent px-1.5 py-px font-mono text-[10.5px] text-text-faint"
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
          <div className="mt-2.5 inline-flex items-center gap-2 text-[11.5px] text-text-muted">
            <span className="inline-block size-1.5 rounded-full bg-running" />
            <span>Reply in progress.</span>
          </div>
        )}
        {m.status === "failed" && (
          <div className="mt-2.5 flex flex-wrap items-center gap-2 border border-error border-l-4 bg-panel px-2.5 py-2 text-[12px] text-error">
            <span className="leading-[18px]">
              {m.error || "Reply stopped before it finished."}
              <span className="ml-1 text-text-faint">Retry reruns this reply from the existing user message; it will not duplicate your message.</span>
            </span>
            {spaceID && (
              <button
                type="button"
                onClick={() => void retryMessage(spaceID, m.id)}
                className="border border-error bg-bg px-2 py-0.5 font-mono text-[10.5px] font-semibold text-error hover:bg-panel-2"
              >
                Retry
              </button>
            )}
          </div>
        )}
        {m.thread_id && m.thread_summary && (
          <ThreadLink threadId={m.thread_id} summary={m.thread_summary} />
        )}
        {!m.task_accessory && m.role === "user" && <TaskCandidate message={m} />}
        {m.thread_info && <ThreadSummaryRow info={m.thread_info} />}
        {m.task_accessory && <TaskAccessoryRow info={m.task_accessory} />}
        {m.role !== "user" && m.usage && <UsageFooter usage={m.usage} />}
      </div>
    </div>
  );
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
        <div className="message-prose max-w-full whitespace-pre-wrap break-words text-[14px] leading-[22px] text-text">
          {renderMentions(visible, mentions)}
        </div>
      ) : (
        <Markdown
          className="message-prose max-w-full text-[14px] leading-[22px] text-text"
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
