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
        "inline-flex max-w-full items-center gap-1.5 border px-2 py-1 text-[11.5px] leading-[17px]",
        isError ? "border-error-border bg-error-bg text-error" : "border-tool-border bg-tool-bg text-tool",
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

  const headLabel = status === "error" ? "Tool failed" : status === "running" ? "Using tools" : "Tool finished";
  const summary = toolEventSummary(ev);
  const hasDetails = !!(ev.args || ev.output || ev.err);

  return (
    <div className={cn("max-w-full py-0.5 text-[11.5px] leading-[17px]", status === "error" ? "text-error" : "text-tool")}>
      <div className="flex flex-wrap items-baseline gap-x-1.5 gap-y-0.5">
        <span
          className={cn(
            "size-1.5 rounded-full self-center",
            status === "error" ? "bg-error" : status === "running" ? "bg-running" : "bg-tool",
          )}
        />
        <span>{headLabel}</span>
        {summary && <span className="max-w-[34rem] truncate text-text-faint">· {summary}</span>}
        {elapsedMs ? <span className="text-text-faint tabular-nums">· {fmtMs(elapsedMs, status)}</span> : null}
        {hasDetails && (
          <button
            type="button"
            onClick={() => setOpen((v) => !v)}
            className="cursor-pointer text-text-faint underline underline-offset-2 hover:text-tool"
          >
            {open ? "hide details" : "view details"}
          </button>
        )}
      </div>
      {open && hasDetails && (
        <div className="ml-3.5 mt-1.5 space-y-2 border-l border-tool-border bg-tool-bg px-3 py-2 text-[11px] text-tool">
          {ev.err && <ToolDetail label="error" tone="error" value={ev.err} />}
          {ev.args && <ToolDetail label={ev.tool_name ? `args · ${ev.tool_name}` : "args"} value={ev.args} />}
          {ev.output && <ToolDetail label="output" value={ev.output} />}
        </div>
      )}
    </div>
  );
}

function ToolDetail({ label, value, tone }: { label: string; value: string; tone?: "error" }) {
  return (
    <div>
      <div className={cn("mb-1 font-mono text-[10px] uppercase", tone === "error" ? "text-error" : "text-text-faint")}>
        {label}
      </div>
      <pre className={cn(
        "max-h-40 overflow-auto whitespace-pre-wrap break-words font-mono text-[10.5px] leading-[1.45]",
        tone === "error" ? "text-error" : "text-tool",
      )}>
        {value}
      </pre>
    </div>
  );
}

