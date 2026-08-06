import { MessageCircle, Plus, UserPlus } from "lucide-react";

import { PixelIdentity } from "../PixelIdentity";
import type { SurfaceDemo } from "./demoTypes";

const MEMBERS = [
  { name: "Iris", kind: "agent" as const, seed: "019c0000-0000-7000-8000-000000000021", status: "working", perms: ["channel.create", "agent.create"] },
  { name: "Leo", kind: "agent" as const, seed: "019c0000-0000-7000-8000-000000000020", status: "working", perms: [] },
  { name: "Nora", kind: "agent" as const, seed: "019c0000-0000-7000-8000-000000000022", status: "waiting", perms: [] },
  { name: "Mara", kind: "human" as const, seed: "019c0000-0000-7000-8000-000000000002", status: "member", perms: [] },
];

function MemberRow({ member, compact = false, card = false }: { member: typeof MEMBERS[number]; compact?: boolean; card?: boolean }) {
  return (
    <article className={`dl-mm-row${card ? " dl-mm-row--card" : ""}${compact ? " dl-mm-row--compact" : ""}`}>
      <PixelIdentity name={member.name} kind={member.kind} seed={member.seed} />
      <div className="dl-mm-body">
        <strong>{member.name}</strong>
        <span className="dl-mm-status"><i /> {member.status}</span>
      </div>
      <div className="dl-mm-perms">
        {member.kind === "agent" ? member.perms.map((perm) => <label key={perm}><input type="checkbox" defaultChecked /> {perm}</label>) : <span className="dl-mm-access">Member</span>}
      </div>
      <button type="button" className="dl-mm-message" aria-label={`Message ${member.name}`}><MessageCircle aria-hidden="true" /></button>
    </article>
  );
}

function ManagementHeader() {
  return (
    <header className="demo-channel-header">
      <span>Members</span>
      <span className="demo-channel-members">4 members · 3 agents</span>
      <button type="button" className="dl-mm-invite"><UserPlus aria-hidden="true" /> Invite Human</button>
      <button type="button" className="dl-mm-invite dl-mm-invite--accent"><Plus aria-hidden="true" /> Create Agent</button>
    </header>
  );
}

function MM1Demo() {
  return (
    <div className="demo-page demo-page--members">
      <aside className="demo-rail">
        <span className="demo-rail-space">S</span>
        <span className="demo-rail-tool demo-rail-tool--active"><MessageCircle aria-hidden="true" /></span>
      </aside>
      <main className="demo-main">
        <ManagementHeader />
        <div className="dl-mm-list">
          {MEMBERS.map((member) => <MemberRow key={member.name} member={member} />)}
        </div>
      </main>
    </div>
  );
}

function MM2Demo() {
  return (
    <div className="demo-page demo-page--members">
      <aside className="demo-rail">
        <span className="demo-rail-space">S</span>
        <span className="demo-rail-tool demo-rail-tool--active"><MessageCircle aria-hidden="true" /></span>
      </aside>
      <main className="demo-main">
        <ManagementHeader />
        <div className="dl-mm-grid">
          {MEMBERS.map((member) => <MemberRow key={member.name} member={member} card />)}
        </div>
      </main>
    </div>
  );
}

function MM3Demo() {
  return (
    <div className="demo-page demo-page--members">
      <aside className="demo-rail">
        <span className="demo-rail-space">S</span>
        <span className="demo-rail-tool demo-rail-tool--active"><MessageCircle aria-hidden="true" /></span>
      </aside>
      <main className="demo-main demo-main--mm-split">
        <ManagementHeader />
        <div className="dl-mm-split">
          <div className="dl-mm-list">
            {MEMBERS.map((member) => <MemberRow key={member.name} member={member} compact />)}
          </div>
          <aside className="dl-mm-manage">
            <h2>Manage Leo</h2>
            <p className="dl-mm-manage-label">Action permissions</p>
            <label><input type="checkbox" /> channel.create — create channels</label>
            <label><input type="checkbox" /> agent.create — create agents</label>
            <p className="dl-mm-manage-label">Lifecycle</p>
            <div className="dl-mm-lifecycle"><button type="button">Suspend</button><button type="button" className="dl-mm-danger">Retire</button></div>
          </aside>
        </div>
      </main>
    </div>
  );
}

export const MEMBER_MANAGEMENT_DEMOS: SurfaceDemo[] = [
  { id: "mm1", label: "MM1 · 列表管理", note: "扁平行 + 行内权限 checkbox + 邀请/创建入口。", Component: MM1Demo },
  { id: "mm2", label: "MM2 · 卡片管理", note: "成员卡片，权限与状态一眼可见。", Component: MM2Demo },
  { id: "mm3", label: "MM3 · 分栏管理", note: "左成员列表 + 右管理面板（权限/生命周期），最接近真实管理后台。", Component: MM3Demo },
];
