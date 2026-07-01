import { useState } from "react";
import { ClipboardList } from "lucide-react";
import { api } from "@/lib/api";
import type { AgentDMItem, ChannelItem, DirectChatItem, PersonaItem, TaskStateCard } from "@/lib/types";
import { useStore } from "@/lib/store";
import { cn, relTime } from "@/lib/utils";
import { normalizeCapability, taskColumn, type TaskColumn } from "@/lib/task-helpers";

export function TaskBoard() {
  const capabilities = useStore((s) => s.capabilities);
  const channels = useStore((s) => s.channels);
  const directChats = useStore((s) => s.directChats);
  const agentDMs = useStore((s) => s.agentDMs);
  const activeChannel = useStore((s) => s.activeChannel);
  const refreshCapabilities = useStore((s) => s.refreshCapabilities);
  const tasks = (capabilities?.tasks || []).filter((t) => (t.lifecycle || "active") === "active");
  const activeChannelItem = channels.find((c) => c.id === activeChannel);
  const [scope, setScope] = useState<"all" | "channel">(activeChannel ? "channel" : "all");
  const visibleTasks = scope === "channel" && activeChannel
    ? tasks.filter((t) => t.space_id === activeChannel)
    : tasks;
  const columns = taskBoardColumns(visibleTasks);

  return (
    <main className="h-full min-w-0 grid grid-rows-[auto_1fr] bg-panel">
      <header className="border-b-hard border-border bg-panel px-5 pb-3.5 pt-4">
        <div className="flex items-end justify-between gap-3">
          <div>
            <h2 className="flex items-center gap-2 font-display text-[19px] font-extrabold leading-tight text-text">
              <span className="inline-flex size-7 items-center justify-center border-2 border-border bg-accent">
                <ClipboardList className="size-[17px] text-text" />
              </span>
              Task Board
            </h2>
            <div className="mt-1 font-mono text-[11px] text-text-muted">
              Active work only · archived hidden
            </div>
          </div>
          <div className="flex items-center gap-1 border border-border bg-panel-2 p-1">
            <ScopeButton active={scope === "all"} onClick={() => setScope("all")}>
              All
            </ScopeButton>
            {activeChannelItem && (
              <ScopeButton active={scope === "channel"} onClick={() => setScope("channel")}>
                #{activeChannelItem.name}
              </ScopeButton>
            )}
          </div>
        </div>
      </header>

      <div className="min-h-0 overflow-y-auto px-5 py-5">
        <div className="grid h-full min-h-[360px] grid-cols-3 gap-3">
          {columns.map((col) => (
            <section key={col.key} className="min-w-0 border-hard border-border bg-panel-2">
              <div className="flex items-center justify-between border-b-hard border-border px-3 py-2">
                <div className="font-display text-[12px] font-extrabold uppercase text-text">
                  {col.label}
                </div>
                <div className="font-mono text-[11px] text-text-muted">{col.tasks.length}</div>
              </div>
              <div className="flex flex-col gap-2 p-2">
                {col.tasks.length > 0 ? col.tasks.map((task) => (
                  <TaskBoardCard
                    key={task.id}
                    task={task}
                    column={col.key}
                    channels={channels}
                    directChats={directChats}
                    agentDMs={agentDMs}
                    onChanged={refreshCapabilities}
                  />
                )) : (
                  <div className="grid min-h-28 place-items-center border border-dashed border-border bg-panel px-3 py-6 text-[12px] text-text-faint">
                    Empty
                  </div>
                )}
              </div>
            </section>
          ))}
        </div>
      </div>
    </main>
  );
}

function ScopeButton({ active, onClick, children }: { active: boolean; onClick: () => void; children: React.ReactNode }) {
  return (
    <button
      type="button"
      onClick={onClick}
      className={cn(
        "border px-2 py-1 font-mono text-[11px]",
        active ? "border-border bg-accent text-text" : "border-transparent text-text-muted hover:border-border hover:text-text",
      )}
    >
      {children}
    </button>
  );
}

function taskBoardColumns(tasks: TaskStateCard[]): { key: TaskColumn; label: string; tasks: TaskStateCard[] }[] {
  return [
    { key: "todo", label: "Todo", tasks: tasks.filter((task) => taskColumn(task.status) === "todo") },
    { key: "doing", label: "Doing", tasks: tasks.filter((task) => taskColumn(task.status) === "doing") },
    { key: "review", label: "Review", tasks: tasks.filter((task) => taskColumn(task.status) === "review") },
  ];
}

