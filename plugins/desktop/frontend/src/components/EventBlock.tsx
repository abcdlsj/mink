import { useEffect, useRef, useState } from "react";
import { AlertTriangle } from "lucide-react";
import { cn } from "@/lib/utils";
import type { EventBlock as EventBlockData, EventStatus } from "@/lib/types";

interface EventBlockProps {
  ev: EventBlockData;
}

export function EventBlock({ ev }: EventBlockProps) {
  if (ev.kind === "service_notice") return <ServiceLine ev={ev} />;
  if (ev.kind === "reasoning") return null;
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
