import { useEffect, useRef, useState } from "react";
import { ChevronRight, Terminal, Brain, Info, AlertTriangle } from "lucide-react";
import { cn } from "@/lib/utils";
import type { EventBlock as EventBlockData, EventStatus } from "@/lib/types";

interface EventBlockProps {
  ev: EventBlockData;
}

export function EventBlock({ ev }: EventBlockProps) {
  if (ev.kind === "service_notice") return <ServiceNotice ev={ev} />;
  return <ExpandableEvent ev={ev} />;
}

function ServiceNotice({ ev }: EventBlockProps) {
  const isError = ev.status === "error";
  return (
    <div
      className={cn(
        "flex items-center gap-2 px-2 py-1 text-[11.5px]",
        isError ? "text-error" : "text-text-muted",
      )}
    >
      {isError ? (
        <AlertTriangle className="size-3 shrink-0" />
      ) : (
        <Info className="size-3 text-text-faint shrink-0" />
      )}
      <span>{ev.output || ""}</span>
    </div>
  );
}

function ExpandableEvent({ ev }: EventBlockProps) {
  const [open, setOpen] = useState(false);
  const [faded, setFaded] = useState(false);
  const [, setTick] = useState(0);
  const startRef = useRef<number | null>(null);
  const status = ev.status || "idle";
  const isReasoning = ev.kind === "reasoning";

  useEffect(() => {
    if (status === "running") {
      startRef.current = Date.now();
      const id = setInterval(() => setTick((n) => n + 1), 200);
      return () => clearInterval(id);
    }
    startRef.current = null;
  }, [status]);

  useEffect(() => {
    if (status === "done") {
      const t = setTimeout(() => setFaded(true), 1000);
      return () => clearTimeout(t);
    }
    setFaded(false);
  }, [status]);

  const elapsedMs = status === "running" && startRef.current
    ? Date.now() - startRef.current
    : ev.duration_ms;

  return (
    <div
      className={cn(
        "rounded-md border border-border-event bg-panel-event overflow-hidden transition-colors",
        "border-l-2",
        statusBorderClass(status, isReasoning, faded),
      )}
    >
      <button
        onClick={() => setOpen((v) => !v)}
        className="w-full flex items-center gap-2 px-2.5 py-1.5 min-h-7 text-left hover:bg-panel-2 transition-colors cursor-pointer select-none"
      >
        <ChevronRight
          className={cn(
            "size-3 text-text-faint shrink-0 transition-transform",
            open && "rotate-90",
          )}
        />
        {isReasoning ? (
          <Brain className="size-3 text-reasoning shrink-0" />
        ) : status === "error" ? (
          <AlertTriangle className="size-3 text-error shrink-0" />
        ) : (
          <Terminal className="size-3 text-text-faint shrink-0" />
        )}
        <span
          className={cn(
            "text-[12.5px] font-medium",
            isReasoning ? "text-reasoning" : "text-text",
          )}
        >
          {isReasoning ? "Reasoning" : "Tool"}
        </span>
        {!isReasoning && ev.tool_name && (
          <span className="text-[12.5px] text-text-muted font-mono">{ev.tool_name}</span>
        )}
        <span className="flex-1" />
        <span className={cn("text-[11px] tabular-nums", statusMetaClass(status))}>
          {isReasoning ? "view" : metaLabel(status, elapsedMs)}
        </span>
        {!isReasoning && status === "running" && (
          <span className="size-1.5 rounded-full bg-running dot-pulse" />
        )}
      </button>
      {open && (
        <div
          className={cn(
            "px-3 py-2 text-[12.5px] border-t border-border-event whitespace-pre-wrap break-words",
            isReasoning ? "text-text-muted leading-relaxed" : "font-mono text-text",
          )}
        >
          {isReasoning ? (
            ev.output || "(no reasoning text)"
          ) : (
            <>
              {ev.args && <div className="text-text-muted">{ev.args}</div>}
              {ev.output && <div className="mt-1">{ev.output}</div>}
              {ev.err && <div className="mt-1.5 text-error">{ev.err}</div>}
            </>
          )}
        </div>
      )}
    </div>
  );
}

function statusBorderClass(status: EventStatus, isReasoning: boolean, faded: boolean): string {
  if (isReasoning) return "border-l-reasoning";
  if (status === "running") return "border-l-running";
  if (status === "error") return "border-l-error";
  if (status === "done") return faded ? "border-l-text-faint" : "border-l-done";
  return "border-l-text-faint";
}

function statusMetaClass(status: EventStatus): string {
  if (status === "running") return "text-running";
  if (status === "error") return "text-error";
  if (status === "done") return "text-done";
  return "text-text-faint";
}

function metaLabel(status: EventStatus, ms?: number): string {
  if (status === "running" && ms) return Math.round(ms / 100) / 10 + "s";
  if (ms) return ms + "ms";
  return status;
}
