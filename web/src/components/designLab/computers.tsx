import { Hash, MessageCircle, Monitor, Plus } from "lucide-react";

import type { SurfaceDemo } from "./demoTypes";

const COMPUTERS = [
  { name: "Dev Computer", version: "0.1.0", agents: 3, online: true },
  { name: "Studio Mac", version: "0.1.0", agents: 1, online: false },
];

function ComputersPage({ variant }: { variant: "list" | "cards" | "dense" }) {
  return (
    <div className="demo-page demo-page--computers">
      <aside className="demo-rail">
        <span className="demo-rail-space">S</span>
        <span className="demo-rail-tool demo-rail-tool--active"><MessageCircle aria-hidden="true" /></span>
        <span className="demo-rail-tool"><Hash aria-hidden="true" /></span>
      </aside>
      <main className="demo-main">
        <header className="demo-channel-header">
          <Monitor aria-hidden="true" /> Computers <span className="demo-channel-members">2 paired</span>
          <span className="demo-plus"><Plus aria-hidden="true" /></span>
        </header>
        <div className={`demo-computers demo-computers--${variant}`}>
          {COMPUTERS.map((computer) => (
            <article key={computer.name} className="demo-computer">
              <span className="demo-computer-icon"><Monitor aria-hidden="true" /></span>
              <div className="demo-computer-body">
                <strong>{computer.name}</strong>
                <span className="demo-computer-status"><i className={computer.online ? "demo-dot-online" : "demo-dot-offline"} /> {computer.online ? "online" : "offline"} · v{computer.version}</span>
              </div>
              <span className="demo-computer-agents">{computer.agents} agents</span>
            </article>
          ))}
        </div>
      </main>
    </div>
  );
}

export const COMPUTERS_DEMOS: SurfaceDemo[] = [
  { id: "cp1", label: "CP1 · 列表", note: "左侧列表 + 右侧详情/onboarding 的经典双区。", Component: () => <ComputersPage variant="list" /> },
  { id: "cp2", label: "CP2 · 卡片", note: "卡片网格，状态、版本、Agent 数一眼可见。", Component: () => <ComputersPage variant="cards" /> },
  { id: "cp3", label: "CP3 · 密集", note: "更小行高，状态用色点，信息靠右。", Component: () => <ComputersPage variant="dense" /> },
];
