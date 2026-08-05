import { Hash, Inbox, ListTodo, MessageCircle, Monitor, Palette, Users } from "lucide-react";

import { PixelIdentity } from "../PixelIdentity";
import type { SurfaceDemo } from "./demoTypes";

function RailIcons({ active = "conversation" }: { active?: string }) {
  const items = [
    { icon: MessageCircle, label: "Conversation", id: "conversation" },
    { icon: Inbox, label: "Inbox", id: "inbox" },
    { icon: ListTodo, label: "Tasks", id: "tasks" },
    { icon: Users, label: "Members", id: "members" },
    { icon: Monitor, label: "Computers", id: "computers" },
    { icon: Palette, label: "Design", id: "design" },
  ];
  return (
    <aside className="demo-rail">
      <span className="demo-rail-space">S</span>
      {items.map((item) => (
        <span
          key={item.id}
          className={`demo-rail-tool${active === item.id ? " demo-rail-tool--active" : ""}`}
          title={item.label}
          aria-label={item.label}
        >
          <item.icon aria-hidden="true" />
        </span>
      ))}
    </aside>
  );
}

function NavList({ compact = false }: { compact?: boolean }) {
  const channels = ["general", "design-lab", "ship-metadata", "empty-lab"];
  const dms = ["Iris", "Leo"];
  return (
    <nav className={`demo-nav${compact ? " demo-nav--compact" : ""}`}>
      <p className="demo-nav-label">CHANNELS</p>
      {channels.map((channel, index) => (
        <span key={channel} className={`demo-nav-item${index === 0 ? " demo-nav-item--active" : ""}`}>
          <Hash aria-hidden="true" /> {channel}
        </span>
      ))}
      <p className="demo-nav-label">DMS</p>
      {dms.map((dm) => (
        <span key={dm} className="demo-nav-item"><MessageCircle aria-hidden="true" /> {dm}</span>
      ))}
      <p className="demo-nav-label">SPACE</p>
      <span className="demo-nav-item"><Users aria-hidden="true" /> Members</span>
      <span className="demo-nav-item"><Monitor aria-hidden="true" /> Computers</span>
    </nav>
  );
}

function Messages({ compact = false }: { compact?: boolean }) {
  const rows = [
    { name: "Mara", kind: "human" as const, seed: "019c0000-0000-7000-8000-000000000002", text: "Kicking off the design review. The pixel seal stays, the palette moves." },
    { name: "Leo", kind: "agent" as const, seed: "019c0000-0000-7000-8000-000000000020", text: "Task !7 is in review — summary attached.", task: true },
    { name: "Nora", kind: "agent" as const, seed: "019c0000-0000-7000-8000-000000000021", text: "Review notes: density is good, spacing around badges needs one more pass." },
  ];
  return (
    <div className={`demo-timeline${compact ? " demo-timeline--compact" : ""}`}>
      {rows.map((row) => (
        <article key={row.name} className="demo-message">
          <PixelIdentity name={row.name} kind={row.kind} seed={row.seed} />
          <div className="demo-message-body">
            <header>
              <strong>{row.name}</strong>
              {row.kind === "agent" ? <span className="demo-agent-label">AGENT</span> : null}
              {row.task ? <span className="demo-task-meta">!7 · IN REVIEW</span> : null}
              <time>14:32</time>
            </header>
            <p>{row.text}</p>
          </div>
        </article>
      ))}
    </div>
  );
}

function Composer({ variant = "f1" }: { variant?: "f1" | "f2" | "f3" }) {
  return (
    <footer className={`demo-composer demo-composer--${variant}`}>
      <span>Message #general...</span>
    </footer>
  );
}

function L1Demo() {
  return (
    <div className="demo-page demo-page--l1">
      <RailIcons />
      <NavList />
      <main className="demo-main">
        <header className="demo-channel-header"><Hash aria-hidden="true" /> general <span className="demo-channel-members">Mara · Iris · Leo · Nora</span></header>
        <Messages />
        <Composer />
        <aside className="demo-thread-pane">
          <strong>Thread</strong>
          <p>root + replies</p>
        </aside>
      </main>
    </div>
  );
}

function L2Demo() {
  return (
    <div className="demo-page demo-page--l2">
      <RailIcons />
      <NavList compact />
      <main className="demo-main">
        <header className="demo-channel-header"><Hash aria-hidden="true" /> general <span className="demo-channel-members">4 members</span></header>
        <Messages compact />
        <Composer variant="f3" />
      </main>
    </div>
  );
}

function L3Demo() {
  return (
    <div className="demo-page demo-page--l3">
      <header className="demo-topbar">
        <span className="demo-rail-space">S</span>
        <strong>Sumi Dev</strong>
        <span className="demo-topbar-search">Search Space...</span>
        <span className="demo-topbar-tool"><Inbox aria-hidden="true" /></span>
        <span className="demo-topbar-tool"><ListTodo aria-hidden="true" /></span>
        <span className="demo-topbar-tool"><Palette aria-hidden="true" /></span>
      </header>
      <NavList />
      <main className="demo-main">
        <header className="demo-channel-header"><Hash aria-hidden="true" /> general <span className="demo-channel-members">Mara · Iris · Leo · Nora</span></header>
        <Messages />
        <Composer variant="f2" />
      </main>
    </div>
  );
}

export const SHELL_DEMOS: SurfaceDemo[] = [
  { id: "l1", label: "L1 · 现状三栏", note: "rail + nav + 主区，Thread 在主区右栏；现有骨架的完整还原。", Component: L1Demo },
  { id: "l2", label: "L2 · 紧凑双栏", note: "窄 rail + 紧凑 nav，无常驻 Thread；密度最高。", Component: L2Demo },
  { id: "l3", label: "L3 · 顶栏式", note: "顶部全局条 + 频道列表 + 搜索，完全脱离三栏；Thread 以浮层出现。", Component: L3Demo },
];
