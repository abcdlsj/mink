import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Link, useNavigate, useParams } from "@tanstack/react-router";
import {
  Check,
  Copy,
  LoaderCircle,
  MailPlus,
  MessageCircle,
  Plus,
  ShieldCheck,
  UserPlus,
  Users,
  X,
} from "lucide-react";
import { type FormEvent, useState } from "react";

import {
  createInvitation,
  createDirectMessage,
  listAgents,
  listMembers,
  updateMember,
  type Agent,
  type Member,
  type Space,
} from "../api/client";
import { activityLabel } from "../agentActivity";
import { PresenceIdentity, SpaceShell } from "../components/SpaceShell";

export function MembersPage() {
  const { spaceSlug } = useParams({ from: "/s/$spaceSlug/members" });

  return (
    <SpaceShell spaceSlug={spaceSlug} active="members">
      {({ space }) => (
        <MembersWorkspace space={space} directory="members" />
      )}
    </SpaceShell>
  );
}

export function AgentsPage() {
  const { spaceSlug } = useParams({ from: "/s/$spaceSlug/agents" });

  return (
    <SpaceShell spaceSlug={spaceSlug} active="agents">
      {({ space }) => (
        <MembersWorkspace space={space} directory="agents" />
      )}
    </SpaceShell>
  );
}

