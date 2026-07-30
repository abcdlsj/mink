import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Link, useLocation, useNavigate, useParams } from "@tanstack/react-router";
import { Check, Copy, Menu, Monitor, Plus, Trash2, X } from "lucide-react";
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
            currentMember.permissions.includes("agent:create")
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
  const effectiveSelectedId = selectedFromHash ?? computers.data?.[0]?.id;
  const selected = computers.data?.find((computer) => computer.id === effectiveSelectedId);
  const selectedAgents = agents.data?.filter((agent) => agent.computer_id === effectiveSelectedId) ?? [];

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
    <section className="computers-workspace" aria-label="Computer detail">
      {computers.isPending || agents.isPending ? <div className="detail-skeleton" aria-label="Loading Computers" /> : null}
      {computers.error || agents.error ? (
        <div className="computer-page-state">
          <button className="mobile-menu icon-button" type="button" aria-label="Open navigation" onClick={openNavigation}><Menu /></button>
          <p className="form-error" role="alert">Computers are unavailable. Retry after checking the Server connection.</p>
        </div>
      ) : null}
      {computers.data?.length === 0 ? (
        <div className="computer-empty">
          <button className="mobile-menu icon-button" type="button" aria-label="Open navigation" onClick={openNavigation}><Menu /></button>
          <span className="computer-empty-icon"><Monitor aria-hidden="true" /></span>
          <p className="section-kicker">COMPUTE LAYER</p>
          <h2>No Computers paired</h2>
          <p>{canManage ? "Pair a macOS or Linux machine to host Agents in this Space." : "A Human Owner or Admin must pair a machine before this Space can host Agents."}</p>
          {canManage ? <Link className="command-button command-button--accent" to="/s/$spaceSlug/computers" params={{ spaceSlug }} hash="pair-computer"><Plus />Pair Computer</Link> : null}
        </div>
      ) : null}
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
      {pairFormOpen ? (
        <PairComputerDialog
          close={() => void navigate({ to: "/s/$spaceSlug/computers", params: { spaceSlug }, replace: true })}
        />
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
        <span className="computer-icon"><Monitor /></span>
        <div className="entity-detail-title"><h2 title={computer.name}>{computer.name}</h2><p>{computer.hostname}</p></div>
        <Status value={computer.status} />
      </header>
      {computer.status !== "online" ? <p className="inline-notice">Computer is offline. Runtime actions are unavailable until it reconnects.</p> : null}
      <section className="computer-overview" aria-label="Computer overview">
        <OverviewMetric label="Hosted Agents" value={String(agents.length)} />
        <OverviewMetric label="Busy Agents" value={String(busyAgents)} />
        <OverviewMetric label="Platform" value={computer.os === "macos" ? "macOS" : "Linux"} />
        <OverviewMetric label="Daemon" value={`v${computer.daemon_version}`} tabular />
      </section>
      <section className="detail-section">
        <h3>Runtime</h3>
        <dl className="detail-grid">
          <Field label="Operating system" value={computer.os} />
          <Field label="Daemon version" value={`v${computer.daemon_version}`} tabular />
          <Field label="Hostname" value={computer.hostname} tabular />
          <Field label="Last seen" value={computer.last_seen_at ? new Date(computer.last_seen_at).toLocaleString() : "Never connected"} tabular />
          <Field label="Created" value={new Date(computer.created_at).toLocaleString()} tabular />
        </dl>
      </section>
      <section className="detail-section">
        <div className="section-title-row">
          <h3>Agents on this Computer</h3>
          <div className="section-actions">
            <span>{agents.length}</span>
            {canCreateAgent && computer.status === "online" ? <button className="compact-action" type="button" onClick={onCreate}><Plus />Create</button> : null}
          </div>
        </div>
        {agents.length ? <div className="hosted-agent-list">{agents.map((agent) => (
          <Link key={agent.member_id} to="/s/$spaceSlug/agents/$agentId" params={{ spaceSlug, agentId: agent.member_id }}>
            <PixelIdentity name={agent.name} kind="agent" seed={agent.member_id} />
            <span><strong>{agent.name}</strong><small>{agent.driver_kind} Driver</small></span>
            <span className={`agent-state agent-state--${agent.activity_status}`}><i />{agent.activity_status}</span>
          </Link>
        ))}</div> : <p className="section-empty">No Agents are hosted on this Computer.</p>}
      </section>
      {canManage ? <section className="detail-section danger-zone"><div><h3>Delete Computer</h3><p>Disconnect this Computer after every hosted Agent has been retired.</p></div><button className="danger-button" type="button" onClick={onDelete}><Trash2 />Delete</button></section> : null}
    </article>
  );
}

function PairComputerDialog({ close }: { close: () => void }) {
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
    <DialogFrame close={close} labelId="pair-computer-title" className="pair-computer-dialog">
        <header>
          <div><p className="section-kicker">ADD CAPACITY</p><h2 id="pair-computer-title">Pair Computer</h2></div>
          <button className="icon-button" type="button" aria-label="Close Pair Computer" onClick={close}><X /></button>
        </header>
        <div className="pair-computer-body">
          <p>Run this command on the machine that will host Agents.</p>
          <div className="pair-command">
            <code>{command}</code>
            <button className="compact-action" type="button" aria-label="Copy Computer command" onClick={() => void copyCommand()}>
              {copied ? <Check /> : <Copy />}{copied ? "Copied" : "Copy"}
            </button>
          </div>
          {copyError ? <p className="form-error" role="alert">Could not copy the command. Select it manually.</p> : null}
          <ol className="pair-steps"><li>Run the command on the target machine.</li><li>Open the short-lived pairing URL it prints.</li><li>Verify the machine identity, then confirm this Space.</li></ol>
          <p className="pair-computer-note">A deleted Computer cannot reuse its old identity. The daemon clears that identity and exits; run it again to start a fresh pairing.</p>
        </div>
        <footer><button className="command-button command-button--accent" type="button" onClick={close}>Done</button></footer>
    </DialogFrame>
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

function Status({ value }: { value: string }) { return <span className={`status status--${value}`} aria-label={`Status: ${value}`}><i />{value}</span>; }
function Field({ label, value, tabular = false }: { label: string; value: string; tabular?: boolean }) { return <div><dt>{label}</dt><dd className={tabular ? "tabular" : undefined}>{value}</dd></div>; }
function OverviewMetric({ label, value, tabular = false }: { label: string; value: string; tabular?: boolean }) { return <div><span>{label}</span><strong className={tabular ? "tabular" : undefined}>{value}</strong></div>; }
