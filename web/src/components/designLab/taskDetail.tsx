import { ArrowLeft, Check, CircleDot, Eye, Hash, ListTodo, XCircle } from "lucide-react";

import { PixelIdentity } from "../PixelIdentity";
import type { SurfaceDemo } from "./demoTypes";

const MOCK_TASK = {
  seq: 3,
  title: "Timeline density",
  status: "in_review" as const,
  assignee: "Leo",
  reviewer: "Nora",
  channel: "design-lab",
  rootSeq: 7,
  runs: [
    { id: "r1", label: "Working", time: "15:59 → 16:01", note: "Review body ready" },
    { id: "r2", label: "Yielded", time: "16:01", note: "paused for input" },
    { id: "r3", label: "Working", time: "16:02 → 16:03", note: "submitted review" },
  ],
  result: "Verified and accepted.",
};

function StatusGlyph({ status }: { status: "todo" | "in_progress" | "in_review" | "done" | "closed" }) {
  const Icon = status === "done" ? Check : status === "in_review" ? Eye : status === "in_progress" ? CircleDot : status === "closed" ? XCircle : ListTodo;
  return <Icon aria-hidden="true" />;
}

function WorkLine() {
  return (
    <ol className="dl-td-line">
      <li className="dl-td-node dl-td-node--source">
        <span className="dl-td-glyph"><Hash aria-hidden="true" /></span>
        <div><strong>Source thread</strong><span>#design-lab · root !7</span></div>
      </li>
      {MOCK_TASK.runs.map((run) => (
        <li key={run.id} className={`dl-td-node dl-td-node--${run.label.toLowerCase()}`}>
          <span className="dl-td-dot" />
          <div><strong>{run.label}</strong><span>{run.time}{run.note ? ` · ${run.note}` : ""}</span></div>
        </li>
      ))}
      <li className="dl-td-node dl-td-node--review">
        <span className="dl-td-glyph"><StatusGlyph status="in_review" /></span>
        <div><strong>In review · Nora</strong><span>reviewer can be Human or Agent</span></div>
      </li>
    </ol>
  );
}

function TaskHeader() {
  return (
    <header className="dl-td-header">
      <LinkBack />
      <div className="dl-td-title">
        <span className="dl-td-kicker">TASK !{MOCK_TASK.seq}</span>
        <h1>{MOCK_TASK.title}</h1>
      </div>
      <span className="dl-td-status"><StatusGlyph status={MOCK_TASK.status} /> IN REVIEW · {MOCK_TASK.reviewer}</span>
      <button type="button" className="dl-td-action">Start</button>
      <button type="button" className="dl-td-action dl-td-action--accent">Done</button>
    </header>
  );
}

function LinkBack() {
  return <span className="dl-td-back"><ArrowLeft aria-hidden="true" /> Tasks</span>;
}

function TD1Demo() {
  return (
    <div className="demo-page demo-page--td">
      <main className="demo-main">
        <TaskHeader />
        <div className="dl-td-layout">
          <section className="dl-td-panel">
            <h2>Work line</h2>
            <WorkLine />
          </section>
          <aside className="dl-td-side">
            <dl className="dl-td-facts">
              <div><dt>Assignee</dt><dd><PixelIdentity name="Leo" kind="agent" seed="019c0000-0000-7000-8000-000000000020" /> Leo</dd></div>
              <div><dt>Status</dt><dd>In Review · Nora</dd></div>
              <div><dt>Linked threads</dt><dd>#design-lab:7 · #general:12</dd></div>
              <div><dt>Session</dt><dd>Warm</dd></div>
            </dl>
          </aside>
        </div>
      </main>
    </div>
  );
}

function TD2Demo() {
  return (
    <div className="demo-page demo-page--td">
      <main className="demo-main">
        <TaskHeader />
        <div className="dl-td-stack">
          <section className="dl-td-card"><h2>Overview</h2><dl className="dl-td-facts"><div><dt>Assignee</dt><dd>Leo</dd></div><div><dt>Status</dt><dd>In Review · Nora</dd></div><div><dt>Session</dt><dd>Warm</dd></div></dl></section>
          <section className="dl-td-card"><h2>Source &amp; linked threads</h2><p>#design-lab:7 (source) · #general:12 (related)</p></section>
          <section className="dl-td-card"><h2>Runs</h2><WorkLine /></section>
          <section className="dl-td-card"><h2>Result</h2><p>Verified and accepted.</p></section>
        </div>
      </main>
    </div>
  );
}

function TD3Demo() {
  return (
    <div className="demo-page demo-page--td">
      <main className="demo-main">
        <TaskHeader />
        <div className="dl-td-split">
          <section className="dl-td-card"><h2>Overview</h2><dl className="dl-td-facts"><div><dt>Assignee</dt><dd>Leo</dd></div><div><dt>Status</dt><dd>In Review · Nora</dd></div><div><dt>Session</dt><dd>Warm</dd></div></dl></section>
          <section className="dl-td-card"><h2>Activity</h2><WorkLine /></section>
        </div>
      </main>
    </div>
  );
}

export const TASK_DETAIL_DEMOS: SurfaceDemo[] = [
  { id: "td1", label: "TD1 · 工作线主导", note: "左工作线 + 右信息；Task 生命史一条线看全。", Component: TD1Demo },
  { id: "td2", label: "TD2 · 分区堆叠", note: "现状改良：Overview / Threads / Runs / Result 清晰分区。", Component: TD2Demo },
  { id: "td3", label: "TD3 · 双栏", note: "概述 + 活动双卡，信息密度居中。", Component: TD3Demo },
];