function MembersWorkspace({ space, directory }: { space: Space; directory: "members" | "agents" }) {
  const queryClient = useQueryClient();
  const navigate = useNavigate();
  const agentsPage = directory === "agents";
  const [inviteOpen, setInviteOpen] = useState(false);
  const [inviteLink, setInviteLink] = useState<string>();
  const [copied, setCopied] = useState(false);
  const [kindFilter, setKindFilter] = useState<"all" | "human" | "agent">(agentsPage ? "agent" : "all");
  const members = useQuery({
    queryKey: ["members", space.id],
    queryFn: () => listMembers(space.id),
  });
  const agents = useQuery({
    queryKey: ["agents", space.id],
    queryFn: () => listAgents(space.id),
  });
  const activityByMemberId = new Map(
    (agents.data ?? []).map((agent) => [agent.member_id, agent.activity_status] as const),
  );
  const roleByMemberId = new Map(
    (agents.data ?? []).map((agent) => [agent.member_id, agent.role_text] as const),
  );
  const agentByMemberId = new Map(
    (agents.data ?? []).map((agent) => [agent.member_id, agent] as const),
  );
  const currentMember = members.data?.find((member) => member.id === space.current_member_id);
  const canInvite = currentMember?.access_level === "owner" || currentMember?.access_level === "admin";
  const canCreateAgent = canInvite || currentMember?.permissions.includes("agent.create");
  const invitation = useMutation({
    mutationFn: (email: string) => createInvitation(space.id, { email }),
    onSuccess: (created) => {
      setInviteLink(
        created.token ? `${window.location.origin}/invite/${created.token}` : undefined,
      );
      setCopied(false);
    },
  });
  const memberUpdate = useMutation({
    mutationFn: ({ memberId, input }: { memberId: string; input: Parameters<typeof updateMember>[2] }) =>
      updateMember(space.id, memberId, input),
    onSuccess: (updated) => {
      queryClient.setQueryData<Member[]>(["members", space.id], (current) =>
        current?.map((member) => (member.id === updated.id ? updated : member)),
      );
    },
  });
  const directMessage = useMutation({
    mutationFn: (memberId: string) => createDirectMessage(space.id, memberId),
    onSuccess: (dm) => {
      void queryClient.invalidateQueries({ queryKey: ["direct-messages", space.id] });
      void navigate({
        to: "/s/$spaceSlug/dm/$memberId",
        params: { spaceSlug: space.slug, memberId: dm.other_member.id },
      });
    },
  });
  const visibleMembers = members.data?.filter((member) => {
    if (agentsPage && member.kind !== "agent") return false;
    return kindFilter === "all" || member.kind === kindFilter;
  }) ?? [];
  const memberGroups = (['agent', 'human'] as const)
    .map((kind) => ({ kind, members: visibleMembers.filter((member) => member.kind === kind) }))
    .filter((group) => group.members.length > 0);

  function submitInvitation(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    const form = new FormData(event.currentTarget);
    invitation.mutate(String(form.get("email") ?? ""));
  }

  async function copyInviteLink() {
    if (!inviteLink) return;
    await navigator.clipboard.writeText(inviteLink);
    setCopied(true);
  }

  return (
    <section className={`members-workspace${agentsPage ? " members-workspace--agents" : ""}`} aria-labelledby="members-heading">
      <header className="members-header">
        <div className="members-title">
          <h1 id="members-heading">{agentsPage ? "Agents" : "Members"}</h1>
          <p>{agentsPage ? "Persistent collaborators with a Computer and a Role." : "People and Agents with access to this Space."}</p>
        </div>
        {canCreateAgent ? (
          <Link
            className="command-button command-button--accent compact-header-action"
            to="/s/$spaceSlug/computers"
            params={{ spaceSlug: space.slug }}
            hash="create-agent"
            aria-label="Create Agent"
            title="Create Agent"
          >
            <Plus aria-hidden="true" />
            <span>Create agent</span>
          </Link>
        ) : null}
        {canInvite && !agentsPage ? (
          <button
            className="command-button compact-header-action"
            type="button"
            aria-label={inviteOpen ? "Close invitation form" : "Invite Human"}
            title={inviteOpen ? "Close invitation form" : "Invite Human"}
            onClick={() => {
              setInviteOpen((open) => !open);
              setInviteLink(undefined);
              invitation.reset();
            }}
          >
            {inviteOpen ? <X aria-hidden="true" /> : <UserPlus aria-hidden="true" />}
            <span>{inviteOpen ? "Close" : "Invite"}</span>
          </button>
        ) : null}
      </header>

      {agentsPage ? (
        <dl className="agent-directory-summary" aria-label="Agent directory summary">
          <DirectoryMetric label="Agents" value={agents.data?.length} loading={agents.isPending} />
          <DirectoryMetric label="Working" value={agents.data ? agents.data.filter((agent) => ["dispatched", "working"].includes(agent.activity_status)).length : undefined} loading={agents.isPending} />
          <DirectoryMetric label="Computers online" value={agents.data ? new Set(agents.data.filter((agent) => agent.computer_reachable && agent.computer_id).map((agent) => agent.computer_id)).size : undefined} loading={agents.isPending} />
          <DirectoryMetric label="Needs attention" value={agents.data ? agents.data.filter((agent) => agent.last_error_code || agent.provision_status === "error").length : undefined} loading={agents.isPending} />
        </dl>
      ) : (
        <div className="member-filter" role="group" aria-label="Filter Members by kind">
          {(["all", "human", "agent"] as const).map((kind) => (
            <button
              key={kind}
              type="button"
              className={kindFilter === kind ? "member-filter--active" : undefined}
              aria-pressed={kindFilter === kind}
              onClick={() => setKindFilter(kind)}
            >
              {kind === "all" ? "All" : kind === "human" ? "Human" : "Agent"}
              <span>
                {kind === "all"
                  ? members.data?.length ?? 0
                  : members.data?.filter((member) => member.kind === kind).length ?? 0}
              </span>
            </button>
          ))}
        </div>
      )}

      {agentsPage && agents.error ? (
        <div className="directory-status directory-status--error" role="alert">
          <span>Unable to load Agent status.</span>
          <button className="quiet-button" type="button" onClick={() => void agents.refetch()} disabled={agents.isFetching}>
            {agents.isFetching ? "Retrying…" : "Retry"}
          </button>
        </div>
      ) : null}

      {inviteOpen ? (
        <section className="invite-band" aria-labelledby="invite-heading">
          <div className="invite-band-title">
            <MailPlus aria-hidden="true" />
            <div>
              <h2 id="invite-heading">Invite a Human</h2>
              <p>Bound to one email and valid for seven days.</p>
            </div>
          </div>
          {inviteLink ? (
            <div className="invite-result">
              <label htmlFor="invite-link">Invitation link</label>
              <div className="copy-field">
                <input id="invite-link" value={inviteLink} readOnly />
                <button
                  className="icon-button"
                  type="button"
                  aria-label="Copy invitation link"
                  title="Copy invitation link"
                  onClick={() => void copyInviteLink()}
                >
                  {copied ? <Check /> : <Copy />}
                </button>
              </div>
            </div>
          ) : (
            <form className="invite-form" onSubmit={submitInvitation}>
              <label htmlFor="invite-email">Email</label>
              <input id="invite-email" name="email" type="email" autoComplete="email" required />
              <button className="command-button command-button--accent" type="submit" disabled={invitation.isPending}>
                {invitation.isPending ? <LoaderCircle className="spin" /> : <UserPlus />}
                Create invitation
              </button>
            </form>
          )}
          {invitation.error ? (
            <p className="form-error invite-error" role="alert">
              {invitation.error.message}
            </p>
          ) : null}
        </section>
      ) : null}

      <div className="member-list" aria-live="polite">
        {members.isPending ? <div className="members-status">Loading Members...</div> : null}
        {members.error ? (
          <div className="members-status members-status--error">{members.error.message}</div>
        ) : null}
        {memberGroups.map((group) => <section className="member-group" key={group.kind} aria-labelledby={`member-group-${group.kind}`}>
          <header className="member-group-heading">
            <h2 id={`member-group-${group.kind}`}>{group.kind === "agent" ? "Agents" : "Humans"}</h2>
            <span>{group.members.length} {group.kind === "agent" ? "Agents" : "Humans"}</span>
          </header>
          {group.members.map((member) => {
          const currentAccess = currentMember?.access_level;
          const ownerCanSetAccess = currentAccess === "owner" && member.access_level !== "owner";
          return (
            <article className="member-row" key={member.id}>
              <div className="member-identity">
                <PresenceIdentity name={member.display_name} kind={member.kind} seed={member.id} activityStatus={activityByMemberId.get(member.id)} />
                  <div>
                    <strong title={member.display_name}>{member.display_name}</strong>
                  <span title={member.kind === "agent" ? roleByMemberId.get(member.id) ?? undefined : undefined}>{member.kind === "agent" ? roleByMemberId.get(member.id) ?? "Agent" : "Space member"}</span>
                  </div>
                <span className={`kind-label kind-label--${member.kind}`}>{member.kind === "agent" ? "Agent" : "Human"}</span>
                {member.kind === "agent" ? (
                  <Link
                    className="agent-detail-link"
                    to="/s/$spaceSlug/agents/$agentId"
                    params={{ spaceSlug: space.slug, agentId: member.id }}
                  >
                    Manage
                  </Link>
                ) : null}
              </div>
              <div className="member-presence">
                {member.kind === "agent" ? <AgentPresence agent={agentByMemberId.get(member.id)} /> : <span className="member-presence-copy"><small className="member-field-label">Presence</small><strong>Human member</strong></span>}
              </div>
              <div className="access-control">
                <span className="member-field-label">Access</span>
                <div className="access-value">
                  <ShieldCheck aria-hidden="true" />
                  {ownerCanSetAccess ? (
                    <select
                      aria-label={`Access level for ${member.display_name}`}
                      value={member.access_level}
                      disabled={memberUpdate.isPending}
                      onChange={(event) =>
                        memberUpdate.mutate({
                          memberId: member.id,
                          input: { access_level: event.target.value as "member" | "admin" },
                        })
                      }
                    >
                      <option value="member">Member</option>
                      <option value="admin">Admin</option>
                    </select>
                  ) : (
                    <strong>{capitalize(member.access_level)}</strong>
                  )}
                </div>
              </div>
              <div className="member-actions">
                {member.id !== currentMember?.id ? (
                  <button
                    className="member-message-button"
                    type="button"
                    disabled={directMessage.isPending}
                    onClick={() => directMessage.mutate(member.id)}
                  >
                    <MessageCircle aria-hidden="true" />
                    Message
                  </button>
                ) : null}
              </div>
            </article>
          );
          })}
        </section>)}
      </div>
      {memberUpdate.error ? (
        <p className="member-update-error form-error" role="alert">
          {memberUpdate.error.message}
        </p>
      ) : null}
      {!members.isPending && visibleMembers.length === 0 ? (
        <div className="empty-members">
          <Users aria-hidden="true" />
          <div>
            <strong>{agentsPage ? "No Agents yet" : members.data?.length ? "No matching members" : "No members yet"}</strong>
            <p>{agentsPage ? "Create an Agent to give this Space a persistent collaborator." : "Invite a Human or create an Agent to start collaborating."}</p>
            {agentsPage && canCreateAgent ? (
              <Link className="command-button command-button--accent" to="/s/$spaceSlug/computers" params={{ spaceSlug: space.slug }} hash="create-agent">
                <Plus aria-hidden="true" /> Create agent
              </Link>
            ) : null}
          </div>
        </div>
      ) : null}
    </section>
  );
}

function AgentPresence({ agent }: { agent?: Agent }) {
  if (!agent) return <span className="member-presence-copy"><small className="member-field-label">Activity</small><strong>Status unavailable</strong></span>;
  const activity = activityLabel(agent.activity_status);
  const connection = agent.computer_reachable ? "Computer online" : "Computer offline";
  return (
    <span className="agent-presence-stack" aria-label={`${activity}; ${agent.computer_id ? connection : "No Computer assigned"}`}>
      <span className={`agent-presence-signal agent-presence-signal--${agent.activity_status}`} aria-hidden="true" />
      <span>
        <small className="member-field-label">Activity</small>
        <strong>{activity}</strong>
        <small>{agent.computer_id ? connection : "No Computer assigned"}</small>
      </span>
    </span>
  );
}

function DirectoryMetric({ label, value, loading }: { label: string; value?: number; loading: boolean }) {
  return <div><dt>{label}</dt><dd aria-label={loading ? `Loading ${label}` : `${value ?? 0} ${label}`}>{loading ? "—" : value}</dd></div>;
}

function capitalize(value: string): string {
  return value.charAt(0).toUpperCase() + value.slice(1);
}
