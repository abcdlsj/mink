import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useParams } from "@tanstack/react-router";
import { Menu, Monitor, Plus, Power, Radio, X } from "lucide-react";
import { type FormEvent, useState } from "react";

import { createAgent, listComputers, revokeComputer, type Computer } from "../api/client";
import { SpaceShell } from "../components/SpaceShell";

export function ComputersPage() {
  const { spaceSlug } = useParams({ from: "/s/$spaceSlug/computers" });
  return (
    <SpaceShell spaceSlug={spaceSlug} active="computers">
      {({ space, currentMember, openNavigation }) => (
        <ComputersWorkspace
          spaceId={space.id}
          canManage={currentMember.access_level === "owner" || currentMember.access_level === "admin"}
          isOwner={currentMember.access_level === "owner"}
          openNavigation={openNavigation}
        />
      )}
    </SpaceShell>
  );
}

function ComputersWorkspace({
  spaceId,
  canManage,
  isOwner,
  openNavigation,
}: {
  spaceId: string;
  canManage: boolean;
  isOwner: boolean;
  openNavigation: () => void;
}) {
  const queryClient = useQueryClient();
  const [agentFormOpen, setAgentFormOpen] = useState(false);
  const computers = useQuery({
    queryKey: ["computers", spaceId],
    queryFn: () => listComputers(spaceId),
    refetchInterval: 10_000,
  });
  const revoke = useMutation({
    mutationFn: revokeComputer,
    onSuccess: (updated) => {
      queryClient.setQueryData<Computer[]>(["computers", spaceId], (current) =>
        current?.map((computer) => (computer.id === updated.id ? updated : computer)),
      );
    },
  });
  const agentCreation = useMutation({
    mutationFn: (input: Parameters<typeof createAgent>[1]) => createAgent(spaceId, input),
    onSuccess: () => {
      setAgentFormOpen(false);
      void queryClient.invalidateQueries({ queryKey: ["members", spaceId] });
    },
  });

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
    <section className="computers-workspace" aria-labelledby="computers-heading">
      <header className="members-header">
        <button className="mobile-menu icon-button" type="button" aria-label="Open navigation" onClick={openNavigation}>
          <Menu />
        </button>
        <div className="members-title">
          <p className="section-kicker">LOCAL CAPACITY</p>
          <h1 id="computers-heading">Computers</h1>
        </div>
        <span className="member-count" aria-label={`${computers.data?.length ?? 0} Computers`}>
          {String(computers.data?.length ?? 0).padStart(2, "0")}
        </span>
        {canManage && computers.data?.some((computer) => computer.status === "online") ? (
          <button className="command-button" type="button" onClick={() => setAgentFormOpen((open) => !open)}>
            {agentFormOpen ? <X /> : <Plus />}{agentFormOpen ? "Close" : "Create Agent"}
          </button>
        ) : null}
      </header>
      <div className="computer-list">
        {agentFormOpen ? (
          <form className="agent-create-form" onSubmit={submitAgent}>
            <div><label htmlFor="agent-name">Agent name</label><input id="agent-name" name="name" required maxLength={40} /></div>
            <div><label htmlFor="agent-handle">Handle</label><input id="agent-handle" name="handle" pattern="[a-z0-9]+(?:-[a-z0-9]+)*" placeholder="auto from name" /></div>
            <div><label htmlFor="agent-computer">Computer</label><select id="agent-computer" name="computer_id" required>{computers.data?.filter((computer) => computer.status === "online").map((computer) => <option key={computer.id} value={computer.id}>{computer.name}</option>)}</select></div>
            <div><label htmlFor="agent-driver">Driver</label><select id="agent-driver" name="driver_kind"><option value="codex">Codex</option><option value="builtin">Builtin</option></select></div>
            <div><label htmlFor="agent-access">Access</label><select id="agent-access" name="access_level"><option value="member">Member</option>{isOwner ? <option value="admin">Admin</option> : null}</select></div>
            <div className="agent-role"><label htmlFor="agent-role">Role</label><textarea id="agent-role" name="role_text" required maxLength={12000} /></div>
            <button className="command-button command-button--accent" type="submit" disabled={agentCreation.isPending}>Create Agent</button>
            {agentCreation.error ? <p className="form-error" role="alert">{agentCreation.error.message}</p> : null}
          </form>
        ) : null}
        {computers.isPending ? <p className="members-status">Loading Computers...</p> : null}
        {computers.error ? <p className="form-error">{computers.error.message}</p> : null}
        {computers.data?.length === 0 ? (
          <div className="computer-empty">
            <Monitor aria-hidden="true" />
            <h2>No Computer paired</h2>
            <p>Run <code>sumi computer --server &lt;this-server&gt;</code> on the machine that will host Agents.</p>
          </div>
        ) : null}
        {computers.data?.map((computer) => (
          <article className="computer-row" key={computer.id}>
            <div className={`computer-status computer-status--${computer.status}`}>
              <Radio aria-hidden="true" />
              {computer.status}
            </div>
            <div className="computer-identity">
              <strong>{computer.name}</strong>
              <span>{computer.hostname}</span>
            </div>
            <dl>
              <div><dt>OS</dt><dd>{computer.os}</dd></div>
              <div><dt>Daemon</dt><dd>v{computer.daemon_version}</dd></div>
              <div><dt>Last seen</dt><dd>{computer.last_seen_at ? new Date(computer.last_seen_at).toLocaleString() : "Never"}</dd></div>
            </dl>
            {canManage && computer.status !== "revoked" ? (
              <button className="danger-button" type="button" disabled={revoke.isPending} onClick={() => revoke.mutate(computer.id)}>
                <Power aria-hidden="true" /> Revoke
              </button>
            ) : null}
          </article>
        ))}
      </div>
    </section>
  );
}
