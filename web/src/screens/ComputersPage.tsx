import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Link, useLocation, useParams } from "@tanstack/react-router";
import { Cpu, Menu, Monitor, Plus, Power, ShieldCheck, X } from "lucide-react";
import { type FormEvent, useState } from "react";

import { createAgent, listAgents, listComputers, revokeComputer, type Agent, type Computer } from "../api/client";
import { PixelIdentity, SpaceShell } from "../components/SpaceShell";

export function ComputersPage() {
  const { spaceSlug } = useParams({ from: "/s/$spaceSlug/computers" });
  return <SpaceShell spaceSlug={spaceSlug} active="computers">{({ space, currentMember, openNavigation }) => <ComputersWorkspace spaceId={space.id} spaceSlug={space.slug} canManage={currentMember.access_level === "owner" || currentMember.access_level === "admin"} canCreateAgent={["owner", "admin"].includes(currentMember.access_level) || currentMember.permissions.includes("agent:create")} isOwner={currentMember.access_level === "owner"} openNavigation={openNavigation} />}</SpaceShell>;
}

function ComputersWorkspace({ spaceId, spaceSlug, canManage, canCreateAgent, isOwner, openNavigation }: { spaceId: string; spaceSlug: string; canManage: boolean; canCreateAgent: boolean; isOwner: boolean; openNavigation: () => void }) {
  const queryClient = useQueryClient();
  const location = useLocation();
  const [agentFormOpen, setAgentFormOpen] = useState(() => location.hash.replace(/^#/, "") === "create-agent");
  const [revokeTarget, setRevokeTarget] = useState<Computer>();
  const computers = useQuery({ queryKey: ["computers", spaceId], queryFn: () => listComputers(spaceId), refetchInterval: 10_000 });
  const agents = useQuery({ queryKey: ["agents", spaceId], queryFn: () => listAgents(spaceId) });
  const revoke = useMutation({ mutationFn: revokeComputer, onSuccess: (updated) => { queryClient.setQueryData<Computer[]>(["computers", spaceId], (current) => current?.map((computer) => computer.id === updated.id ? updated : computer)); setRevokeTarget(undefined); } });
  const agentCreation = useMutation({ mutationFn: (input: Parameters<typeof createAgent>[1]) => createAgent(spaceId, input), onSuccess: () => { setAgentFormOpen(false); void queryClient.invalidateQueries({ queryKey: ["members", spaceId] }); void queryClient.invalidateQueries({ queryKey: ["agents", spaceId] }); } });
  const onlineComputers = computers.data?.filter((computer) => computer.status === "online") ?? [];
  const selectedFromHash = location.hash.startsWith("#computer-") ? location.hash.slice("#computer-".length) : undefined;
  const effectiveSelectedId = selectedFromHash ?? computers.data?.[0]?.id;
  const selected = computers.data?.find((computer) => computer.id === effectiveSelectedId);
  const selectedAgents = agents.data?.filter((agent) => agent.computer_id === effectiveSelectedId) ?? [];

  function submitAgent(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    const form = new FormData(event.currentTarget);
    agentCreation.mutate({ computer_id: String(form.get("computer_id") ?? ""), name: String(form.get("name") ?? ""), handle: String(form.get("handle") ?? "") || undefined, role_text: String(form.get("role_text") ?? ""), access_level: String(form.get("access_level") ?? "member") as "member" | "admin", driver_kind: String(form.get("driver_kind") ?? "codex") as "codex" | "builtin" });
  }

  return (
    <section className="computers-workspace" aria-labelledby="computers-heading">
      <header className="members-header"><button className="mobile-menu icon-button" type="button" aria-label="Open navigation" onClick={openNavigation}><Menu /></button><div className="members-title"><p className="section-kicker">LOCAL CAPACITY</p><h1 id="computers-heading">Computers</h1></div><span className="member-count" aria-label={`${computers.data?.length ?? 0} Computers`}>{String(computers.data?.length ?? 0).padStart(2, "0")}</span>{canCreateAgent ? <button className="command-button" type="button" onClick={() => setAgentFormOpen((open) => !open)}>{agentFormOpen ? <X /> : <Plus />}{agentFormOpen ? "Close" : "Create Agent"}</button> : null}</header>
      {agentFormOpen ? <AgentForm submit={submitAgent} pending={agentCreation.isPending} error={agentCreation.error?.message} computers={onlineComputers} isOwner={isOwner} /> : null}
      {computers.isPending || agents.isPending ? <div className="detail-skeleton" aria-label="Loading Computers" /> : null}
      {computers.error || agents.error ? <p className="form-error" role="alert">Computers are unavailable. Retry after checking the Server connection.</p> : null}
      {computers.data?.length === 0 ? <div className="computer-empty"><Monitor aria-hidden="true" /><h2>No Computer paired</h2><p>Run <code>sumi computer --server {window.location.origin}</code> on the machine that will host Agents.</p></div> : null}
      {computers.data?.length ? (
        <div className="computer-split">
          {selected ? <ComputerDetail computer={selected} agents={selectedAgents} canManage={canManage} spaceSlug={spaceSlug} onRevoke={() => setRevokeTarget(selected)} /> : null}
        </div>
      ) : null}
      {revokeTarget ? <RevokeDialog computer={revokeTarget} agents={agents.data?.filter((agent) => agent.computer_id === revokeTarget.id) ?? []} pending={revoke.isPending} error={revoke.error?.message} close={() => setRevokeTarget(undefined)} confirm={() => revoke.mutate(revokeTarget.id)} /> : null}
    </section>
  );
}

function ComputerDetail({ computer, agents, canManage, spaceSlug, onRevoke }: { computer: Computer; agents: Agent[]; canManage: boolean; spaceSlug: string; onRevoke: () => void }) {
  return <article className="computer-detail"><header className="entity-detail-header"><span className="computer-icon"><Monitor /></span><div className="entity-detail-title"><h2 title={computer.name}>{computer.name}</h2><p>{computer.hostname}</p></div><Status value={computer.status} />{canManage && computer.status !== "revoked" ? <button className="danger-button" type="button" onClick={onRevoke}><Power /> Revoke Computer</button> : null}</header>{computer.status !== "online" ? <p className={`inline-notice inline-notice--${computer.status}`}>{computer.status === "revoked" ? "This Computer is revoked. Its credential cannot reconnect and hosted Agents are unavailable." : "Computer is offline. Memory and runtime actions are unavailable until it reconnects."}</p> : null}<section className="detail-section"><h3>Info</h3><dl className="detail-grid"><Field label="Operating system" value={computer.os} /><Field label="Daemon version" value={`v${computer.daemon_version}`} mono /><Field label="Hostname" value={computer.hostname} mono /><Field label="Last seen" value={computer.last_seen_at ? new Date(computer.last_seen_at).toLocaleString() : "Never connected"} mono /><Field label="Created" value={new Date(computer.created_at).toLocaleString()} mono /></dl></section><section className="detail-section"><h3>Capabilities</h3><div className="capability-row"><span><Cpu />{computer.os === "linux" ? "bubblewrap" : "sandbox-exec"}</span><span><ShieldCheck />Daemon reported</span></div><p className="section-note">Only capabilities proven by the current daemon status are shown. Driver availability is reported per hosted Agent.</p></section><section className="detail-section"><div className="section-title-row"><h3>Agents on this Computer</h3><span>{agents.length}</span></div>{agents.length ? <div className="hosted-agent-list">{agents.map((agent) => <Link key={agent.member_id} to="/s/$spaceSlug/agents/$agentId" params={{ spaceSlug, agentId: agent.member_id }}><PixelIdentity name={agent.name} /><span><strong>{agent.name}</strong><small>{agent.driver_kind} Driver</small></span><span className={`agent-state agent-state--${agent.status}`}><i />{agent.status}</span></Link>)}</div> : <p className="section-empty">No Agents are hosted on this Computer.</p>}</section></article>;
}

function RevokeDialog({ computer, agents, pending, error, close, confirm }: { computer: Computer; agents: Agent[]; pending: boolean; error?: string; close: () => void; confirm: () => void }) {
  const active = agents.filter((agent) => ["active", "provisioning"].includes(agent.status));
  return <div className="dialog-backdrop" role="presentation"><section className="confirm-dialog" role="dialog" aria-modal="true" aria-labelledby="revoke-title"><header><h2 id="revoke-title">Revoke {computer.name}?</h2><button className="icon-button" type="button" aria-label="Close revoke confirmation" onClick={close}><X /></button></header><p>The old Computer credential will stop working immediately. Sumi v1 cannot migrate Agent Homes.</p><h3>Affected Agents ({agents.length})</h3>{agents.length ? <ul>{agents.map((agent) => <li key={agent.member_id}><PixelIdentity name={agent.name} /><span>{agent.name}</span><Status value={agent.status} /></li>)}</ul> : <p>No hosted Agents.</p>}{active.length ? <p className="form-error" role="alert">Pause or retire {active.length} active {active.length === 1 ? "Agent" : "Agents"} before revoking this Computer.</p> : null}{error ? <p className="form-error" role="alert">{error}</p> : null}<footer><button className="command-button" type="button" onClick={close}>Cancel</button><button className="danger-button" type="button" disabled={pending || active.length > 0} onClick={confirm}><Power /> Revoke {computer.name}</button></footer></section></div>;
}

function AgentForm({ submit, pending, error, computers, isOwner }: { submit: (event: FormEvent<HTMLFormElement>) => void; pending: boolean; error?: string; computers: Computer[]; isOwner: boolean }) { return <form className="agent-create-form" onSubmit={submit}><div><label htmlFor="agent-name">Agent name</label><input id="agent-name" name="name" required maxLength={40} /></div><div><label htmlFor="agent-handle">Handle</label><input id="agent-handle" name="handle" pattern="[a-z0-9]+(?:-[a-z0-9]+)*" placeholder="auto from name" /></div><div><label htmlFor="agent-computer">Computer</label><select id="agent-computer" name="computer_id" required disabled={computers.length === 0}>{computers.map((computer) => <option key={computer.id} value={computer.id}>{computer.name}</option>)}</select></div><div><label htmlFor="agent-driver">Driver</label><select id="agent-driver" name="driver_kind"><option value="codex">Codex</option><option value="builtin">Builtin</option></select></div><div><label htmlFor="agent-access">Access Level</label><select id="agent-access" name="access_level"><option value="member">Member</option>{isOwner ? <option value="admin">Admin</option> : null}</select></div><div className="agent-role"><label htmlFor="agent-role">Role</label><textarea id="agent-role" name="role_text" required maxLength={12000} /></div><button className="command-button command-button--accent" type="submit" disabled={pending || computers.length === 0}>Create Agent</button>{computers.length === 0 ? <p className="agent-create-prerequisite">Bring a paired Computer online before creating an Agent.</p> : null}{error ? <p className="form-error" role="alert">{error}</p> : null}</form>; }
function Status({ value }: { value: string }) { return <span className={`status status--${value}`} aria-label={`Status: ${value}`}><i />{value}</span>; }
function Field({ label, value, mono = false }: { label: string; value: string; mono?: boolean }) { return <div><dt>{label}</dt><dd className={mono ? "mono" : undefined}>{value}</dd></div>; }
