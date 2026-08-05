import { useQuery } from "@tanstack/react-query";
import { Link, useParams } from "@tanstack/react-router";
import {
  Hash,
  Inbox,
  ListTodo,
  MessageCircle,
  Monitor,
  Users,
} from "lucide-react";
import { useState, type CSSProperties } from "react";

import { listTasks, type Run, type Task } from "../api/client";
import { PixelIdentity, SpaceShell } from "../components/SpaceShell";
import {
  FONT_CANDIDATES,
  PALETTE_CANDIDATES,
  PIXEL_SCALE,
  SPACE_ACCENT_SETS,
  TASK_BADGE_CANDIDATES,
} from "../designCandidates";
import "../styles/design-lab.css";

// Mock reviewer until Task carries a reviewer field (AX input).
const DEMO_REVIEWER = "Nora";

export function DesignLabPage() {
  const { spaceSlug } = useParams({ from: "/s/$spaceSlug/design-lab" });
  return (
    <SpaceShell spaceSlug={spaceSlug} active="design">
      {({ space }) => <DesignLabWorkspace spaceId={space.id} spaceSlug={space.slug} />}
    </SpaceShell>
  );
}

function DesignLabWorkspace({ spaceId, spaceSlug }: { spaceId: string; spaceSlug: string }) {
  const [paletteId, setPaletteId] = useState(PALETTE_CANDIDATES[0].id);
  const [fontId, setFontId] = useState(FONT_CANDIDATES[0].id);
  const [accentSetId, setAccentSetId] = useState(SPACE_ACCENT_SETS[0].id);

  const palette = PALETTE_CANDIDATES.find((candidate) => candidate.id === paletteId)!;
  const font = FONT_CANDIDATES.find((candidate) => candidate.id === fontId)!;
  const accentSet = SPACE_ACCENT_SETS.find((candidate) => candidate.id === accentSetId)!;

  const tasks = useQuery({
    queryKey: ["tasks", spaceId],
    queryFn: () => listTasks(spaceId),
    enabled: Boolean(spaceId),
  });
  const sampleTask = tasks.data?.find((task) => task.status === "in_review") ?? tasks.data?.[0];

  const variables = {
    ...palette.tokens,
    "--space-accent": accentSet.accents[0],
    "--font-sans": font.sans,
    "--font-mono": font.mono,
  } as CSSProperties;

  return (
    <section className="design-lab" style={variables} aria-label="Design candidates">
      <header className="design-lab-header">
        <div>
          <h1>Design candidates</h1>
          <p>Demo-only comparison board. Pick a direction, then we refine it against the product.</p>
        </div>
        <span className="design-lab-note">not in product navigation</span>
      </header>

      <div className="design-lab-controls">
        <div className="design-lab-control">
          <span className="design-lab-control-label">Palette</span>
          <div className="design-lab-segment" role="group" aria-label="Palette candidates">
            {PALETTE_CANDIDATES.map((candidate) => (
              <button
                key={candidate.id}
                type="button"
                aria-pressed={candidate.id === paletteId}
                onClick={() => setPaletteId(candidate.id)}
              >
                {candidate.label}
              </button>
            ))}
          </div>
          <p className="design-lab-control-note">{palette.note}</p>
        </div>
        <div className="design-lab-control">
          <span className="design-lab-control-label">Typeface</span>
          <div className="design-lab-segment" role="group" aria-label="Typeface candidates">
            {FONT_CANDIDATES.map((candidate) => (
              <button
                key={candidate.id}
                type="button"
                aria-pressed={candidate.id === fontId}
                onClick={() => setFontId(candidate.id)}
              >
                {candidate.label}
              </button>
            ))}
          </div>
          <p className="design-lab-control-note">{font.note}</p>
        </div>
        <div className="design-lab-control">
          <span className="design-lab-control-label">Space accents</span>
          <div className="design-lab-accents" role="group" aria-label="Space accent families">
            {SPACE_ACCENT_SETS.map((set) => (
              <button
                key={set.id}
                type="button"
                className={`design-lab-accent-set${set.id === accentSetId ? " design-lab-accent-set--active" : ""}`}
                aria-pressed={set.id === accentSetId}
                onClick={() => setAccentSetId(set.id)}
              >
                <span style={{ background: set.accents[0] }} />
                <span style={{ background: set.accents[1] }} />
                <span style={{ background: set.accents[2] }} />
                <span style={{ background: set.accents[3] }} />
              </button>
            ))}
          </div>
        </div>
      </div>

      <div className="design-lab-font-sample">
        <header>
          <h2>Typeface sample</h2>
          <span>now rendering: {font.label}</span>
        </header>
        <p>
          Sumi lets Human and Agent collaborate in one Space. 持续在线、可被打断、可转向的协作者，工作跨 Run 持续。
          <strong> Bold weight carries the message. </strong>
          <em>Italic keeps the emphasis quiet.</em>
        </p>
        <code>!3 · IN REVIEW · Nora — seq 000124 · 14:32:05</code>
      </div>

      <h2 className="design-lab-section-title">Shell + components</h2>
      <MiniShell accents={accentSet.accents} />

      <h2 className="design-lab-section-title">Task badge on messages</h2>
      <p className="design-lab-section-note">
        User feedback: the current badge is too large and clashes with the message. Three shapes to compare.
      </p>
      <TaskBadgeCompare task={sampleTask} />

      <h2 className="design-lab-section-title">Work line (AX)</h2>
      <p className="design-lab-section-note">
        Task life drawn as one continuous line: Source Thread → Runs → Review → Result.
        Rendered from real data (Task {sampleTask ? `!${sampleTask.seq}` : ""}).
      </p>
      {sampleTask ? <WorkLine task={sampleTask} spaceSlug={spaceSlug} /> : <p>No sample task.</p>}

      <h2 className="design-lab-section-title">AX signals</h2>
      <div className="design-lab-grid">
        <ReviewSignal task={sampleTask} />
        <FocusSignal />
        <PixelScale />
      </div>
    </section>
  );
}

