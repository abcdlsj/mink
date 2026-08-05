import { Hash, MessageCircle, Users } from "lucide-react";

import { PixelIdentity } from "../PixelIdentity";
import type { SurfaceDemo } from "./demoTypes";

const AGENTS = [
  { name: "Iris", seed: "019c0000-0000-7000-8000-000000000021", status: "working", perms: "channel.create · agent.create" },
  { name: "Leo", seed: "019c0000-0000-7000-8000-000000000020", status: "working", perms: "—" },
  { name: "Nora", seed: "019c0000-0000-7000-8000-000000000022", status: "waiting", perms: "—" },
];

function MembersPage({ variant }: { variant: "rows" | "groups" | "dense" }) {
  return (
    <div className="demo-page demo-page--members">
      <aside className="demo-rail">
        <span className="demo-rail-space">S</span>
        <span className="demo-rail-tool demo-rail-tool--active"><MessageCircle aria-hidden="true" /></span>
        <span className="demo-rail-tool"><Hash aria-hidden="true" /></span>
      </aside>
      <main className="demo-main">
        <header className="demo-channel-header"><Users aria-hidden="true" /> Members <span className="demo-channel-members">4 people · 3 agents</span></header>
        <div className={`demo-members demo-members--${variant}`}>
          {variant === "groups" ? <p className="demo-members-label">AGENTS</p> : null}
          {AGENTS.map((agent) => (
            <article key={agent.name} className="demo-member">
              <PixelIdentity name={agent.name} kind="agent" seed={agent.seed} />
              <div className="demo-member-body">
                <strong>{agent.name}</strong>
                <span className="demo-member-status"><i /> {agent.status}</span>
                {variant !== "dense" && <small>{agent.perms}</small>}
              </div>
              <button type="button" aria-label={`Message ${agent.name}`}>✉</button>
            </article>
          ))}
          {variant === "groups" ? <p className="demo-members-label">HUMANS</p> : null}
          <article className="demo-member">
            <PixelIdentity name="Mara" kind="human" seed="019c0000-0000-7000-8000-000000000002" />
            <div className="demo-member-body">
              <strong>Mara</strong>
              <span className="demo-member-status">member</span>
            </div>
            <button type="button" aria-label="Message Mara">✉</button>
          </article>
        </div>
      </main>
    </div>
  );
}

export const MEMBERS_DEMOS: SurfaceDemo[] = [
  { id: "m1", label: "M1 · 扁平行", note: "身份 + 状态 + 权限 + 消息按钮，行间细分隔。", Component: () => <MembersPage variant="rows" /> },
  { id: "m2", label: "M2 · 分组", note: "Agents / Humans 分组，权限摘要一行。", Component: () => <MembersPage variant="groups" /> },
  { id: "m3", label: "M3 · 密集", note: "行高收紧、权限省略、状态点更小。", Component: () => <MembersPage variant="dense" /> },
];
