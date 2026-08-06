import { Hash, Inbox, ListTodo, MessageCircle, Monitor, Palette, Plus, Users } from "lucide-react";

import { PixelIdentity } from "../PixelIdentity";
import { PixelWord } from "../PixelWord";
import type { SurfaceDemo } from "./demoTypes";

const CHANNELS = [
  { slug: "general", unread: 0 },
  { slug: "design-lab", unread: 3 },
  { slug: "empty-lab", unread: 0 },
];
const DISCOVER = ["ship-metadata"];
const DMS = [
  { name: "Iris", seed: "019c0000-0000-7000-8000-000000000021", online: true, unread: 1 },
  { name: "Leo", seed: "019c0000-0000-7000-8000-000000000020", online: true, unread: 0 },
];

export function Rail() {
  const items = [
    { icon: MessageCircle, label: "Conversation", active: true },
    { icon: Inbox, label: "Inbox" },
    { icon: ListTodo, label: "Tasks" },
    { icon: Users, label: "Members" },
    { icon: Monitor, label: "Computers" },
    { icon: Palette, label: "Design" },
  ];
  return (
    <aside className="demo-rail">
      <span className="demo-rail-space">S</span>
      {items.map((item) => (
        <span key={item.label} className={`demo-rail-tool${item.active ? " demo-rail-tool--active" : ""}`} title={item.label}><item.icon aria-hidden="true" /></span>
      ))}
    </aside>
  );
}

function Brand({ compact = false }: { compact?: boolean }) {
  return (
    <div className={`dmn-brand${compact ? " dmn-brand--compact" : ""}`}>
      <PixelWord text="Sumi Dev" />
    </div>
  );
}

function ChannelItems({ dense = false }: { dense?: boolean }) {
  return (
    <>
      {CHANNELS.map((channel, index) => (
        <span key={channel.slug} className={`dmn-item${index === 0 ? " dmn-item--active" : ""}${dense ? " dmn-item--dense" : ""}`}>
          <Hash aria-hidden="true" /> {channel.slug}
          {channel.unread ? <b className="dmn-count">{channel.unread}</b> : null}
        </span>
      ))}
    </>
  );
}

function DiscoverItems() {
  return DISCOVER.map((channel) => (
    <span key={channel} className="dmn-item dmn-item--discover">
      <Hash aria-hidden="true" /> {channel}
      <button type="button">JOIN</button>
    </span>
  ));
}

function DmItems({ withStatus = false, dense = false }: { withStatus?: boolean; dense?: boolean }) {
  return DMS.map((dm) => (
    <span key={dm.name} className={`dmn-item${dense ? " dmn-item--dense" : ""}`}>
      <span className="dmn-avatar">
        <PixelIdentity name={dm.name} kind="agent" seed={dm.seed} />
        {withStatus ? <i className={`dmn-presence-dot${dm.online ? " dmn-presence-dot--online" : ""}`} /> : null}
      </span>
      {dm.name}
      {dm.unread ? <b className="dmn-count">{dm.unread}</b> : null}
    </span>
  ));
}

export function MainMock() {
  return (
    <main className="dmn-main">
      <header className="dmn-channel-header"><Hash aria-hidden="true" /> general <span className="dmn-members">Mara · Iris · Leo · Nora</span></header>
      <div className="dmn-messages">
        <div className="dmn-msg"><PixelIdentity name="Mara" kind="human" seed="019c0000-0000-7000-8000-000000000002" /><div><strong>Mara</strong><time>14:32</time><p>Kicking off the design review.</p></div></div>
        <div className="dmn-msg"><PixelIdentity name="Leo" kind="agent" seed="019c0000-0000-7000-8000-000000000020" /><div><strong>Leo</strong><time>14:34</time><p>Task !7 is in review.</p></div></div>
      </div>
      <footer className="dmn-composer">Message #general...</footer>
    </main>
  );
}

function N1Demo() {
  return (
    <div className="demo-page demo-page--dmn">
      <Rail />
      <nav className="dmn dmn--n1">
        <Brand />
        <div className="dmn-scroll">
          <p className="dmn-label">CHANNELS <span>{CHANNELS.length}</span><button type="button" aria-label="Create Channel"><Plus aria-hidden="true" /></button></p>
          <ChannelItems />
          <p className="dmn-label">DISCOVER</p>
          <DiscoverItems />
          <p className="dmn-label">DMS <span>{DMS.length}</span><button type="button" aria-label="Start DM"><Plus aria-hidden="true" /></button></p>
          <DmItems withStatus />
        </div>
      </nav>
      <MainMock />
    </div>
  );
}

function N2Demo() {
  return (
    <div className="demo-page demo-page--dmn">
      <Rail />
      <nav className="dmn dmn--n2">
        <Brand compact />
        <div className="dmn-scroll dmn-scroll--dense">
          <p className="dmn-label">CHANNELS <span>{CHANNELS.length}</span><button type="button" aria-label="Create Channel"><Plus aria-hidden="true" /></button></p>
          <ChannelItems dense />
          <p className="dmn-label">DISCOVER</p>
          <DiscoverItems />
          <p className="dmn-label">DMS <span>{DMS.length}</span><button type="button" aria-label="Start DM"><Plus aria-hidden="true" /></button></p>
          <DmItems dense />
        </div>
      </nav>
      <MainMock />
    </div>
  );
}

function N3Demo() {
  return (
    <div className="demo-page demo-page--dmn">
      <Rail />
      <nav className="dmn dmn--n3">
        <Brand />
        <div className="dmn-scroll">
          <p className="dmn-label">CHANNELS <span>{CHANNELS.length}</span><button type="button" aria-label="Create Channel"><Plus aria-hidden="true" /></button></p>
          <ChannelItems />
          <div className="dmn-divider" />
          <p className="dmn-label">DISCOVER</p>
          <DiscoverItems />
          <div className="dmn-divider" />
          <p className="dmn-label">DMS <span>{DMS.length}</span><button type="button" aria-label="Start DM"><Plus aria-hidden="true" /></button></p>
          <DmItems withStatus />
        </div>
      </nav>
      <MainMock />
    </div>
  );
}

function N4Demo() {
  return (
    <div className="demo-page demo-page--dmn">
      <Rail />
      <nav className="dmn dmn--n4">
        <Brand />
        <div className="dmn-scroll">
          <p className="dmn-label">CHANNELS <span>{CHANNELS.length}</span><button type="button" aria-label="Create Channel"><Plus aria-hidden="true" /></button></p>
          <ChannelItems />
          <p className="dmn-label">DISCOVER</p>
          <DiscoverItems />
          <p className="dmn-label">DMS <span>{DMS.length}</span><button type="button" aria-label="Start DM"><Plus aria-hidden="true" /></button></p>
          <DmItems withStatus />
        </div>
      </nav>
      <MainMock />
    </div>
  );
}

export const MIDDLE_NAV_DEMOS: SurfaceDemo[] = [
  { id: "n1", label: "N1 · 现状", note: "当前中间栏：品牌像素字 + CHANNELS/DISCOVER/DMS 分组。", Component: N1Demo },
  { id: "n2", label: "N2 · 紧凑", note: "同样内容，行高与间距收紧，密度更高。", Component: N2Demo },
  { id: "n3", label: "N3 · 物件感", note: "分组用 ink 分隔线，active 项硬边 + 硬阴影，保持物件语言。", Component: N3Demo },
  { id: "n4", label: "N4 · 信号克制", note: "未读用硬边小方块，DM 在线点贴头像角，无装饰性徽章。", Component: N4Demo },
];
