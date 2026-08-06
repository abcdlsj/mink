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

export const MEMBER_MANAGEMENT_DEMOS: SurfaceDemo[] = [
  { id: "mm1", label: "MM1 · 列表管理", note: "扁平行 + 轻量消息 icon；产品已采用。", Component: MM1Demo },
];