export function toolEventSummary(ev: EventBlockData): string {
  const fromArgs = summarizeArgs(ev.tool_name || "", ev.args || "");
  if (fromArgs) return fromArgs;
  return toolActivity(ev.tool_name || "", !!ev.err);
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
    const title = stringValue(obj.title) || stringValue(obj.name) || stringValue(obj.intent);
    if (command) return commandSummary(command);
    if (query) return querySummary(query);
    if (path) return pathAction(lower) + " file";
    if (target) return targetSummary(target);
    if (title) return titleSummary(title, lower);
  }
  const bare = text.replace(/^["']|["']$/g, "");
  if (looksLikePayload(bare)) return toolActivity(toolName, false);
  return shorten("checking " + bare, 80);
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

function pathAction(toolName: string): string {
  if (toolName.includes("write") || toolName.includes("edit") || toolName.includes("patch")) return "edit";
  if (toolName.includes("list")) return "list";
  if (toolName.includes("read") || toolName.includes("open")) return "read";
  return "checking";
}

function commandSummary(command: string): string {
  const c = command.trim();
  if (!c) return "running shell command";
  if (/\b(git status|git diff|git log|git show)\b/.test(c)) return "checking git state";
  if (/\b(go test|gotestsum)\b/.test(c)) return "running Go tests";
  if (/\bgo vet\b/.test(c)) return "running Go vet";
  if (/\bgo build\b|\bmake build\b/.test(c)) return "running build";
  if (/\b(npm run build|pnpm build|yarn build|vite build)\b/.test(c)) return "running frontend build";
  if (/\b(tsc|typecheck)\b/.test(c)) return "running type check";
  if (/\b(rg|grep|fd|find)\b/.test(c)) return "searching project";
  if (/\b(sed|cat|tail|head|less)\b/.test(c)) return "reading files";
  if (/\b(curl|wget)\b/.test(c)) return "checking endpoint";
  if (/\b(lsof|ps|kill)\b/.test(c)) return "checking local process";
  if (looksLikePayload(c)) return "running shell command";
  return shorten("running " + firstCommandWord(c), 80);
}

function querySummary(query: string): string {
  const q = query.trim();
  if (!q || looksLikePayload(q)) return "searching";
  return `searching "${shorten(q, 44)}"`;
}

function targetSummary(target: string): string {
  if (/^https?:\/\//i.test(target)) return "checking URL";
  if (looksLikePath(target)) return "checking target";
  return shorten("checking " + target, 60);
}

function titleSummary(title: string, toolName: string): string {
  const t = cleanText(title);
  if (!t || looksLikePayload(t)) return toolActivity(toolName, false);
  return shorten("checking " + t, 70);
}

function toolActivity(toolName: string, failed: boolean): string {
  const lower = toolName.toLowerCase();
  if (failed) return "see details";
  if (lower.includes("search") || lower.includes("grep") || lower.includes("find")) return "searching project";
  if (lower.includes("read") || lower.includes("open") || lower.includes("cat")) return "reading file";
  if (lower.includes("list") || lower.includes("ls")) return "listing files";
  if (lower.includes("write") || lower.includes("edit") || lower.includes("patch")) return "editing file";
  if (lower.includes("http") || lower.includes("fetch") || lower.includes("curl")) return "checking endpoint";
  if (lower.includes("bash") || lower.includes("shell") || lower.includes("exec")) return "running shell command";
  return "checking tool result";
}

function firstCommandWord(command: string): string {
  const first = command.split(/\s+/)[0] || "command";
  return first.replace(/^.*\//, "");
}

function looksLikePayload(text: string): boolean {
  if (text.length > 120) return true;
  if (/^[\[{]/.test(text.trim())) return true;
  if (looksLikePath(text)) return true;
  if (/https?:\/\/\S{20,}/i.test(text)) return true;
  return false;
}

function looksLikePath(text: string): boolean {
  return /(^|[\s"'])(\/Users\/|\/tmp\/|\/var\/|~\/|\.\.?\/|[A-Za-z]:\\)/.test(text);
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
    <div className="py-0.5 text-[11.5px] text-mention">
      <div className="flex items-center gap-1.5 flex-wrap">
        <ArrowRight className="size-3 text-mention shrink-0" />
        <span className="whitespace-nowrap">called</span>
        <span className="inline-flex items-baseline border border-mention-border bg-mention-bg px-1 font-display font-semibold text-mention whitespace-nowrap">
          <AtSign className="size-3 self-center" />
          {display}
        </span>
        {ev.duration_ms ? (
          <span className="text-text-faint tabular-nums whitespace-nowrap">· {fmtMs(ev.duration_ms, ev.status)}</span>
        ) : null}
      </div>
      {ev.reply && (
        <div className="ml-4 mt-1 border-l-2 border-mention-border bg-mention-bg px-3 py-1.5 text-[12.5px] leading-[1.55] text-text-muted">
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
    <div className={cn("py-0.5 text-[11.5px]", status === "error" ? "text-error" : "text-tool")}>
      <div className="flex flex-wrap items-baseline gap-x-1.5 gap-y-0.5">
        <ArrowRight className="size-3 text-text-faint shrink-0 self-center" />
        <span className="whitespace-nowrap">delegated to</span>
        <span className={cn(
          "inline-flex items-center gap-0.5 border px-1 font-display font-semibold whitespace-nowrap",
          status === "running" || status === "pending"
            ? "border-running-border bg-running-bg text-running"
            : status === "error"
              ? "border-error-border bg-error-bg text-error"
              : "border-agent-border bg-agent-bg text-agent",
        )}>
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
        <div className="ml-4 mt-1.5 space-y-2.5 border-l-2 border-tool-border bg-tool-bg px-3 py-2 text-[12.5px] text-text">
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
