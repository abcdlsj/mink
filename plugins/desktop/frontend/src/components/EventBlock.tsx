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
  const [open, setOpen] = useState(false);
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

  const hasDetails = !!(ev.args || ev.output || ev.err);

  const headLabel = (() => {
    if (status === "error") return "Tool failed";
    if (status === "running") return "Running";
    return "Used";
  })();

  return (
    <div className={cn("py-0.5 text-[12px]", status === "error" ? "text-error" : "text-text-muted")}>
      <div className="flex flex-wrap items-baseline gap-x-1.5 gap-y-0.5">
        <span>{headLabel}</span>
        {ev.tool_name && (
          <span className="font-mono text-text">{ev.tool_name}</span>
        )}
        {ev.err && status === "error" && (
          <span className="text-error">— {ev.err}</span>
        )}
        {elapsedMs ? (
          <span className="text-text-faint tabular-nums">
            · {fmtMs(elapsedMs, status)}
          </span>
        ) : null}
        {hasDetails && (
          <button
            onClick={() => setOpen((v) => !v)}
            className="text-[11.5px] text-text-faint hover:text-text-muted underline underline-offset-2 cursor-pointer"
          >
            {open ? "hide details" : "view details"}
          </button>
        )}
        {status === "running" && (
          <span className="size-1.5 rounded-full bg-running dot-pulse" />
        )}
      </div>
      {open && hasDetails && (
        <div className="mt-1 pl-3 border-l border-border-soft text-text font-mono text-[12px] whitespace-pre-wrap break-words">
          {ev.args && <div className="text-text-muted">{ev.args}</div>}
          {ev.output && <div className="mt-0.5">{ev.output}</div>}
          {ev.err && status !== "error" && <div className="mt-0.5 text-error">{ev.err}</div>}
        </div>
      )}
    </div>
  );
}

function fmtMs(ms: number, status: EventStatus): string {
  if (status === "running") return Math.round(ms / 100) / 10 + "s";
  if (ms >= 1000) return Math.round(ms / 100) / 10 + "s";
  return ms + "ms";
}

function MentionLine({ ev }: EventBlockProps) {
  const display = ev.agent_display || ev.agent_id || "agent";
  return (
    <div className="py-0.5 text-[12px] text-text-muted">
      <div className="flex items-center gap-1.5">
        <ArrowRight className="size-3 text-text-faint shrink-0" />
        <span>called</span>
        <span className="font-display font-medium text-text inline-flex items-center gap-0.5">
          <AtSign className="size-3" />
          {display}
        </span>
        {ev.duration_ms ? (
          <span className="text-text-faint tabular-nums">· {fmtMs(ev.duration_ms, ev.status)}</span>
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
  const statusText = status === "pending" ? "pending" : status === "running" ? "running" : status;
  const statusColor =
    status === "error" ? "text-error" :
    status === "done" ? "text-done" :
    status === "running" || status === "pending" ? "text-running" :
    "text-text-faint";

  const hasDetails = !!(ev.task || ev.output || ev.err);

  return (
    <div className={cn("py-0.5 text-[12px]", status === "error" ? "text-error" : "text-text-muted")}>
      <div className="flex flex-wrap items-baseline gap-x-1.5 gap-y-0.5">
        <ArrowRight className="size-3 text-text-faint shrink-0 self-center" />
        <span>delegated to</span>
        <span className="font-display font-medium text-text inline-flex items-center gap-0.5">
          <AtSign className="size-3" />
          {display}
        </span>
        <span className={cn("tabular-nums", statusColor)}>· {statusText}</span>
        {elapsedMs ? <span className="text-text-faint tabular-nums">· {fmtMs(elapsedMs, status)}</span> : null}
        {hasDetails && (
          <button
            onClick={() => setOpen((v) => !v)}
            className="text-[11.5px] text-text-faint hover:text-text-muted underline underline-offset-2 cursor-pointer"
          >
            {open ? "hide details" : "view details"}
          </button>
        )}
        {(status === "running" || status === "pending") && (
          <span className="size-1.5 rounded-full bg-running dot-pulse self-center" />
        )}
      </div>
      {open && hasDetails && (
        <div className="mt-1 ml-4 pl-3 border-l-2 border-border-soft text-[12.5px] text-text">
          {ev.task && (
            <div className="text-text-muted mb-1">
              <span className="font-display text-[10px] uppercase tracking-[0.7px] text-text-whisper mr-1.5">task</span>
              {ev.task}
            </div>
          )}
          {ev.output && (
            <div className="mt-1">
              <span className="font-display text-[10px] uppercase tracking-[0.7px] text-text-whisper mr-1.5">result</span>
              <span className="whitespace-pre-wrap">{ev.output}</span>
            </div>
          )}
          {ev.err && (
            <div className="mt-1 text-error">
              <span className="font-display text-[10px] uppercase tracking-[0.7px] mr-1.5">error</span>
              {ev.err}
            </div>
          )}
        </div>
      )}
    </div>
  );
}