function MiniShell({ accents }: { accents: readonly [string, string, string, string] }) {
  const navItems = [
    { icon: MessageCircle, label: "Conversations", active: true },
    { icon: Inbox, label: "Inbox" },
    { icon: ListTodo, label: "Tasks" },
    { icon: Users, label: "Members" },
    { icon: Monitor, label: "Computers" },
  ];
  return (
    <div className="dl-shell">
      <aside className="dl-rail">
        <span className="dl-rail-badge" style={{ background: accents[0] }}>S</span>
        {navItems.slice(0, 3).map((item) => (
          <span key={item.label} className="dl-rail-icon" title={item.label}>
            <item.icon aria-hidden="true" />
          </span>
        ))}
      </aside>
      <nav className="dl-nav">
        {navItems.map((item) => (
          <span key={item.label} className={`dl-nav-item${item.active ? " dl-nav-item--active" : ""}`}>
            <item.icon aria-hidden="true" />
            {item.label}
            {item.active && <i className="dl-nav-marker" aria-hidden="true" />}
          </span>
        ))}
      </nav>
      <main className="dl-main">
        <div className="dl-button-row">
          <button type="button" className="command-button">Primary action</button>
          <button type="button" className="command-button command-button--accent">Accent action</button>
          <button type="button" className="quiet-button">Quiet</button>
          <button type="button" className="danger-button">Delete</button>
        </div>
        <div className="dl-status-row">
          <span className="task-status task-status--todo">TODO</span>
          <span className="task-status task-status--in_progress">IN PROGRESS</span>
          <span className="task-status task-status--in_review">IN REVIEW</span>
          <span className="task-status task-status--done">DONE</span>
          <span className="task-status task-status--closed">CLOSED</span>
        </div>
        <div className="dl-message">
          <PixelIdentity name="Mara" kind="human" seed="019c0000-0000-7000-8000-000000000002" />
          <div className="dl-message-body">
            <header><strong>Mara</strong><time>14:32</time></header>
            <p>A sample message inside the candidate shell.</p>
          </div>
        </div>
      </main>
    </div>
  );
}

function TaskBadgeCompare({ task }: { task?: Task }) {
  if (!task) return <p className="design-lab-section-note">Create samples first (mise run demo).</p>;
  return (
    <div className="dl-badge-compare">
      {TASK_BADGE_CANDIDATES.map((candidate) => (
        <article key={candidate.id} className="dl-badge-card">
          <h3>{candidate.label}</h3>
          <div className="dl-badge-message">
            <PixelIdentity name="Mara" kind="human" seed="019c0000-0000-7000-8000-000000000002" />
            <div className="dl-message-body">
              <header>
                <strong>Mara</strong><time>14:32</time>
                {candidate.id === "inline-pill" && <TaskPill task={task} />}
                {candidate.id === "metadata-row" && <TaskMeta task={task} />}
              </header>
              <p>This is the message body the badge must not compete with.</p>
              {candidate.id === "current" && (
                <Link
                  className="message-task-badge message-task-badge--in_review"
                  to="/s/$spaceSlug/tasks/$taskId"
                  params={{ spaceSlug: "sumi-dev", taskId: task.id }}
                >
                  <PixelStatusGlyph status={task.status} />
                  <b>!{task.seq}</b>
                  <span>{task.title}</span>
                </Link>
              )}
            </div>
          </div>
          <p className="dl-badge-note">{candidate.note}</p>
        </article>
      ))}
    </div>
  );
}

