import { Hash, Inbox, ListTodo, MessageCircle, Monitor, Palette, Plus, Users } from "lucide-react";

import { PixelIdentity } from "../PixelIdentity";
import { PixelWord } from "../PixelWord";
import type { SurfaceDemo } from "./demoTypes";

const CHANNELS = ["general", "design-lab", "empty-lab"];
const DMS = [
  { name: "Iris", seed: "019c0000-0000-7000-8000-000000000021", online: true },
  { name: "Leo", seed: "019c0000-0000-7000-8000-000000000020", online: true },
];
const AGENTS = [
  { name: "Iris", seed: "019c0000-0000-7000-8000-000000000021", status: "working" },
  { name: "Leo", seed: "019c0000-0000-7000-8000-000000000020", status: "working" },
  { name: "Nora", seed: "019c0000-0000-7000-8000-000000000022", status: "waiting" },
];

function Tools({ horizontal = false }: { horizontal?: boolean }) {
  const items = [MessageCircle, Inbox, ListTodo, Users, Monitor, Palette];
  return (
    <div className={`dmn-tools${horizontal ? " dmn-tools--horizontal" : ""}`}>
      {items.map((Icon, index) => (
        <span key={index} className={`dmn-tool${index === 0 ? " dmn-tool--active" : ""}`}><Icon aria-hidden="true" /></span>
      ))}
    </div>
  );
}

function MainMock() {
  return (
    <main className="dmn-main">
      <header className="dmn-channel-header"><Hash aria-hidden="true" /> general <span className="dmn-members">Mara · Iris · Leo · Nora</span></header>
      <div className="dmn-messages">
        <div className="dmn-msg"><PixelIdentity name="Mara" kind="human" seed="019c0000-0000-7000-8000-000000000002" /><div><strong>Mara</strong><p>Kicking off the design review.</p></div></div>
        <div className="dmn-msg"><PixelIdentity name="Leo" kind="agent" seed="019c0000-0000-7000-8000-000000000020" /><div><strong>Leo</strong><p>Task !7 is in review.</p></div></div>
      </div>
      <footer className="dmn-composer">Message #general...</footer>
    </main>
  );
}

function M1Demo() {
  return (
    <div className="demo-page demo-page--dmn">
      <aside className="demo-rail"><span className="demo-rail-space">S</span></aside>
      <nav className="dmn dmn--m1">
        <div className="dmn-brand"><PixelWord text="Sumi Dev" /></div>
        <div className="dmn-scroll">
          <p className="dmn-label">CHANNELS <span>3</span><button type="button" aria-label="Create Channel"><Plus aria-hidden="true" /></button></p>
          {CHANNELS.map((channel, index) => (
            <span key={channel} className={`dmn-item${index === 0 ? " dmn-item--active" : ""}`}><Hash aria-hidden="true" /> {channel}</span>
          ))}
          <p className="dmn-label">DMS <span>2</span></p>
          {DMS.map((dm) => (
            <span key={dm.name} className="dmn-item"><PixelIdentity name={dm.name} kind="agent" seed={dm.seed} /> {dm.name}<i className="dmn-dot" /></span>
          ))}
        </div>
        <Tools />
      </nav>
      <MainMock />
    </div>
  );
}

function M2Demo() {
  return (
    <div className="demo-page demo-page--dmn">
      <aside className="demo-rail"><span className="demo-rail-space">S</span></aside>
      <nav className="dmn dmn--m2">
        <div className="dmn-brand"><PixelWord text="Sumi Dev" /></div>
        <div className="dmn-scroll">
          <p className="dmn-label">CHANNELS</p>
          {CHANNELS.map((channel, index) => (
            <span key={channel} className={`dmn-item${index === 0 ? " dmn-item--active" : ""}`}><Hash aria-hidden="true" /> {channel}</span>
          ))}
          <p className="dmn-label">DMS</p>
          {DMS.map((dm) => (
            <span key={dm.name} className="dmn-item"><PixelIdentity name={dm.name} kind="agent" seed={dm.seed} /> {dm.name}</span>
          ))}
        </div>
        <Tools horizontal />
      </nav>
      <MainMock />
    </div>
  );
}

function M3Demo() {
  return (
    <div className="demo-page demo-page--dmn">
      <aside className="demo-rail"><span className="demo-rail-space">S</span></aside>
      <nav className="dmn dmn--m3">
        <div className="dmn-brand"><PixelWord text="Sumi Dev" /></div>
        <div className="dmn-scroll">
          <p className="dmn-label">CHANNELS <span>3</span><button type="button" aria-label="Create Channel"><Plus aria-hidden="true" /></button></p>
          {CHANNELS.map((channel, index) => (
            <span key={channel} className={`dmn-item${index === 0 ? " dmn-item--active" : ""}`}><Hash aria-hidden="true" /> {channel}{index === 0 ? <b className="dmn-count">8</b> : null}</span>
          ))}
          <p className="dmn-label">AGENTS <span>3</span></p>
          {AGENTS.map((agent) => (
            <span key={agent.name} className="dmn-item"><PixelIdentity name={agent.name} kind="agent" seed={agent.seed} /> {agent.name}<i className={`dmn-status dmn-status--${agent.status}`} /></span>
          ))}
          <p className="dmn-label">DMS <span>2</span></p>
          {DMS.map((dm) => (
            <span key={dm.name} className="dmn-item"><PixelIdentity name={dm.name} kind="agent" seed={dm.seed} /> {dm.name}</span>
          ))}
        </div>
        <Tools />
      </nav>
      <MainMock />
    </div>
  );
}

function M4Demo() {
  return (
    <div className="demo-page demo-page--dmn">
      <aside className="demo-rail"><span className="demo-rail-space">S</span></aside>
      <nav className="dmn dmn--m4">
        <div className="dmn-brand"><PixelWord text="Sumi Dev" /></div>
        <div className="dmn-scroll">
          <p className="dmn-label">CHANNELS</p>
          {CHANNELS.map((channel, index) => (
            <span key={channel} className={`dmn-item${index === 0 ? " dmn-item--active" : ""}`}><Hash aria-hidden="true" /> {channel}</span>
          ))}
          <p className="dmn-label">DMS</p>
          {DMS.map((dm) => (
            <span key={dm.name} className="dmn-item"><PixelIdentity name={dm.name} kind="agent" seed={dm.seed} /> {dm.name}</span>
          ))}
        </div>
      </nav>
      <MainMock />
    </div>
  );
}

export const MIDDLE_NAV_DEMOS: SurfaceDemo[] = [
  { id: "m1", label: "M1 · 现状", note: "294px：像素字品牌 + CHANNELS/DMS 分组 + 底部工具。", Component: M1Demo },
  { id: "m2", label: "M2 · 紧凑", note: "240px：密度更高，底部工具改横向一行。", Component: M2Demo },
  { id: "m3", label: "M3 · 分组增强", note: "300px：CHANNELS/AGENTS/DMS 带计数与状态点，未读徽章。", Component: M3Demo },
  { id: "m4", label: "M4 · 极简", note: "260px：只有品牌与列表，无底部工具区，最轻。", Component: M4Demo },
];
