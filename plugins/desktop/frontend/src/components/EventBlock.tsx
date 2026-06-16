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
        "inline-flex items-center gap-1.5 border border-border px-1.5 py-0.5 text-[12px]",
        isError ? "bg-action-bg text-error" : "bg-panel-2 text-text-muted",
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
  const summary = toolSummary(ev);

  return (
    <div className={cn("inline-flex border border-border px-1.5 py-0.5 text-[12px]", status === "error" ? "bg-action-bg text-error" : "bg-panel-2 text-text-muted")}>
      <div className="flex flex-wrap items-baseline gap-x-1.5 gap-y-0.5">
        <span>{headLabel}</span>
        {ev.tool_name && (
          <span className="font-mono font-semibold text-text">{ev.tool_name}</span>
        )}
        {summary && (
          <span className={cn("max-w-[34rem] truncate", status === "error" ? "text-error" : "text-text-muted")}>
            · {summary}
          </span>
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

function toolSummary(ev: EventBlockData): string {
  if (ev.err) return shorten(cleanText(ev.err), 120);
  const fromArgs = summarizeArgs(ev.tool_name || "", ev.args || "");
  if (fromArgs) return fromArgs;
  if (ev.output) return shorten(cleanText(ev.output), 120);
  return "";
}

function summarizeArgs(toolName: string, raw: string): string {
  const text = cleanText(raw);
  if (!text) return "";
  const parsed = parseJSON(text);
  const lower = toolName.toLowerCase();
  if (parsed && typeof parsed === "object" && !Array.isArray(parsed)) {
    const obj = parsed as Record<string, unknown>;
    const command = stringValue(obj.command) || stringValue(obj.cmd) || stringValue(obj.shell_command);
    const query = stringValue(obj.query) || stringValue(obj.q) || stringValue(obj.search_query) || stringValue(obj.pattern);
    const path = stringValue(obj.path) || stringValue(obj.file) || stringValue(obj.filename) || stringValue(obj.uri);
    const target = stringValue(obj.target) || stringValue(obj.channel) || stringValue(obj.url);
    const title = stringValue(obj.title) || stringValue(obj.name);
    if (command) return shorten(commandLabel(lower) + " " + command, 120);
    if (query) return shorten("search " + query, 120);
    if (path) return shorten(pathAction(lower) + " " + path, 120);
    if (target) return shorten("target " + target, 120);
    if (title) return shorten(title, 120);
  }
  return shorten(text.replace(/^["']|["']$/g, ""), 120);
}

function parseJSON(raw: string): unknown | null {
  try {
    return JSON.parse(raw);
  } catch {
    return null;
  }
}

function stringValue(value: unknown): string {
  if (typeof value === "string") return value.trim();
  if (typeof value === "number" || typeof value === "boolean") return String(value);
  if (Array.isArray(value)) return value.map(stringValue).filter(Boolean).join(", ");
  return "";
}

function commandLabel(toolName: string): string {
  if (toolName.includes("bash") || toolName.includes("shell") || toolName.includes("exec")) return "run";
  return "command";
}

function pathAction(toolName: string): string {
  if (toolName.includes("write") || toolName.includes("edit") || toolName.includes("patch")) return "edit";
  if (toolName.includes("list")) return "list";
  if (toolName.includes("read") || toolName.includes("open")) return "read";
  return "path";
}

function cleanText(text: string): string {
  return text.replace(/\s+/g, " ").trim();
}

function shorten(text: string, max: number): string {
  if (text.length <= max) return text;
  return text.slice(0, Math.max(0, max - 1)).trimEnd() + "…";
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
        <ArrowRight className="size-3 text-text-muted shrink-0" />
        <span className="whitespace-nowrap">called</span>
        <span className="inline-flex items-baseline border border-border bg-accent px-1 font-display font-semibold text-text whitespace-nowrap">
          <AtSign className="size-3 self-center" />
          {display}
        </span>
        {ev.duration_ms ? (
          <span className="text-text-faint tabular-nums whitespace-nowrap">· {fmtMs(ev.duration_ms, ev.status)}</span>
        ) : null}
      </div>
      {ev.reply && (
        <div className="ml-4 mt-1 border-l-2 border-border bg-panel-2 px-3 py-1 text-[13px] leading-[1.6] text-text">
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
        <span className="inline-flex items-center gap-0.5 border border-border bg-accent px-1 font-display font-semibold text-text whitespace-nowrap">
          <AtSign className="size-3" />
          {display}
        </span>
        <span className={cn("tabular-nums whitespace-nowrap", statusColor)}>· {statusText}</span>
        {elapsedMs ? <span className="text-text-faint tabular-nums whitespace-nowrap">· {fmtMs(elapsedMs, status)}</span> : null}
        {hasDetails && (
          <button
            onClick={() => setOpen((v) => !v)}
            className="cursor-pointer text-[11.5px] text-text-muted underline underline-offset-2 hover:text-text"
          >
            {open ? "hide details" : detailLabel}
          </button>
        )}
        {(status === "running" || status === "pending") && (
          <span className="size-1.5 rounded-full bg-running self-center" />
        )}
      </div>
      {open && hasDetails && (
        <div className="ml-4 mt-1.5 space-y-2.5 border-l-2 border-border bg-panel-2 px-3 py-2 text-[12.5px] text-text">
          {ev.err && (
            <div>
              <div className="font-display text-[10.5px] font-semibold uppercase text-error mb-1">error</div>
              <div className="text-error whitespace-pre-wrap leading-[1.55]">{ev.err}</div>
            </div>
          )}
          {ev.task && (
            <div>
              <div className="mb-1 font-display text-[10.5px] font-semibold uppercase text-text-muted">task</div>
              <div className="text-text-muted whitespace-pre-wrap leading-[1.55]">{ev.task}</div>
            </div>
          )}
          {ev.output && !ev.err && (
            <div>
              <div className="mb-1 font-display text-[10.5px] font-semibold uppercase text-text-muted">result</div>
              <div className="text-text whitespace-pre-wrap leading-[1.6]">
                <Markdown variant="lite">{ev.output}</Markdown>
              </div>
            </div>
          )}
          {ev.steps && ev.steps.length > 0 && (
            <div>
              <div className="mb-1 font-display text-[10.5px] font-semibold uppercase text-text-muted">key steps</div>
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
