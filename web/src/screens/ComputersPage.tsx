import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Link, useLocation, useNavigate, useParams } from "@tanstack/react-router";
import { Check, Copy, Menu, Plus, Trash2, X } from "lucide-react";
import { type FormEvent, useState } from "react";

import {
  createAgent,
  deleteComputer,
  listAgents,
  listComputers,
  type Agent,
  type Computer,
} from "../api/client";
import { PixelIdentity, SpaceShell } from "../components/SpaceShell";
import { DialogFrame } from "../components/DialogFrame";

export function ComputersPage() {
  const { spaceSlug } = useParams({ from: "/s/$spaceSlug/computers" });
  return (
    <SpaceShell spaceSlug={spaceSlug} active="computers">
      {({ space, currentMember, openNavigation }) => (
        <ComputersWorkspace
          spaceId={space.id}
          spaceSlug={space.slug}
          canManage={["owner", "admin"].includes(currentMember.access_level)}
          canCreateAgent={
            ["owner", "admin"].includes(currentMember.access_level) ||
            currentMember.permissions.includes("agent.create")
          }
          isOwner={currentMember.access_level === "owner"}
          openNavigation={openNavigation}
        />
      )}
    </SpaceShell>
  );
}

function ComputersWorkspace({
  spaceId,
  spaceSlug,
  canManage,
  canCreateAgent,
  isOwner,
  openNavigation,
}: {
  spaceId: string;
  spaceSlug: string;
  canManage: boolean;
  canCreateAgent: boolean;
  isOwner: boolean;
  openNavigation: () => void;
}) {
  const queryClient = useQueryClient();
  const navigate = useNavigate();
  const location = useLocation();
  const activeHash = String(location.hash).replace(/^#/, "");
  const agentFormOpen = activeHash === "create-agent";
  const [agentComputerId, setAgentComputerId] = useState<string>();
  const [agentReturnHash, setAgentReturnHash] = useState(() => activeHash === "create-agent" ? "" : activeHash);
  const [deleteTarget, setDeleteTarget] = useState<Computer>();
  const pairFormOpen = canManage && activeHash === "pair-computer";
  const computers = useQuery({
    queryKey: ["computers", spaceId],
    queryFn: () => listComputers(spaceId),
    refetchInterval: 10_000,
  });
  const agents = useQuery({ queryKey: ["agents", spaceId], queryFn: () => listAgents(spaceId) });
  const deletion = useMutation({
    mutationFn: deleteComputer,
    onSuccess: (deleted) => {
      queryClient.setQueryData<Computer[]>(["computers", spaceId], (current) =>
        current?.filter((computer) => computer.id !== deleted.id),
      );
      void queryClient.invalidateQueries({ queryKey: ["agents", spaceId] });
      void queryClient.invalidateQueries({ queryKey: ["members", spaceId] });
      setDeleteTarget(undefined);
      void navigate({ to: "/s/$spaceSlug/computers", params: { spaceSlug }, replace: true });
    },
  });
  const agentCreation = useMutation({
    mutationFn: (input: Parameters<typeof createAgent>[1]) => createAgent(spaceId, input),
    onSuccess: () => {
      closeAgentDialog();
      void queryClient.invalidateQueries({ queryKey: ["members", spaceId] });
      void queryClient.invalidateQueries({ queryKey: ["agents", spaceId] });
    },
  });
  const onlineComputers = computers.data?.filter((computer) => computer.status === "online") ?? [];
  const selectedFromHash = activeHash.startsWith("computer-")
    ? activeHash.slice("computer-".length)
    : undefined;
  const selected = selectedFromHash ? computers.data?.find((computer) => computer.id === selectedFromHash) : undefined;
  const selectedAgents = selected ? agents.data?.filter((agent) => agent.computer_id === selected.id) ?? [] : [];

  function openAgentDialog(computerId?: string) {
    setAgentReturnHash(activeHash === "create-agent" ? "" : activeHash);
    setAgentComputerId(computerId);
    agentCreation.reset();
    void navigate({
      to: "/s/$spaceSlug/computers",
      params: { spaceSlug },
      hash: "create-agent",
    });
  }

  function closeAgentDialog() {
    setAgentComputerId(undefined);
    void navigate({
      to: "/s/$spaceSlug/computers",
      params: { spaceSlug },
      hash: agentReturnHash || undefined,
      replace: true,
    });
  }

  function submitAgent(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    const form = new FormData(event.currentTarget);
    agentCreation.mutate({
      computer_id: String(form.get("computer_id") ?? ""),
      name: String(form.get("name") ?? ""),
      handle: String(form.get("handle") ?? "") || undefined,
      role_text: String(form.get("role_text") ?? ""),
      access_level: String(form.get("access_level") ?? "member") as "member" | "admin",
      driver_kind: String(form.get("driver_kind") ?? "codex") as "codex" | "builtin",
    });
  }

  return (
    <section className="computers-workspace" aria-label="Computers">
      {computers.isPending || agents.isPending ? <div className="detail-skeleton" aria-label="Loading Computers" /> : null}
      {computers.error || agents.error ? (
        <div className="computer-page-state">
          <button className="mobile-menu icon-button" type="button" aria-label="Open navigation" onClick={openNavigation}><Menu /></button>
          <p className="form-error" role="alert">Computers are unavailable. Retry after checking the Server connection.</p>
        </div>
      ) : null}
      {computers.data?.length === 0 ? (
        canManage ? <ComputerOnboarding openNavigation={openNavigation} active={pairFormOpen} /> : <div className="computer-empty"><button className="mobile-menu icon-button" type="button" aria-label="Open navigation" onClick={openNavigation}><Menu /></button><p className="section-kicker">COMPUTE LAYER</p><h2>No Computers paired</h2><p>A Human Owner or Admin must pair a machine before this Space can host Agents.</p></div>
      ) : null}
      {canManage && (!selectedFromHash || pairFormOpen) && computers.data?.length ? <ComputerOnboarding openNavigation={openNavigation} active={pairFormOpen} /> : null}
      {!canManage && !selectedFromHash && computers.data?.length ? <div className="computer-page-state"><button className="mobile-menu icon-button" type="button" aria-label="Open navigation" onClick={openNavigation}><Menu /></button><p>Select a Computer from the navigation.</p></div> : null}
      {selected ? (
        <div className="computer-split">
          <ComputerDetail
            computer={selected}
            agents={selectedAgents}
            canManage={canManage}
            canCreateAgent={canCreateAgent}
            spaceSlug={spaceSlug}
            openNavigation={openNavigation}
            onCreate={() => openAgentDialog(selected.id)}
            onDelete={() => setDeleteTarget(selected)}
          />
        </div>
      ) : null}
      {agentFormOpen ? (
        <AgentDialog
          submit={submitAgent}
          close={closeAgentDialog}
          pending={agentCreation.isPending}
          error={agentCreation.error?.message}
          computers={onlineComputers}
          selectedComputerId={agentComputerId}
          isOwner={isOwner}
        />
      ) : null}
      {deleteTarget ? (
        <DeleteDialog
          computer={deleteTarget}
          agents={agents.data?.filter((agent) => agent.computer_id === deleteTarget.id) ?? []}
          pending={deletion.isPending}
          error={deletion.error?.message}
          close={() => setDeleteTarget(undefined)}
          confirm={() => deletion.mutate(deleteTarget.id)}
        />
      ) : null}
    </section>
  );
}

function ComputerOnboarding({ openNavigation, active }: { openNavigation: () => void; active?: boolean }) {
  const command = `sumi computer --server ${window.location.origin}`;
  const [copied, setCopied] = useState(false);
  const [copyError, setCopyError] = useState(false);
  async function copyCommand() {
    try {
      await navigator.clipboard.writeText(command);
      setCopied(true);
      setCopyError(false);
    } catch {
      setCopyError(true);
    }
  }
  return (
    <section className={active ? "computer-onboarding computer-onboarding--pairing" : "computer-onboarding"} aria-labelledby="computer-onboarding-heading">
      <header className="computer-onboarding-header">
        <button className="mobile-menu icon-button" type="button" aria-label="Open navigation" onClick={openNavigation}><Menu /></button>
        <div className="page-title"><h1 id="computer-onboarding-heading">Pair a Computer</h1><p>Confirm this machine in a Space.</p></div>
      </header>
      <div className="computer-onboarding-body">
        <div className="computer-onboarding-intro">
          <p className="eyebrow">COMPUTE LAYER</p>
          <h2>Bring a machine online</h2>
          <ol className="computer-onboarding-steps">
            <li>
              <div>
                <strong>Run the pairing command</strong>
                <p>Run the command in a terminal on the machine to pair. It creates the Computer Token that identifies this Space.</p>
              </div>
            </li>
            <li>
              <div>
                <strong>Open the URL it prints</strong>
                <p>The daemon prints a pairing URL. Open it in a browser to present this machine's identity.</p>
              </div>
            </li>
            <li>
              <div>
                <strong>Verify the identity, then confirm this Space</strong>
                <p>Check that the hostname and fingerprint match the machine you ran the command on, then confirm the pairing.</p>
              </div>
            </li>
          </ol>
        </div>
        <div className="computer-onboarding-command">
          <div className="pair-command"><code>{command}</code><button className="compact-action" type="button" aria-label="Copy Computer command" onClick={() => void copyCommand()}>{copied ? <Check /> : <Copy />}{copied ? "Copied" : "Copy"}</button></div>
          {copyError ? <p className="form-error" role="alert">Could not copy the command. Select it manually.</p> : null}
          <p className="computer-onboarding-note">A deleted Computer cannot reuse its old identity.</p>
        </div>
      </div>
    </section>
  );
}

function ComputerDetail({
  computer,
  agents,
  canManage,
  canCreateAgent,
  spaceSlug,
  openNavigation,
  onCreate,
  onDelete,
}: {
  computer: Computer;
  agents: Agent[];
  canManage: boolean;
  canCreateAgent: boolean;
  spaceSlug: string;
  openNavigation: () => void;
  onCreate: () => void;
  onDelete: () => void;
}) {
  const busyAgents = agents.filter((agent) => ["queued", "starting", "running", "stopping", "unreachable"].includes(agent.activity_status)).length;
  return (
    <article className="computer-detail">
      <header className="entity-detail-header">
        <button className="mobile-menu icon-button" type="button" aria-label="Open navigation" onClick={openNavigation}><Menu /></button>
        <div className="entity-detail-title"><h1 title={computer.name}>{computer.name}</h1><p>{computer.hostname} · {computer.os === "macos" ? "macOS" : "Linux"}</p></div>
        <Status value={computer.status} />
      </header>
      {computer.status !== "online" ? <p className="inline-notice">Computer is offline. Runtime actions are unavailable until it reconnects.</p> : null}
      <div className="fact-band" aria-label="Computer overview">
        <div><span>Hosted Agents</span><strong>{String(agents.length)}</strong></div>
        <div><span>Busy Agents</span><strong>{String(busyAgents)}</strong></div>
        <div><span>Platform</span><strong>{computer.os === "macos" ? "macOS" : "Linux"}</strong></div>
        <div><span>Daemon</span><strong className="tabular">v{computer.daemon_version}</strong></div>
        <div><span>Last seen</span><strong className="tabular">{computer.last_seen_at ? new Date(computer.last_seen_at).toLocaleString() : "Never connected"}</strong></div>
      </div>
      <section className="detail-panel computer-agents">
        <header className="detail-panel-heading">
          <h3>Agents on this Computer</h3>
          <div className="section-actions">
            {canCreateAgent && computer.status === "online" ? <button className="compact-action" type="button" onClick={onCreate}><Plus />Create</button> : null}
          </div>
        </header>
        {agents.length ? <ul className="hosted-agent-list">{agents.map((agent) => (
          <li key={agent.member_id}><Link to="/s/$spaceSlug/agents/$agentId" params={{ spaceSlug, agentId: agent.member_id }}>
            <PixelIdentity name={agent.name} kind="agent" seed={agent.member_id} />
            <span><strong>{agent.name}</strong><small title={agent.role_text}>{agent.role_text}</small></span>
            <span className={`agent-state agent-state--${agent.activity_status}`} aria-label={`Activity: ${agent.activity_status}`} title={`Activity: ${agent.activity_status}`}><i aria-hidden="true" />{agent.activity_status}</span>
          </Link></li>
        ))}</ul> : <div className="computer-agents-empty"><p>No Agents are hosted on this Computer.</p>{canCreateAgent && computer.status === "online" ? <button className="command-button" type="button" onClick={onCreate}><Plus />Create first Agent</button> : null}</div>}
      </section>
      {canManage ? <div className="computer-actions"><p>Retire every hosted Agent before deleting this Computer.</p><button className="danger-button" type="button" onClick={onDelete}><Trash2 />Delete</button></div> : null}
    </article>
  );
}

function DeleteDialog({ computer, agents, pending, error, close, confirm }: { computer: Computer; agents: Agent[]; pending: boolean; error?: string; close: () => void; confirm: () => void }) {
  return (
    <DialogFrame close={close} labelId="delete-computer-title" className="confirm-dialog">
        <header><h2 id="delete-computer-title">Delete {computer.name}?</h2><button className="icon-button" type="button" aria-label="Close delete confirmation" onClick={close}><X /></button></header>
        <p>The daemon will exit and its Computer Token can never reconnect. A fresh pairing creates a new Computer identity.</p>
        <h3>Assigned Agents ({agents.length})</h3>
        {agents.length ? <><ul>{agents.map((agent) => <li key={agent.member_id}><PixelIdentity name={agent.name} kind="agent" seed={agent.member_id} /><span>{agent.name}</span><Status value={`${agent.desired_lifecycle} · ${agent.provision_status}`} /></li>)}</ul><p className="form-error" role="alert">Retire every assigned Agent before deleting this Computer.</p></> : <p>No assigned Agents. This Computer can be deleted.</p>}
        {error ? <p className="form-error" role="alert">{error}</p> : null}
        <footer><button className="command-button" type="button" onClick={close}>Cancel</button><button className="danger-button" type="button" disabled={pending || agents.length > 0} onClick={confirm}><Trash2 />Delete {computer.name}</button></footer>
    </DialogFrame>
  );
}

function AgentDialog({ submit, close, pending, error, computers, selectedComputerId, isOwner }: { submit: (event: FormEvent<HTMLFormElement>) => void; close: () => void; pending: boolean; error?: string; computers: Computer[]; selectedComputerId?: string; isOwner: boolean }) {
  return (
    <DialogFrame className="agent-dialog" close={close} labelId="create-agent-title">
        <header><div><p className="section-kicker">NEW MEMBER</p><h2 id="create-agent-title">Create Agent</h2></div><button className="icon-button" type="button" aria-label="Close Create Agent" onClick={close}><X /></button></header>
        <form className="agent-create-form" onSubmit={submit}>
          <label className="agent-field agent-field--wide">Computer *<select name="computer_id" required defaultValue={selectedComputerId ?? computers[0]?.id ?? ""} disabled={computers.length === 0}>{computers.map((computer) => <option key={computer.id} value={computer.id}>{computer.name} · {computer.hostname}</option>)}</select></label>
          <label className="agent-field">Name *<input name="name" aria-label="Agent name" required maxLength={40} placeholder="e.g. Iris" data-dialog-initial-focus /></label>
          <label className="agent-field">Handle<input name="handle" pattern="[a-z0-9]+(?:-[a-z0-9]+)*" placeholder="auto-generated" /></label>
          <label className="agent-field agent-field--wide">Role *<textarea name="role_text" aria-label="Role" required maxLength={12000} placeholder="Describe responsibilities and boundaries…" /></label>
          <label className="agent-field">Driver<select name="driver_kind"><option value="codex">Codex</option><option value="builtin">Builtin</option></select></label>
          <label className="agent-field">Access Level<select name="access_level"><option value="member">Member</option>{isOwner ? <option value="admin">Admin</option> : null}</select></label>
          {computers.length === 0 ? <p className="agent-create-prerequisite">Bring a paired Computer online before creating an Agent.</p> : null}
          {error ? <p className="form-error" role="alert">{error}</p> : null}
          <footer><button className="command-button" type="button" onClick={close}>Cancel</button><button className="command-button command-button--accent" type="submit" disabled={pending || computers.length === 0}>{pending ? "Creating…" : "Create Agent"}</button></footer>
        </form>
    </DialogFrame>
  );
}

function Status({ value }: { value: string }) { return <span className={`status status--${value}`} aria-label={`Status: ${value}`} title={`Status: ${value}`}><i aria-hidden="true" />{value}</span>; }
