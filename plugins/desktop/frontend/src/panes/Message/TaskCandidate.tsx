import { useState } from "react";
import { api } from "@/lib/api";
import { useStore } from "@/lib/store";
import type { MessageView, PersonaItem } from "@/lib/types";
import { cn } from "@/lib/utils";

interface SourceRef {
  spaceID: string;
  sourceMessageID: string;
  sourceThreadID?: string;
}

export function TaskCandidate({ message, forceMainScope = false }: { message: MessageView; forceMainScope?: boolean }) {
  const personas = useStore((s) => s.personas);
  const refreshCapabilities = useStore((s) => s.refreshCapabilities);
  const openCurrentRoute = useStore((s) => s.openCurrentRoute);
  const source = useTaskSource(message, forceMainScope);
  const executors = personas.filter(canExecuteTask);
  const [open, setOpen] = useState(false);
  const [title, setTitle] = useState(() => defaultTitle(message.content || ""));
  const [assignee, setAssignee] = useState("");
  const [outcome, setOutcome] = useState("");
  const [criteria, setCriteria] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const [created, setCreated] = useState("");

  if (!source || executors.length === 0 || !message.content?.trim() || !looksLikeTaskRequest(message.content)) return null;

  const missing = [
    !title.trim() && "title",
    !assignee && "assignee",
    !outcome.trim() && "outcome",
    !criteria.trim() && "criteria",
  ].filter(Boolean) as string[];
  const ready = missing.length === 0;

  const create = async () => {
    if (!ready || created) return;
    setBusy(true);
    setError("");
    try {
      const task = await api.createTask({
        space_id: source.spaceID,
        source_message_id: source.sourceMessageID,
        source_thread_id: source.sourceThreadID,
        assignee_id: assignee,
        created_by: "user",
        assigned_by: "user",
        title: title.trim(),
        expected_outcome: outcome.trim(),
        acceptance_criteria: criteria.trim(),
        explicit_task_intent: true,
      });
      setCreated(task.id);
      await Promise.all([refreshCapabilities(), openCurrentRoute()]);
    } catch (err) {
      setError(err instanceof Error ? err.message : "Create task failed");
    } finally {
      setBusy(false);
    }
  };

  if (!open) {
    return (
      <button
        type="button"
        onClick={() => setOpen(true)}
        className="mt-2 inline-flex border border-border bg-panel-event px-1.5 py-0.5 font-mono text-[11px] text-text-muted hover:text-text"
      >
        Create task
      </button>
    );
  }

  return (
    <div className="mt-2 max-w-[560px] border border-border bg-panel-event p-2.5">
      <div className="flex items-center justify-between gap-2">
        <div className="font-display text-[12px] font-extrabold uppercase text-text">Task candidate</div>
        <button type="button" onClick={() => setOpen(false)} className="font-mono text-[11px] text-text-faint hover:text-text">
          collapse
        </button>
      </div>
      <div className="mt-2 grid gap-2">
        <input
          value={title}
          onChange={(e) => setTitle(e.target.value)}
          placeholder="Task title"
          className="border border-border bg-panel px-2 py-1.5 text-[12.5px] text-text outline-none focus:border-text-faint"
        />
        <select
          value={assignee}
          onChange={(e) => setAssignee(e.target.value)}
          className="border border-border bg-panel px-2 py-1.5 text-[12.5px] text-text outline-none focus:border-text-faint"
        >
          <option value="">Assign to agent...</option>
          {executors.map((p) => (
            <option key={p.id} value={p.id}>
              @{p.display || p.id} · {capabilityLabel(p)}
            </option>
          ))}
        </select>
        <textarea
          value={outcome}
          onChange={(e) => setOutcome(e.target.value)}
          placeholder="Expected outcome"
          rows={2}
          className="resize-none border border-border bg-panel px-2 py-1.5 text-[12.5px] text-text outline-none focus:border-text-faint"
        />
        <textarea
          value={criteria}
          onChange={(e) => setCriteria(e.target.value)}
          placeholder="Acceptance criteria"
          rows={2}
          className="resize-none border border-border bg-panel px-2 py-1.5 text-[12.5px] text-text outline-none focus:border-text-faint"
        />
      </div>
      <div className="mt-2 flex items-center justify-between gap-2">
        <div className="min-w-0 text-[11px] text-text-faint">
          {created ? `Created ${created}` : ready ? "Source linked to this message." : `Needs ${missing.join(", ")}.`}
          {error && <span className="ml-2 text-error">{error}</span>}
        </div>
        <button
          type="button"
          disabled={!ready || busy || !!created}
          onClick={() => void create()}
          className={cn(
            "shrink-0 border border-border bg-accent px-2 py-1 font-mono text-[11px] text-text hover:border-text-faint",
            (!ready || busy || !!created) && "pointer-events-none opacity-45",
          )}
        >
          {created ? "Created" : busy ? "creating..." : "Create task"}
        </button>
      </div>
    </div>
  );
}

