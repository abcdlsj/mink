import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Link, useParams } from "@tanstack/react-router";
import { CircleCheck, CircleDot, Clock, Link2, ListTodo, Menu, RotateCcw, Search, Unlink, XCircle } from "lucide-react";
import { type FormEvent, useState } from "react";

import {
  closeTask,
  completeTask,
  getTask,
  linkTaskThread,
  listAgents,
  listTasks,
  requestTaskChanges,
  resetTaskSession,
  startTask,
  submitTaskReview,
  unlinkTaskThread,
  updateTask,
  type Run,
  type Task,
  type TaskStatus,
  type ThreadReference,
} from "../api/client";
import { SpaceShell } from "../components/SpaceShell";

type TaskFilter = "all_open" | TaskStatus | "assigned_to_me";

const filters: Array<{ id: TaskFilter; label: string }> = [
  { id: "all_open", label: "All open" },
  { id: "todo", label: "TODO" },
  { id: "in_progress", label: "In Progress" },
  { id: "in_review", label: "In Review" },
  { id: "done", label: "Done" },
  { id: "closed", label: "Closed" },
  { id: "assigned_to_me", label: "Assigned to me" },
];

const openOrder: Record<TaskStatus, number> = {
  in_review: 0,
  in_progress: 1,
  todo: 2,
  done: 3,
  closed: 4,
};

export function TasksPage() {
  const { spaceSlug } = useParams({ from: "/s/$spaceSlug/tasks" });
  return (
    <SpaceShell spaceSlug={spaceSlug} active="tasks">
      {({ space, openNavigation }) => (
        <TaskList
          spaceId={space.id}
          spaceSlug={space.slug}
          currentMemberId={space.current_member_id}
          openNavigation={openNavigation}
        />
      )}
    </SpaceShell>
  );
}

function TaskList({ spaceId, spaceSlug, currentMemberId, openNavigation }: { spaceId: string; spaceSlug: string; currentMemberId: string; openNavigation: () => void }) {
  const [filter, setFilter] = useState<TaskFilter>("all_open");
  const tasks = useQuery({ queryKey: ["tasks", spaceId], queryFn: () => listTasks(spaceId) });
  const visible = (tasks.data ?? [])
    .filter((task) => filter === "all_open"
      ? !["done", "closed"].includes(task.status)
      : filter === "assigned_to_me"
        ? task.assignee_agent_member_id === currentMemberId
        : task.status === filter)
    .sort((left, right) => openOrder[left.status] - openOrder[right.status] || right.updated_at.localeCompare(left.updated_at));

  return (
    <section className="tasks-workspace" aria-labelledby="tasks-heading">
      <header className="tasks-header">
        <button className="mobile-menu icon-button" type="button" aria-label="Open navigation" onClick={openNavigation}><Menu /></button>
        <div><p className="page-kicker">WORK CONTINUITY</p><h1 id="tasks-heading">Tasks</h1><p>Tasks keep formal work connected to its Threads and Result.</p></div>
        <span className="task-total">{visible.length} SHOWN</span>
      </header>
      <nav className="task-filters" aria-label="Task filters">
        {filters.map((item) => <button key={item.id} type="button" aria-pressed={filter === item.id} onClick={() => setFilter(item.id)}>{item.label}</button>)}
      </nav>
      {tasks.isPending ? <div className="route-status">Loading Tasks…</div> : null}
      {tasks.error ? <div className="route-status route-status--error" role="alert">Tasks unavailable. Retry from this page.</div> : null}
      {!tasks.isPending && !tasks.error && visible.length === 0 ? <div className="tasks-empty"><ListTodo /><h2>No matching Tasks</h2><p>Create a Task from a Root Message in Conversation.</p></div> : null}
      {visible.length ? <div className="task-list" role="list">{visible.map((task) => <TaskRow key={task.id} task={task} spaceSlug={spaceSlug} />)}</div> : null}
    </section>
  );
}

function TaskRow({ task, spaceSlug }: { task: Task; spaceSlug: string }) {
  return (
    <article className="task-row" role="listitem">
      <TaskStatusLabel status={task.status} />
      <Link className="task-row-main" to="/s/$spaceSlug/tasks/$taskId" params={{ spaceSlug, taskId: task.id }}>
        <strong>{task.title}</strong>
      </Link>
      <span className="task-row-meta">
        {task.assignee_name ?? "Unassigned"}
        {" · "}
        <ThreadLink thread={task.source_thread} spaceSlug={spaceSlug} label="Source" />
      </span>
      <time dateTime={task.updated_at}>{formatTaskTime(task.updated_at)}</time>
    </article>
  );
}

export function TaskDetailPage() {
  const { spaceSlug, taskId } = useParams({ from: "/s/$spaceSlug/tasks/$taskId" });
  return (
    <SpaceShell spaceSlug={spaceSlug} active="tasks">
      {({ space, openNavigation }) => <TaskDetail taskId={taskId} spaceId={space.id} spaceSlug={space.slug} openNavigation={openNavigation} />}
    </SpaceShell>
  );
}

