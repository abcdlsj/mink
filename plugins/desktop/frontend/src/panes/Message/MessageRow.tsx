import { useMemo, useState } from "react";
import type { EventBlock as EventBlockData, MessageView } from "@/lib/types";
import { EventBlock } from "@/components/EventBlock";
import { Identicon } from "@/components/Identicon";
import { Markdown } from "@/components/Markdown";
import { renderMentions } from "@/components/Mention";
import { useStore } from "@/lib/store";
import { cn, relTime } from "@/lib/utils";
import { ReasoningPreface } from "./ReasoningPreface";
import { TaskAccessoryRow } from "./TaskAccessory";
import { ThreadLink, ThreadSummaryRow } from "./ThreadAccessory";
import { personaForActiveAgent, shortRole, stripCollabLeak } from "./message-helpers";

export function MessageRow({ m, compact }: { m: MessageView; compact: boolean }) {
  const agents = useStore((s) => s.agents);
  const agentDMs = useStore((s) => s.agentDMs);
  const view = useStore((s) => s.view);
  const activeAgent = useStore((s) => s.activeAgent);

  const dmAgent = view === "agent" && m.role !== "user"
    ? personaForActiveAgent(agents, agentDMs, activeAgent)
    : undefined;

  const ag = dmAgent || agents.find((a) => a.id === m.author_id);
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

  return (
    <div
      id={"message-" + m.id}
      className={cn(
        "grid grid-cols-[28px_1fr] gap-2.5 md:grid-cols-[32px_1fr] md:gap-3.5",
        compact ? "-mt-4 mb-1" : "mb-6 border-b border-border-soft pb-5 last:border-b-0",
      )}
    >
      <div
        className={cn(
          "mt-px size-7 overflow-hidden border-2 border-border bg-panel md:size-8",
          compact && "invisible",
        )}
      >
        <Identicon seed={seed} kind={kind} />
      </div>
      <div className="min-w-0">
        {!compact && (
          <div className="mb-1.5 flex items-baseline gap-2">
            <span className="font-display text-[13.5px] font-black text-text">
              {displayName}
            </span>
            {m.role !== "user" && ag?.role && (
              <span
                className="border border-border bg-panel-event px-1 font-display text-[10px] font-semibold uppercase tracking-[0.5px] text-text"
                title={ag.role}
              >
                {shortRole(ag.role)}
              </span>
            )}
            <span className="font-mono text-[11px] text-text-faint tabular-nums">{relTime(m.time)}</span>
          </div>
        )}
        {m.reasoning && m.role !== "user" && <ReasoningPreface text={m.reasoning} />}
        {m.auto_reply_reason && m.role !== "user" && (
          <div className="mb-1 inline-flex border border-border bg-accent-bg px-1.5 py-px text-[11.5px] text-text">
            {(ag?.display || m.author_name || "Agent")} joined from channel listening.
          </div>
        )}
        {m.content && (
          m.role === "user" ? (
            <div
              className={cn(
                "max-w-full whitespace-pre-wrap break-words text-[14px] leading-[1.65] text-text md:text-[14.5px] md:leading-[1.7]",
                m.reasoning && "mt-2",
              )}
            >
              {renderMentions(m.content, knownMentions)}
            </div>
          ) : (
            <Markdown
              className={cn(
                "max-w-full text-[14px] leading-[1.65] text-text md:text-[14.5px] md:leading-[1.7]",
                m.reasoning && "mt-2",
              )}
              mentions={knownMentions}
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
        {m.role !== "user" && m.usage && <UsageFooter usage={m.usage} />}
      </div>
    </div>
  );
}

function ToolFold({ events }: { events: EventBlockData[] }) {
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
        "self-start cursor-pointer text-[12px] underline underline-offset-2",
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
        "self-start cursor-pointer text-[12px]",
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