function TaskBoardCard({
  task,
  column,
  channels,
  directChats,
  agentDMs,
  onChanged,
}: {
  task: TaskStateCard;
  column: TaskColumn;
  channels: ChannelItem[];
  directChats: DirectChatItem[];
  agentDMs: AgentDMItem[];
  onChanged: () => Promise<void>;
}) {
  const agents = useStore((s) => s.agents);
  const personas = useStore((s) => s.personas);
  const openChannel = useStore((s) => s.openChannel);
  const openThread = useStore((s) => s.openThread);
  const openDirectChat = useStore((s) => s.openDirectChat);
  const openAgent = useStore((s) => s.openAgent);
  const focusTaskOrigin = useStore((s) => s.focusTaskOrigin);
  const openCurrentRoute = useStore((s) => s.openCurrentRoute);
  const [updating, setUpdating] = useState("");
  const [assigning, setAssigning] = useState(false);
  const assignee = agents.find((a) => a.id === task.worker_id)?.display || task.worker_id || "agent";
  const assignedBy = agents.find((a) => a.id === task.assigned_by)?.display || task.assigned_by || task.created_by || "user";
  const executors = personas.filter(canExecuteTask);
  const source = taskSourceLabel(task, channels, directChats, agentDMs);

  const openOrigin = async () => {
    const sourceMessageID = task.source_message || task.trigger_message_id || task.parent_message_id || "";
    const sourceThreadID = task.source_thread_id || task.parent_message_id || "";
    if (task.space_id && channels.some((c) => c.id === task.space_id)) {
      await openChannel(task.space_id);
      if (sourceThreadID) await openThread(sourceThreadID);
    } else if (task.space_id && directChats.some((d) => d.id === task.space_id)) {
      await openDirectChat(task.space_id);
    } else if (task.space_id && agentDMs.some((d) => d.id === task.space_id)) {
      await openAgent(task.space_id);
    }
    focusTaskOrigin(task.id, sourceMessageID);
  };

  const updateStatus = async (status: string, e: React.MouseEvent) => {
    e.stopPropagation();
    setUpdating(status);
    try {
      await api.updateTaskStatus(task.id, status);
      await Promise.all([onChanged(), openCurrentRoute()]);
    } finally {
      setUpdating("");
    }
  };

  const assignTo = async (personaID: string) => {
    if (!personaID || personaID === (task.assignee_id || task.worker_id)) return;
    setAssigning(true);
    try {
      await api.assignTask({
        task_id: task.id,
        assignee_id: personaID,
        assigned_by: "user",
        expected_outcome: task.expected_outcome,
        acceptance_criteria: task.acceptance_criteria,
      });
      await Promise.all([onChanged(), openCurrentRoute()]);
    } finally {
      setAssigning(false);
    }
  };

  return (
    <article
      role="button"
      tabIndex={0}
      onClick={() => void openOrigin()}
      onKeyDown={(e) => {
        if (e.key === "Enter" || e.key === " ") void openOrigin();
      }}
      className={cn(
        "cursor-pointer border border-border bg-panel px-3 py-2.5 text-left transition-colors hover:border-text-faint hover:bg-accent",
        column === "doing" && "border-l-[5px] border-l-running",
        column === "review" && "border-l-[5px] border-l-accent",
      )}
    >
      <div className="line-clamp-2 break-words text-[13px] font-semibold leading-[1.35] text-text">
        {task.title || "Untitled task"}
      </div>
      <div className="mt-2 flex items-center justify-between gap-2 text-[11px] text-text-faint">
        <span className="truncate">to @{assignee}</span>
        <span className="shrink-0 font-mono">{relTime(task.updated_at)}</span>
      </div>
      <div className="mt-1 truncate text-[11px] text-text-muted">by @{assignedBy}</div>
      <div className="mt-1 truncate text-[11px] text-text-muted">{source}</div>
      {task.acceptance_criteria && (
        <div className="mt-2 line-clamp-2 border-l-2 border-border pl-2 text-[11.5px] leading-snug text-text-muted">
          {task.acceptance_criteria}
        </div>
      )}
      {executors.length > 0 && (
        <div className="mt-2" onClick={(e) => e.stopPropagation()}>
          <select
            value={task.assignee_id || task.worker_id || ""}
            disabled={assigning}
            onChange={(e) => void assignTo(e.target.value)}
            className={cn(
              "w-full border border-border bg-panel-2 px-2 py-1 font-mono text-[11px] text-text-muted outline-none hover:text-text focus:border-text-faint",
              assigning && "opacity-50",
            )}
          >
            <option value="">Assign to agent...</option>
            {executors.map((p) => (
              <option key={p.id} value={p.id}>
                @{p.display || p.id}
              </option>
            ))}
          </select>
        </div>
      )}
      <div className="mt-2 flex flex-wrap gap-1.5">
        {column === "todo" && (
          <TaskAction label="Start" busy={updating === "doing"} onClick={(e) => void updateStatus("doing", e)} />
        )}
        {column === "doing" && (
          <TaskAction label="Ready for review" busy={updating === "review"} onClick={(e) => void updateStatus("review", e)} />
        )}
        {column === "review" && (
          <>
            <TaskAction label="Done" busy={updating === "done"} onClick={(e) => void updateStatus("done", e)} />
            <TaskAction label="Close" busy={updating === "closed"} onClick={(e) => void updateStatus("closed", e)} />
          </>
        )}
      </div>
    </article>
  );
}

function canExecuteTask(p: PersonaItem): boolean {
  return (p.capabilities || []).some((cap) => normalizeCapability(cap) === "task.execute");
}

function TaskAction({
  label,
  busy,
  onClick,
}: {
  label: string;
  busy: boolean;
  onClick: (e: React.MouseEvent) => void;
}) {
  return (
    <button
      type="button"
      onClick={onClick}
      className={cn(
        "border border-border bg-panel-2 px-2 py-0.5 font-mono text-[10.5px] text-text-muted hover:text-text",
        busy && "pointer-events-none opacity-50",
      )}
    >
      {busy ? "..." : label}
    </button>
  );
}

function taskSourceLabel(
  task: TaskStateCard,
  channels: ChannelItem[],
  directChats: DirectChatItem[],
  agentDMs: AgentDMItem[],
): string {
  const channel = channels.find((c) => c.id === task.space_id);
  if (channel) return task.parent_message_id ? `#${channel.name} · thread` : `#${channel.name}`;
  const direct = directChats.find((d) => d.id === task.space_id);
  if (direct) return `direct · ${direct.title}`;
  const agent = agentDMs.find((d) => d.id === task.space_id);
  if (agent) return `agent · ${agent.title}`;
  return task.space_id || "workspace";
}