function TaskDetail({ taskId, spaceId, spaceSlug, openNavigation }: { taskId: string; spaceId: string; spaceSlug: string; openNavigation: () => void }) {
  const queryClient = useQueryClient();
  const task = useQuery({ queryKey: ["task", taskId], queryFn: () => getTask(taskId) });
  const agents = useQuery({ queryKey: ["agents", spaceId], queryFn: () => listAgents(spaceId) });
  const change = useMutation({
    mutationFn: async (operation: () => Promise<Task>) => operation(),
    onSuccess: (updated) => {
      queryClient.setQueryData(["task", taskId], updated);
      void queryClient.invalidateQueries({ queryKey: ["tasks", updated.space_id] });
    },
  });

  if (task.isPending) return <div className="route-status">Loading Task…</div>;
  if (task.error || !task.data) return <div className="route-status route-status--error" role="alert">Task unavailable or outside your visible Threads.</div>;
  const value = task.data;

  function rename(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    const title = String(new FormData(event.currentTarget).get("title") ?? "").trim();
    if (title && title !== value.title) change.mutate(() => updateTask(value.id, { title }));
  }
  function linkThread(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    const form = event.currentTarget;
    const threadId = String(new FormData(form).get("thread_id") ?? "").trim();
    if (threadId) change.mutate(() => linkTaskThread(value.id, { thread_id: threadId }), { onSuccess: () => form.reset() });
  }
  function finish(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    const result = String(new FormData(event.currentTarget).get("result") ?? "").trim();
    if (result) change.mutate(() => completeTask(value.id, { result_thread_id: value.current_run?.focus.id ?? value.source_thread.id, result_markdown: result }));
  }
  function close(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    const form = new FormData(event.currentTarget);
    change.mutate(() => closeTask(value.id, { reason: String(form.get("reason")) as "invalid" | "duplicate" | "not_needed" | "obsolete" | "other", note: String(form.get("note") ?? "") || undefined }));
  }

  return (
    <section className="task-detail" aria-labelledby="task-heading">
      <header className="task-detail-header">
        <button className="mobile-menu icon-button" type="button" aria-label="Open navigation" onClick={openNavigation}><Menu /></button>
        <div><p className="page-kicker">TASK</p><h1 id="task-heading">{value.title}</h1></div>
        <TaskStatusLabel status={value.status} />
      </header>
      {value.runtime_issue_code ? <p className="inline-notice inline-notice--error" role="alert">Task cannot run: <code>{value.runtime_issue_code}</code>. Restore compatible Thread membership or remove the Related Thread.</p> : null}
      {change.error ? <p className="inline-notice inline-notice--error" role="alert">The Task change failed. The current Server state is unchanged.</p> : null}
      <div className="task-detail-scroll">
        <section className="task-detail-section"><h2>Task</h2><form className="task-title-form" onSubmit={rename}><label>Title<input name="title" defaultValue={value.title} required /></label><button className="command-button" type="submit" disabled={change.isPending}>Save title</button></form><dl className="detail-grid"><Field label="Assignee" value={value.assignee_name ?? "Unassigned"} /><Field label="Created by" value={value.creator_name} /><Field label="Updated" value={new Date(value.updated_at).toLocaleString()} /></dl></section>
        {!value.finished_at ? <section className="task-detail-section"><h2>Actions</h2><div className="task-actions">{value.status === "todo" ? <label>Assignee<select defaultValue="" onChange={(event) => event.target.value && change.mutate(() => startTask(value.id, event.target.value))}><option value="" disabled>Start with Agent…</option>{(agents.data ?? []).filter((agent) => agent.desired_lifecycle === "active").map((agent) => <option key={agent.member_id} value={agent.member_id}>{agent.name}</option>)}</select></label> : null}{value.status === "in_progress" ? <button className="command-button" type="button" onClick={() => change.mutate(() => submitTaskReview(value.id))}>Submit review</button> : null}{value.status === "in_review" ? <button className="command-button" type="button" onClick={() => change.mutate(() => requestTaskChanges(value.id))}>Request changes</button> : null}{["in_progress", "in_review"].includes(value.status) ? <form onSubmit={finish}><label>Result Message<textarea name="result" required /></label><button className="command-button command-button--accent" type="submit">Mark Done</button></form> : null}<form onSubmit={close}><label>Close reason<select name="reason" defaultValue="invalid"><option value="invalid">Invalid</option><option value="duplicate">Duplicate</option><option value="not_needed">Not needed</option><option value="obsolete">Obsolete</option><option value="other">Other</option></select></label><label>Note<input name="note" /></label><button className="danger-button" type="submit"><XCircle /> Close Task</button></form></div></section> : null}
        <section className="task-detail-section"><h2>Source Thread</h2><ThreadReferenceRow thread={value.source_thread} spaceSlug={spaceSlug} /></section>
        <section className="task-detail-section"><h2>Related Threads</h2>{value.related_threads.length ? <ul className="linked-thread-list">{value.related_threads.map((thread) => <li key={thread.id}><ThreadReferenceRow thread={thread} spaceSlug={spaceSlug} /><button className="icon-button" type="button" aria-label={`Unlink #${thread.channel_slug} Thread`} onClick={() => change.mutate(() => unlinkTaskThread(value.id, thread.id))}><Unlink /></button></li>)}</ul> : <p>No Related Threads.</p>} {!value.finished_at ? <form className="link-thread-form" onSubmit={linkThread}><label>Related Thread ID<input name="thread_id" required /></label><button className="command-button" type="submit" disabled={change.isPending}><Link2 /> Link Thread</button></form> : null}</section>
        <section className="task-detail-section"><h2>Current Run and Focus</h2>{value.current_run ? <RunSummary run={value.current_run} spaceSlug={spaceSlug} /> : <p>No active Run. Task status does not imply an active Run.</p>}</section>
        <section className="task-detail-section"><h2>Recent Run outcomes</h2>{value.recent_runs.length ? <ul className="run-history">{value.recent_runs.map((run) => <li key={run.id}><RunSummary run={run} spaceSlug={spaceSlug} /></li>)}</ul> : <p>No completed Runs.</p>}</section>
        {value.status === "done" ? <section className="task-detail-section"><h2>Result</h2>{value.result_message?.content.type === "text" ? <p className="task-result">{value.result_message.content.body_markdown}</p> : <p>Result Message is unavailable.</p>}</section> : null}
        {value.status === "closed" ? <section className="task-detail-section"><h2>Close reason</h2><p>{closeReasonLabel(value.close_reason_code)}{value.close_reason_note ? ` — ${value.close_reason_note}` : ""}</p></section> : null}
        <section className="task-detail-section session-continuity"><h2>Session continuity</h2><SessionState task={value} /><p>This state only describes the next Run's startup cost. Message, Task and Result facts remain on Server.</p>{!["done", "closed"].includes(value.status) ? <button className="command-button" type="button" disabled={change.isPending} onClick={() => change.mutate(() => resetTaskSession(value.id))}><RotateCcw /> Reset continuity</button> : null}</section>
      </div>
    </section>
  );
}

