import { useQuery } from "@tanstack/react-query";
import { Link, useParams } from "@tanstack/react-router";
import {
  Hash,
  ListTodo,
  MessageCircle,
} from "lucide-react";
import { useState, type CSSProperties } from "react";

// Candidate typefaces are only loaded for the comparison board.
import "@fontsource-variable/hanken-grotesk";
import "@fontsource-variable/instrument-sans";
import "@fontsource-variable/manrope";
import "@fontsource/ibm-plex-mono/400.css";
import "@fontsource/ibm-plex-mono/600.css";
import "@fontsource/spline-sans-mono/400.css";
import "@fontsource/spline-sans-mono/600.css";
import "@fontsource/jetbrains-mono/400.css";
import "@fontsource/jetbrains-mono/600.css";

import { listTasks, type Run, type Task } from "../api/client";
import { ChannelCandidates } from "../components/designLab/ChannelCandidates";
import { ComposerCandidates } from "../components/designLab/ComposerCandidates";
import { ComputersCandidates } from "../components/designLab/ComputersCandidates";
import { InboxCandidates } from "../components/designLab/InboxCandidates";
import { MembersCandidates } from "../components/designLab/MembersCandidates";
import { OnboardingCandidates } from "../components/designLab/OnboardingCandidates";
import { ShellCandidates } from "../components/designLab/ShellCandidates";
import { ThreadCandidates } from "../components/designLab/ThreadCandidates";
import { PixelIdentity, SpaceShell } from "../components/SpaceShell";
import {
  FONT_CANDIDATES,
  MESSAGE_ACTIONS_CANDIDATES,
  PALETTE_CANDIDATES,
  PIXEL_SCALE,
  SPACE_ACCENT_SETS,
  TASK_BADGE_CANDIDATES,
} from "../designCandidates";
import "../styles/design-lab.css";

// Mock reviewer until Task carries a reviewer field (AX input).
const DEMO_REVIEWER = "Nora";

const SECTIONS = [
  { id: "shell", label: "Shell layout" },
  { id: "channel", label: "Channel" },
  { id: "messages", label: "Message & Task" },
  { id: "composer", label: "Composer" },
  { id: "thread", label: "Thread" },
  { id: "inbox", label: "Inbox" },
  { id: "members", label: "Members & Agents" },
  { id: "computers", label: "Computers" },
  { id: "onboarding", label: "Onboarding" },
  { id: "ax", label: "AX signals" },
  { id: "pixels", label: "Pixel scale" },
] as const;

export function DesignLabPage() {
  const { spaceSlug } = useParams({ from: "/s/$spaceSlug/design-lab" });
  return (
    <SpaceShell spaceSlug={spaceSlug} active="design">
      {({ space }) => <DesignLabWorkspace spaceId={space.id} spaceSlug={space.slug} />}
    </SpaceShell>
  );
}

