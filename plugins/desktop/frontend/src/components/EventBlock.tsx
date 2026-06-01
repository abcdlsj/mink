import { useEffect, useRef, useState } from "react";
import { AlertTriangle, AtSign, ArrowRight } from "lucide-react";
import { cn } from "@/lib/utils";
import { Markdown } from "@/components/Markdown";
import type { EventBlock as EventBlockData, EventStatus } from "@/lib/types";

interface EventBlockProps {
  ev: EventBlockData;
}

export function EventBlock({ ev }: EventBlockProps) {
  if (ev.kind === "service_notice") return <ServiceLine ev={ev} />;
  if (ev.kind === "reasoning") return null;
  if (ev.kind === "mention") return <MentionLine ev={ev} />;
  if (ev.kind === "delegate") return <DelegateLine ev={ev} />;
  return <ToolLine ev={ev} />;
}

function ServiceLine({ ev }: EventBlockProps) {
  const isError = ev.status === "error";
  return (
    <div
      className={cn(
        "flex items-center gap-1.5 py-0.5 text-[12px]",
        isError ? "text-error" : "text-text-faint",
      )}
    >
      {isError && <AlertTriangle className="size-3 shrink-0" />}
      <span>{ev.output || ""}</span>
    </div>
  );
}

function ToolLine({ ev }: EventBlockProps) {
  const [, setTick] = useState(0);
  const startRef = useRef<number | null>(null);
  const status = ev.status || "idle";

  useEffect(() => {
    if (status === "running") {
      startRef.current = Date.now();
      const id = setInterval(() => setTick((n) => n + 1), 250);
      return () => clearInterval(id);
    }
    startRef.current = null;
  }, [status]);

  const elapsedMs = status === "running" && startRef.current
    ? Date.now() - startRef.current
    : ev.duration_ms;

  const headLabel = (() => {
    if (status === "error") return "Failed";
    if (status === "running") return "Running";
    return "Ran";
  })();

  return (
    <div className={cn("py-0.5 text-[12px]", status === "error" ? "text-error" : "text-text-muted")}>
      <div className="flex flex-wrap items-baseline gap-x-1.5 gap-y-0.5">
        <span>{headLabel}</span>
        {ev.tool_name && (
          <span className="font-mono text-text">{ev.tool_name}</span>
        )}
        {elapsedMs ? (
          <span className="text-text-faint tabular-nums">
            · {fmtMs(elapsedMs, status)}
          </span>
        ) : null}
        {status === "running" && (
          <span className="size-1.5 rounded-full bg-running" />
        )}
      </div>
    </div>
  );
}

function fmtMs(ms: number, status: EventStatus): string {
  if (ms < 1000) return ms + "ms";
  const totalSec = Math.round(ms / 1000);
  if (totalSec < 60) return Math.round(ms / 100) / 10 + "s";
  const m = Math.floor(totalSec / 60);
  const s = totalSec % 60;
  if (m < 60) return s ? m + "m " + s + "s" : m + "m";
  const h = Math.floor(m / 60);
  const mm = m % 60;
  return mm ? h + "h " + mm + "m" : h + "h";
  void status;
}

function MentionLine({ ev }: EventBlockProps) {
  const display = ev.agent_display || ev.agent_id || "agent";
  return (
    <div className="py-0.5 text-[12px] text-text-muted">
      <div className="flex items-center gap-1.5 flex-wrap">
        <ArrowRight className="size-3 text-text-faint shrink-0" />
        <span className="whitespace-nowrap">called</span>
        <span className="font-display font-medium text-text inline-flex items-baseline whitespace-nowrap">
          <AtSign className="size-3 self-center" />
          {display}
        </span>
        {ev.duration_ms ? (
          <span className="text-text-faint tabular-nums whitespace-nowrap">· {fmtMs(ev.duration_ms, ev.status)}</span>
        ) : null}
      </div>
      {ev.reply && (
        <div className="mt-1 ml-4 pl-3 border-l-2 border-border-soft text-[13px] text-text leading-[1.6]">
          <Markdown variant="lite" className="whitespace-pre-wrap">
            {ev.reply}
          </Markdown>
        </div>
      )}
    </div>
  );
}

