import { Hash, MessageCircle } from "lucide-react";

import { PixelIdentity } from "../PixelIdentity";
import type { SurfaceDemo } from "./demoTypes";

const ROWS = [
  { name: "Mara", kind: "human" as const, seed: "019c0000-0000-7000-8000-000000000002", text: "Kicking off the design review. Keep the pixel seal, move the palette." },
  { name: "Leo", kind: "agent" as const, seed: "019c0000-0000-7000-8000-000000000020", text: "Task !7 submitted for review — summary in the thread.", task: true },
  { name: "Nora", kind: "agent" as const, seed: "019c0000-0000-7000-8000-000000000021", text: "Review notes: density is good; spacing around the badge needs one more pass." },
];

function ChannelShell({ children, centered = false }: { children: React.ReactNode; centered?: boolean }) {
  return (
    <div className="demo-page demo-page--channel">
      <aside className="demo-rail">
        <span className="demo-rail-space">S</span>
        <span className="demo-rail-tool demo-rail-tool--active"><MessageCircle aria-hidden="true" /></span>
        <span className="demo-rail-tool"><Hash aria-hidden="true" /></span>
      </aside>
      <main className={`demo-main${centered ? " demo-main--centered" : ""}`}>
        <header className="demo-channel-header"><Hash aria-hidden="true" /> general <span className="demo-channel-members">Mara · Iris · Leo · Nora</span></header>
        {children}
      </main>
    </div>
  );
}

function Row({ row, bubble = false, dense = false }: { row: typeof ROWS[number]; bubble?: boolean; dense?: boolean }) {
  return (
    <article className={`demo-msg${bubble ? " demo-msg--bubble" : ""}${dense ? " demo-msg--dense" : ""}`}>
      <PixelIdentity name={row.name} kind={row.kind} seed={row.seed} />
      <div className="demo-msg-body">
        <header>
          <strong>{row.name}</strong>
          {row.kind === "agent" ? <span className="demo-agent-label">AGENT</span> : null}
          {row.task ? <span className="demo-task-meta">!7 · IN REVIEW</span> : null}
          <time>14:32</time>
        </header>
        <p>{row.text}</p>
      </div>
    </article>
  );
}

function C1Demo() {
  return (
    <ChannelShell>
      <div className="demo-timeline">
        {ROWS.map((row) => <Row key={row.name} row={row} />)}
      </div>
      <footer className="demo-composer demo-composer--f1"><span>Message #general...</span></footer>
    </ChannelShell>
  );
}

export const CHANNEL_DEMOS: SurfaceDemo[] = [
  { id: "c1", label: "C1 · 行式", note: "现状消息行，hover 显示动作；时间在 header。已采用。", Component: C1Demo },
];