function TaskStatusLabel({ status }: { status: TaskStatus }) {
  const Icon = status === "done" ? CircleCheck : status === "closed" ? XCircle : status === "in_review" ? Search : status === "in_progress" ? CircleDot : ListTodo;
  return <span className={`task-status task-status--${status}`}><Icon aria-hidden="true" />{status.replace("_", " ").toUpperCase()}</span>;
}
function ThreadLink({ thread, spaceSlug, label }: { thread: ThreadReference; spaceSlug: string; label: string }) { return <Link className="task-source-link" to="/s/$spaceSlug/channels/$channelSlug" params={{ spaceSlug, channelSlug: thread.channel_slug }} hash={`message-${thread.root_message_id}`} aria-label={`${label}: #${thread.channel_slug} @${thread.root_message_seq}`}>#{thread.channel_slug} @{thread.root_message_seq}</Link>; }
function ThreadReferenceRow({ thread, spaceSlug }: { thread: ThreadReference; spaceSlug: string }) { return <div className="thread-reference"><span>{thread.relation === "source" ? "SOURCE" : "RELATED"}</span><ThreadLink thread={thread} spaceSlug={spaceSlug} label={thread.relation} /><code>{thread.id}</code></div>; }
function RunSummary({ run, spaceSlug }: { run: Run; spaceSlug: string }) { return <article className="run-summary"><span className={`run-status run-status--${run.status}`}><Clock aria-hidden="true" />{run.status.replace("_", " ")}</span><strong>{run.agent_name}</strong><ThreadLink thread={run.focus} spaceSlug={spaceSlug} label="Focus" />{run.outcome ? <small>Outcome: {run.outcome}</small> : null}{run.error_code ? <code>{run.error_code}</code> : null}{run.continuation_note ? <p>{run.continuation_note}</p> : null}</article>; }
function SessionState({ task }: { task: Task }) { const value = task.session_continuity; return <p className={`session-state session-state--${value.state}`}><strong>{value.state.replace("_", " ").toUpperCase()}</strong>{value.generation ? ` · generation ${value.generation}` : ""}{value.reason_code ? ` · ${value.reason_code}` : ""}</p>; }
function Field({ label, value }: { label: string; value: string }) { return <div><dt>{label}</dt><dd>{value}</dd></div>; }
function formatTaskTime(value: string) { return new Intl.DateTimeFormat(undefined, { month: "short", day: "numeric", hour: "2-digit", minute: "2-digit" }).format(new Date(value)); }
function closeReasonLabel(reason: Task["close_reason_code"]) { return reason ? reason.replace("_", " ").replace(/^./, (value) => value.toUpperCase()) : "Unknown"; }
