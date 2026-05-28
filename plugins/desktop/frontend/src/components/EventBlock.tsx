import { useState } from "react";
import { ChevronRight, Terminal, Brain, Info } from "lucide-react";
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
  return (
    <div className="flex items-center gap-2 px-2 py-0.5 text-[11.5px] text-text-muted">
      <Info className="size-3 text-text-faint shrink-0" />
      <span>{ev.output || ""}</span>
    </div>
  );
}

function ExpandableEvent({ ev }: EventBlockProps) {
  const [open, setOpen] = useState(false);
  const status = ev.status || "idle";
  const isReasoning = ev.kind === "reasoning";

  return (
    <div
      className={cn(
        "rounded-md border border-border-event bg-panel-event overflow-hidden transition-colors",
        "border-l-2",
        statusBorderClass(status, isReasoning),
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
        <span className={cn("text-[11px]", statusMetaClass(status))}>
          {isReasoning ? "view" : metaLabel(ev)}
        </span>
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

function statusBorderClass(status: EventStatus, isReasoning: boolean): string {
  if (isReasoning) return "border-l-reasoning";
  if (status === "running") return "border-l-running";
  if (status === "error") return "border-l-error";
  if (status === "done") return "border-l-text-faint";
  return "border-l-text-faint";
}

function statusMetaClass(status: EventStatus): string {
  if (status === "running") return "text-running";
  if (status === "error") return "text-error";
  return "text-text-faint";
}

function metaLabel(ev: EventBlockData): string {
  const parts: string[] = [ev.status];
  if (ev.duration_ms) parts.push(ev.duration_ms + "ms");
  return parts.filter(Boolean).join(" · ");
}