function useTaskSource(message: MessageView, forceMainScope: boolean): SourceRef | null {
  const view = useStore((s) => s.view);
  const activeChannel = useStore((s) => s.activeChannel);
  const activeDirect = useStore((s) => s.activeDirect);
  const activeAgentSpace = useStore((s) => s.activeAgentSpace);
  const detail = useStore((s) => s.detail);
  const threadDetail = useStore((s) => s.threadDetail);
  if (!forceMainScope && threadDetail && !threadDetail.unsupported && !threadDetail.not_found) {
    return {
      spaceID: threadDetail.space_id,
      sourceMessageID: message.id,
      sourceThreadID: threadDetail.parent_id,
    };
  }
  if (view === "channel" && activeChannel) {
    return {
      spaceID: activeChannel,
      sourceMessageID: message.id,
      sourceThreadID: message.thread_id,
    };
  }
  if (view === "direct" && (activeDirect || detail?.item.id)) {
    return { spaceID: activeDirect || detail!.item.id, sourceMessageID: message.id };
  }
  if (view === "agent" && (activeAgentSpace || detail?.item.id)) {
    return { spaceID: detail?.item.id || activeAgentSpace!, sourceMessageID: message.id };
  }
  return null;
}

function canExecuteTask(p: PersonaItem): boolean {
  return (p.capabilities || []).some((cap) => normalizeCapability(cap) === "task.execute");
}

function capabilityLabel(p: PersonaItem): string {
  const caps = (p.capabilities || []).map(normalizeCapability).filter((cap) => cap.startsWith("task."));
  return caps.length ? caps.join(", ") : "task.execute";
}

function normalizeCapability(cap: string): string {
  const c = cap.trim().toLowerCase().replaceAll("_", ".").replaceAll(":", ".");
  if (c === "execute" || c === "exec") return "task.execute";
  if (c === "assign") return "task.assign";
  if (c === "create") return "task.create";
  if (c === "review") return "task.review";
  if (c === "plan") return "task.plan";
  return c;
}

function defaultTitle(content: string): string {
  const text = content.replace(/\s+/g, " ").trim();
  if (!text) return "";
  return text.length > 72 ? text.slice(0, 69) + "..." : text;
}

function looksLikeTaskRequest(content: string): boolean {
  const text = content.replace(/\s+/g, " ").trim().toLowerCase();
  if (!text) return false;
  return explicitTaskIntentPatterns.some((re) => re.test(text));
}

const explicitTaskIntentPatterns = [
  /创建任务/,
  /创建(一个|个)?\s*task\b/,
  /新建任务/,
  /新建(一个|个)?\s*task\b/,
  /建(个|一个)?任务/,
  /加(个|一个)?任务/,
  /记为任务/,
  /记录成任务/,
  /转成任务/,
  /作为任务/,
  /任务.*(指派|分配|交给|给)\s*@?[\w\u4e00-\u9fa5-]+/,
  /\b(create|add|make)\s+(a\s+)?task\b/,
  /\bassign\s+(this\s+)?task\b/,
  /\bturn\s+this\s+into\s+(a\s+)?task\b/,
];
