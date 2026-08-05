import { Hash, Inbox, ListTodo, MessageCircle, Monitor, Users } from "lucide-react";
import type { ReactNode } from "react";

function Candidate({ label, note, children }: { label: string; note: string; children: ReactNode }) {
  return (
    <article className="dl-candidate">
      <h3>{label}</h3>
      <p>{note}</p>
      {children}
    </article>
  );
}

function Icon({ icon: IconComponent, active = false }: { icon: typeof Hash; active?: boolean }) {
  return <span className={`dl-l-tool${active ? " dl-l-tool--active" : ""}`}><IconComponent aria-hidden="true" /></span>;
}

export function ShellCandidates() {
  return (
    <div className="dl-region-grid">
      <Candidate
        label="L1 · 现状三栏"
        note="rail 64 + nav 294 + 主区；Thread 在主区内右栏。现有舒适感基线。"
      >
        <div className="dl-layout dl-layout--l1" aria-label="L1 layout">
          <aside className="dl-l-rail">
            <span className="dl-l-space">S</span>
            <Icon icon={MessageCircle} active />
            <Icon icon={Inbox} />
            <Icon icon={ListTodo} />
          </aside>
          <nav className="dl-l-nav">
            <strong>Conversations</strong>
            <span className="dl-l-nav-item dl-l-nav-item--active"><Hash aria-hidden="true" /> general</span>
            <span className="dl-l-nav-item"><Hash aria-hidden="true" /> design-lab</span>
            <strong>Tasks</strong>
            <span className="dl-l-nav-item"><ListTodo aria-hidden="true" /> Tasks</span>
          </nav>
          <main className="dl-l-main">
            <header className="dl-l-header"><Hash aria-hidden="true" /> general</header>
            <div className="dl-l-body">
              <div className="dl-l-message" />
              <div className="dl-l-message" />
              <div className="dl-l-message" />
            </div>
            <footer className="dl-l-composer">Message...</footer>
            <aside className="dl-l-thread">Thread</aside>
          </main>
        </div>
      </Candidate>

      <Candidate
        label="L2 · 紧凑双栏"
        note="rail 48 + nav 260 可折叠；主区更宽，Thread 仍为主区右栏。信息密度优先。"
      >
        <div className="dl-layout dl-layout--l2" aria-label="L2 layout">
          <aside className="dl-l-rail">
            <span className="dl-l-space">S</span>
            <Icon icon={MessageCircle} active />
            <Icon icon={Inbox} />
            <Icon icon={ListTodo} />
          </aside>
          <nav className="dl-l-nav">
            <strong>Channels</strong>
            <span className="dl-l-nav-item dl-l-nav-item--active"><Hash aria-hidden="true" /> general</span>
            <span className="dl-l-nav-item"><Hash aria-hidden="true" /> design-lab</span>
            <span className="dl-l-nav-item"><Users aria-hidden="true" /> Members</span>
            <span className="dl-l-nav-item"><Monitor aria-hidden="true" /> Computers</span>
          </nav>
          <main className="dl-l-main">
            <header className="dl-l-header"><Hash aria-hidden="true" /> general</header>
            <div className="dl-l-body">
              <div className="dl-l-message" />
              <div className="dl-l-message" />
              <div className="dl-l-message" />
            </div>
            <footer className="dl-l-composer">Message...</footer>
            <aside className="dl-l-thread">Thread</aside>
          </main>
        </div>
      </Candidate>

      <Candidate
        label="L3 · 顶栏 + 频道列表"
        note="顶部全局条 + 左侧频道列表；Thread 作为浮层覆盖。更像现代协作工具。"
      >
        <div className="dl-layout dl-layout--l3" aria-label="L3 layout">
          <header className="dl-l-topbar">
            <span className="dl-l-space">S</span>
            <span className="dl-l-topbar-title">Sumi Dev</span>
            <Icon icon={Inbox} />
            <Icon icon={ListTodo} />
            <Icon icon={Users} />
          </header>
          <nav className="dl-l-nav">
            <strong>Channels</strong>
            <span className="dl-l-nav-item dl-l-nav-item--active"><Hash aria-hidden="true" /> general</span>
            <span className="dl-l-nav-item"><Hash aria-hidden="true" /> design-lab</span>
            <strong>DMs</strong>
            <span className="dl-l-nav-item"><MessageCircle aria-hidden="true" /> Iris</span>
          </nav>
          <main className="dl-l-main">
            <header className="dl-l-header"><Hash aria-hidden="true" /> general</header>
            <div className="dl-l-body">
              <div className="dl-l-message" />
              <div className="dl-l-message" />
              <div className="dl-l-message" />
            </div>
            <footer className="dl-l-composer">Message...</footer>
            <aside className="dl-l-thread dl-l-thread--float">Thread</aside>
          </main>
        </div>
      </Candidate>
    </div>
  );
}
