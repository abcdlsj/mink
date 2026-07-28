import { useQuery } from "@tanstack/react-query";
import { Link, useParams } from "@tanstack/react-router";
import { ArrowUpRight, Circle, CircleCheck, CircleDot, Menu, OctagonX } from "lucide-react";

import { listTasks, type Task } from "../api/client";
import { SpaceShell } from "../components/SpaceShell";

const groups: Array<{
  status: Task["status"];
  label: string;
  icon: typeof Circle;
}> = [
  { status: "open", label: "Open", icon: Circle },
  { status: "in_progress", label: "In progress", icon: CircleDot },
  { status: "done", label: "Done", icon: CircleCheck },
  { status: "canceled", label: "Canceled", icon: OctagonX },
];

export function TasksPage() {
  const { spaceSlug } = useParams({ from: "/s/$spaceSlug/tasks" });
  return (
    <SpaceShell spaceSlug={spaceSlug} active="tasks">
      {({ space, openNavigation }) => <TaskWorkspace spaceId={space.id} spaceSlug={space.slug} openNavigation={openNavigation} />}
    </SpaceShell>
  );
}

function TaskWorkspace({ spaceId, spaceSlug, openNavigation }: { spaceId: string; spaceSlug: string; openNavigation: () => void }) {
  const tasks = useQuery({ queryKey: ["tasks", spaceId], queryFn: () => listTasks(spaceId) });
  return (
    <section className="tasks-workspace" aria-labelledby="tasks-heading">
      <header className="tasks-header">
        <button className="mobile-menu icon-button" type="button" aria-label="Open navigation" title="Open navigation" onClick={openNavigation}><Menu /></button>
        <div>
          <p className="page-kicker">AGENT TASK FLOW</p>
          <h1 id="tasks-heading">Tasks</h1>
          <p>Claim and movement happen through <code>sumi agent task</code>.</p>
        </div>
        <span className="task-total">{tasks.data?.length ?? 0} TOTAL</span>
      </header>
      {tasks.isPending ? <div className="route-status">Loading Tasks...</div> : null}
      {tasks.error ? <div className="route-status route-status--error">{tasks.error.message}</div> : null}
      {tasks.data?.length === 0 ? (
        <div className="tasks-empty"><CircleDot /><h2>No Tasks yet</h2><p>An Agent can convert a Channel root Message with <code>sumi agent task convert</code>.</p></div>
      ) : null}
      {tasks.data ? (
        <div className="task-board">
          {groups.map((group) => {
            const items = tasks.data.filter((task) => task.status === group.status);
            const Icon = group.icon;
            return (
              <section className={`task-column task-column--${group.status}`} key={group.status} aria-labelledby={`task-${group.status}`}>
                <header><Icon aria-hidden="true" /><h2 id={`task-${group.status}`}>{group.label}</h2><span>{items.length}</span></header>
                <div className="task-column-list">
                  {items.map((task) => (
                    <article className="task-card" key={task.id}>
                      <h3>{task.title}</h3>
                      <p>{task.assignee_name ? `Assigned to ${task.assignee_name}` : "Unassigned"}</p>
                      <Link
                        className="task-source-link"
                        to="/s/$spaceSlug/channels/$channelSlug"
                        params={{ spaceSlug, channelSlug: task.channel_slug }}
                        hash={`message-${task.source_message_id}`}
                      >
                        <span>#{task.channel_slug} @{task.source_seq}</span><ArrowUpRight aria-hidden="true" />
                      </Link>
                      <footer><span>BY {task.creator_name}</span><time dateTime={task.updated_at}>{formatTaskTime(task.updated_at)}</time></footer>
                    </article>
                  ))}
                  {!items.length ? <p className="task-column-empty">Nothing here</p> : null}
                </div>
              </section>
            );
          })}
        </div>
      ) : null}
    </section>
  );
}

function formatTaskTime(value: string) {
  return new Intl.DateTimeFormat(undefined, { month: "short", day: "numeric" }).format(new Date(value));
}