function DesignLabWorkspace({ spaceId, spaceSlug }: { spaceId: string; spaceSlug: string }) {
  const initial = new URLSearchParams(window.location.search);
  const [paletteId, setPaletteId] = useState(
    () => initial.get("palette") ?? PALETTE_CANDIDATES[0].id,
  );
  const [fontId, setFontId] = useState(() => initial.get("font") ?? FONT_CANDIDATES[0].id);
  const [accentSetId, setAccentSetId] = useState(
    () => initial.get("accents") ?? SPACE_ACCENT_SETS[0].id,
  );
  const [activeSection, setActiveSection] = useState<string>(SECTIONS[0].id);

  function choose(key: "palette" | "font" | "accents", value: string) {
    const url = new URL(window.location.href);
    url.searchParams.set(key, value);
    window.history.replaceState(null, "", url);
  }

  function goToSection(id: string) {
    setActiveSection(id);
    document.getElementById(`section-${id}`)?.scrollIntoView({ behavior: "smooth", block: "start" });
  }

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
          <p>Every surface has 2–3 shapes to compare. Selections sync to the URL.</p>
        </div>
        <span className="design-lab-note">demo only</span>
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
                onClick={() => {
                  setPaletteId(candidate.id);
                  choose("palette", candidate.id);
                }}
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
                onClick={() => {
                  setFontId(candidate.id);
                  choose("font", candidate.id);
                }}
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
                onClick={() => {
                  setAccentSetId(set.id);
                  choose("accents", set.id);
                }}
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

      <div className="design-lab-layout">
        <nav className="design-lab-toc" aria-label="Design sections">
          {SECTIONS.map((section) => (
            <button
              key={section.id}
              type="button"
              aria-pressed={activeSection === section.id}
              onClick={() => goToSection(section.id)}
            >
              {section.label}
            </button>
          ))}
        </nav>
        <div className="design-lab-sections">
          <section id="section-shell" className="design-lab-section">
            <h2>Shell layout</h2>
            <p className="design-lab-section-note">Three ways to distribute the whole page.</p>
            <ShellCandidates />
          </section>

          <section id="section-channel" className="design-lab-section">
            <h2>Channel</h2>
            <p className="design-lab-section-note">Message timeline treatments.</p>
            <ChannelCandidates />
          </section>

          <section id="section-messages" className="design-lab-section">
            <h2>Message & Task</h2>
            <p className="design-lab-section-note">
              Task badge placement and floating actions; work line below.
            </p>
            <TaskBadgeCompare task={sampleTask} />
            <MessageActionsCompare />
            {sampleTask ? <WorkLine task={sampleTask} spaceSlug={spaceSlug} /> : <p>No sample task.</p>}
          </section>

          <section id="section-composer" className="design-lab-section">
            <h2>Composer</h2>
            <ComposerCandidates />
          </section>

          <section id="section-thread" className="design-lab-section">
            <h2>Thread</h2>
            <ThreadCandidates />
          </section>

          <section id="section-inbox" className="design-lab-section">
            <h2>Inbox</h2>
            <InboxCandidates />
          </section>

          <section id="section-members" className="design-lab-section">
            <h2>Members & Agents</h2>
            <MembersCandidates />
          </section>

          <section id="section-computers" className="design-lab-section">
            <h2>Computers</h2>
            <ComputersCandidates />
          </section>

          <section id="section-onboarding" className="design-lab-section">
            <h2>Onboarding</h2>
            <OnboardingCandidates />
          </section>

          <section id="section-ax" className="design-lab-section">
            <h2>AX signals</h2>
            <div className="design-lab-grid">
              <ReviewSignal task={sampleTask} />
              <FocusSignal />
            </div>
          </section>

          <section id="section-pixels" className="design-lab-section">
            <h2>Pixel scale</h2>
            <PixelScale />
          </section>
        </div>
      </div>
    </section>
  );
}

function TaskBadgeCompare({ task }: { task?: Task }) {
  if (!task) return <p className="design-lab-section-note">Create samples first (mise run demo).</p>;
  return (
    <div className="dl-badge-compare">
      {TASK_BADGE_CANDIDATES.map((candidate) => (
        <article key={candidate.id} className="dl-badge-card">
          <h3>{candidate.label}</h3>
          <div className={`dl-badge-message dl-badge-message--${candidate.id}`}>
            <PixelIdentity name="Mara" kind="human" seed="019c0000-0000-7000-8000-000000000002" />
            <div className="dl-message-body">
              <header>
                <strong>Mara</strong>
                {candidate.id === "header-meta" && <TaskMeta task={task} />}
                {candidate.id !== "top-right" && <time>14:32</time>}
              </header>
              <p>This is the message body the badge must not compete with.</p>
              {candidate.id === "bottom-left" && <TaskMeta task={task} />}
            </div>
            {candidate.id === "top-right" && <TaskMeta task={task} />}
            {candidate.id === "top-right" && <time className="dl-badge-time-right">14:32</time>}
          </div>
          <p className="dl-badge-note">{candidate.note}</p>
        </article>
      ))}
    </div>
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

function MessageActionsCompare() {
  return (
    <div className="dl-actions-compare">
      {MESSAGE_ACTIONS_CANDIDATES.map((candidate) => (
        <article key={candidate.id} className="dl-badge-card">
          <h3>{candidate.label}</h3>
          <div className="dl-actions-message">
            <PixelIdentity name="Mara" kind="human" seed="019c0000-0000-7000-8000-000000000002" />
            <div className="dl-message-body">
              <header><strong>Mara</strong><time>14:32</time></header>
              <p>Message body that the floating actions must not cover.</p>
            </div>
            <div className={`dl-actions dl-actions--${candidate.id}`} aria-label="Message actions">
              <button type="button" title="Reply to thread" aria-label="Reply to Thread">
                <MessageCircle aria-hidden="true" />
              </button>
              <button type="button" title="Create Task" aria-label="Create Task">
                <ListTodo aria-hidden="true" />
              </button>
              {candidate.id === "text-actions" && <span>Reply · Task</span>}
            </div>
          </div>
          <p className="dl-badge-note">{candidate.note}</p>
        </article>
      ))}
    </div>
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
      <p>One 8×8 grid, integer pixel units only. Avatars render at 20–48px.</p>
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