function TaskPill({ task }: { task: Task }) {
  return (
    <Link
      className="dl-task-pill"
      to="/s/$spaceSlug/tasks/$taskId"
      params={{ spaceSlug: "sumi-dev", taskId: task.id }}
      title={task.title}
    >
      <PixelStatusGlyph status={task.status} />
      <b>!{task.seq}</b>
      <span>{statusLabel(task.status)}</span>
    </Link>
  );
}

function TaskMeta({ task }: { task: Task }) {
  return (
    <Link
      className="dl-task-meta"
      to="/s/$spaceSlug/tasks/$taskId"
      params={{ spaceSlug: "sumi-dev", taskId: task.id }}
      title={task.title}
    >
      <b>!{task.seq}</b>
      <span className={`dl-task-meta-status dl-task-meta-status--${task.status}`}>
        {statusLabel(task.status)}
      </span>
      {task.status === "in_review" && <span className="dl-task-reviewer">· {DEMO_REVIEWER}</span>}
    </Link>
  );
}

function WorkLine({ task, spaceSlug }: { task: Task; spaceSlug: string }) {
  const runs = task.recent_runs;
  const visibleRuns = runs.slice(0, 5);
  const olderCount = runs.length - visibleRuns.length;
  return (
    <div className="dl-workline">
      <ol className="dl-workline-vertical">
        <li className="dl-node dl-node--source">
          <span className="dl-node-glyph"><Hash aria-hidden="true" /></span>
          <div>
            <strong>Source thread</strong>
            <span>#{task.source_thread.channel_slug} · root !{task.source_thread.root_message_seq}</span>
          </div>
        </li>
        {olderCount > 0 && (
          <li className="dl-node dl-node--collapsed">
            <span className="dl-node-dot" />
            <div><span>{olderCount} earlier runs (collapsed)</span></div>
          </li>
        )}
        {visibleRuns.map((run) => <RunNode key={run.id} run={run} spaceSlug={spaceSlug} />)}
        {task.current_run && task.current_run.id === visibleRuns[0]?.id && (
          <li className="dl-node dl-node--live">
            <span className="dl-node-dot dl-node-dot--live" />
            <div><strong>Running now</strong></div>
          </li>
        )}
        {task.status === "in_review" && (
          <li className="dl-node dl-node--review">
            <span className="dl-node-glyph"><PixelStatusGlyph status="in_review" /></span>
            <div>
              <strong>In review · {DEMO_REVIEWER}</strong>
              <span>reviewer can be Human or Agent, depending on assignment</span>
            </div>
          </li>
        )}
        {task.status === "done" && task.result_message && (
          <li className="dl-node dl-node--result">
            <span className="dl-node-glyph"><PixelStatusGlyph status="done" /></span>
            <div>
              <strong>Result</strong>
              <span>message !{task.result_message.seq} in the Source Thread</span>
            </div>
          </li>
        )}
        {task.status === "closed" && (
          <li className="dl-node dl-node--closed">
            <span className="dl-node-glyph"><PixelStatusGlyph status="closed" /></span>
            <div>
              <strong>Closed · {task.close_reason_code}</strong>
              {task.close_reason_note && <span>{task.close_reason_note}</span>}
            </div>
          </li>
        )}
      </ol>
      <div className="dl-workline-horizontal" aria-hidden="true">
        <span>Source</span>
        <i />
        <span>Runs ×{runs.length}</span>
        <i />
        <span>Review · {DEMO_REVIEWER}</span>
        <i />
        <span>Result</span>
      </div>
    </div>
  );
}

function RunNode({ run, spaceSlug }: { run: Run; spaceSlug: string }) {
  return (
    <li className={`dl-node dl-node--run dl-node--run-${run.status}`}>
      <span className="dl-node-dot" />
      <div>
        <strong>{runLabel(run)}</strong>
        <span>
          focus #...:{run.focus.root_message_seq} · {formatTime(run.started_at)}
          {run.finished_at && ` → ${formatTime(run.finished_at)}`}
        </span>
        {run.error_code && <code>{run.error_code}</code>}
      </div>
      <Link
        className="dl-node-link"
        to="/s/$spaceSlug/channels/$channelSlug"
        params={{ spaceSlug, channelSlug: run.focus.channel_slug ?? "general" }}
        aria-label="Open focus thread"
      >
        Open
      </Link>
    </li>
  );
}