function DelegateLine({ ev }: EventBlockProps) {
  const [open, setOpen] = useState(false);
  const [, setTick] = useState(0);
  const startRef = useRef<number | null>(null);
  const status = ev.status || "pending";

  useEffect(() => {
    if (status === "running" || status === "pending") {
      startRef.current = startRef.current ?? Date.now();
      const id = setInterval(() => setTick((n) => n + 1), 250);
      return () => clearInterval(id);
    }
    startRef.current = null;
  }, [status]);

  const elapsedMs = (status === "running" || status === "pending") && startRef.current
    ? Date.now() - startRef.current
    : ev.duration_ms;

  const display = ev.agent_display || ev.agent_id || "agent";
  const longRunning = (status === "running" || status === "pending") && elapsedMs && elapsedMs >= 5 * 60 * 1000;
  const statusText =
    status === "pending" ? "pending" :
    status === "running" ? (longRunning ? "still running" : "running") :
    status === "done" ? "completed" :
    status === "error" ? "failed" :
    status;
  const statusColor =
    status === "error" ? "text-error" :
    status === "done" ? "text-text-muted" :
    status === "running" || status === "pending" ? "text-running" :
    "text-text-faint";

  const hasDetails = !!(ev.task || ev.output || ev.err || (ev.steps && ev.steps.length));
  const detailLabel = status === "done" ? "view result" : status === "error" ? "view details" : "view details";

  return (
    <div className={cn("py-0.5 text-[12px]", status === "error" ? "text-error" : "text-text-muted")}>
      <div className="flex flex-wrap items-baseline gap-x-1.5 gap-y-0.5">
        <ArrowRight className="size-3 text-text-faint shrink-0 self-center" />
        <span className="whitespace-nowrap">delegated to</span>
        <span className="font-display font-medium text-text inline-flex items-center gap-0.5 whitespace-nowrap">
          <AtSign className="size-3" />
          {display}
        </span>
        <span className={cn("tabular-nums whitespace-nowrap", statusColor)}>· {statusText}</span>
        {elapsedMs ? <span className="text-text-faint tabular-nums whitespace-nowrap">· {fmtMs(elapsedMs, status)}</span> : null}
        {hasDetails && (
          <button
            onClick={() => setOpen((v) => !v)}
            className="text-[11.5px] text-text-faint hover:text-text-muted underline underline-offset-2 cursor-pointer"
          >
            {open ? "hide details" : detailLabel}
          </button>
        )}
        {(status === "running" || status === "pending") && (
          <span className="size-1.5 rounded-full bg-running self-center" />
        )}
      </div>
      {open && hasDetails && (
        <div className="mt-1.5 ml-4 pl-3 border-l-2 border-border-soft text-[12.5px] text-text space-y-2.5">
          {ev.err && (
            <div>
              <div className="font-display text-[10px] uppercase tracking-[0.7px] text-error mb-1">error</div>
              <div className="text-error whitespace-pre-wrap leading-[1.55]">{ev.err}</div>
            </div>
          )}
          {ev.task && (
            <div>
              <div className="font-display text-[10px] uppercase tracking-[0.7px] text-text-whisper mb-1">task</div>
              <div className="text-text-muted whitespace-pre-wrap leading-[1.55]">{ev.task}</div>
            </div>
          )}
          {ev.output && !ev.err && (
            <div>
              <div className="font-display text-[10px] uppercase tracking-[0.7px] text-text-whisper mb-1">result</div>
              <div className="text-text whitespace-pre-wrap leading-[1.6]">
                <Markdown variant="lite">{ev.output}</Markdown>
              </div>
            </div>
          )}
          {ev.steps && ev.steps.length > 0 && (
            <div>
              <div className="font-display text-[10px] uppercase tracking-[0.7px] text-text-whisper mb-1">key steps</div>
              <ol className="space-y-0.5">
                {ev.steps.map((s, i) => (
                  <li key={i} className="flex items-baseline gap-2 text-[12px] leading-[1.55]">
                    <span className="font-display text-[10px] tabular-nums shrink-0 text-text-whisper">
                      {String(i + 1).padStart(2, "0")}
                    </span>
                    <span className={cn(
                      "truncate",
                      s.status === "error" ? "text-error" : "text-text-muted",
                    )}>
                      {s.output || s.tool}
                    </span>
                  </li>
                ))}
              </ol>
            </div>
          )}
          <div className="text-[10.5px] text-text-whisper font-mono pt-0.5">
            task: {ev.task_id || "—"}
          </div>
        </div>
      )}
    </div>
  );
}