function ReviewSignal({ task }: { task?: Task }) {
  return (
    <article className="dl-ax-card">
      <h3>Review shows the reviewer</h3>
      <p>In Review must answer who is holding the line — Human or Agent.</p>
      {task && (
        <div className="dl-review-mock">
          <PixelStatusGlyph status="in_review" />
          <span>IN REVIEW · {DEMO_REVIEWER}</span>
        </div>
      )}
      <p className="dl-ax-note">Requires a reviewer field on Task (currently not in the API).</p>
    </article>
  );
}

function FocusSignal() {
  return (
    <article className="dl-ax-card">
      <h3>Agent focus marker</h3>
      <p>A quiet marker on the Agent seal when it has an active Run; hover reveals the Focus.</p>
      <div className="dl-focus-mock">
        <span className="dl-focus-avatar">
          <PixelIdentity name="Leo" kind="agent" seed="019c0000-0000-7000-8000-000000000020" />
          <i className="dl-focus-dot" title="active run" aria-label="active run" />
        </span>
        <span className="dl-focus-label">Leo · focus #design-lab:7</span>
      </div>
    </article>
  );
}

function PixelScale() {
  return (
    <article className="dl-ax-card">
      <h3>Pixel component scale</h3>
      <p>One 8×8 grid, integer pixel units only. Avatars render at 16–64px.</p>
      <div className="dl-pixel-scale">
        {PIXEL_SCALE.map((step) => (
          <div key={step.size} className="dl-pixel-step">
            <span className={`dl-pixel-sample dl-pixel-sample--${step.size}`}>
              <PixelIdentity name="Lin" kind="agent" seed="019c0000-0000-7000-8000-000000000020" />
            </span>
            <span className="dl-pixel-meta">{step.size}px · {step.unit}px unit</span>
            <small>{step.use}</small>
          </div>
        ))}
      </div>
    </article>
  );
}

function PixelStatusGlyph({ status }: { status: Task["status"] }) {
  const shapes: Record<Task["status"], ReadonlyArray<readonly [number, number]>> = {
    todo: [[1, 1], [2, 1], [3, 1], [4, 1], [5, 1], [6, 1], [1, 6], [2, 6], [3, 6], [4, 6], [5, 6], [6, 6], [1, 2], [1, 3], [1, 4], [1, 5], [6, 2], [6, 3], [6, 4], [6, 5]],
    in_progress: [[2, 1], [3, 1], [4, 1], [5, 1], [6, 2], [1, 2], [1, 3], [1, 4], [1, 5], [6, 5], [2, 6], [3, 6], [4, 6], [5, 6], [3, 3], [4, 3], [3, 4], [4, 4]],
    in_review: [[1, 2], [2, 1], [3, 1], [4, 1], [5, 1], [6, 2], [6, 3], [6, 4], [5, 5], [4, 5], [3, 5], [2, 5], [1, 4], [1, 3], [3, 3], [4, 3], [3, 4], [4, 4]],
    done: [[1, 4], [2, 5], [3, 6], [4, 5], [5, 4], [6, 3], [6, 2], [7, 2]],
    closed: [[1, 1], [2, 2], [3, 3], [4, 4], [3, 5], [2, 6], [1, 7], [6, 1], [5, 2], [4, 3], [4, 4], [5, 5], [6, 6], [7, 7]],
  };
  return (
    <svg viewBox="0 0 8 8" className="dl-pixel-glyph" aria-hidden="true" shapeRendering="crispEdges">
      {shapes[status].map(([x, y]) => <rect key={`${x}-${y}`} x={x} y={y} width="1" height="1" />)}
    </svg>
  );
}

function runLabel(run: Run): string {
  if (run.status === "yielded") return `Yielded${run.continuation_note ? " — paused for input" : ""}`;
  if (run.status === "working") return "Working";
  if (run.status === "dispatched") return "Dispatched";
  if (run.outcome) return `Run ${run.outcome}`;
  return `Run ${run.status}`;
}

function statusLabel(status: Task["status"]): string {
  return status === "in_progress" ? "IN PROGRESS" : status === "in_review" ? "IN REVIEW" : status.toUpperCase();
}

function formatTime(value: string | null | undefined): string {
  if (!value) return "";
  return new Date(value).toLocaleTimeString("en-GB", { hour: "2-digit", minute: "2-digit" });
}
