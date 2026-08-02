import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Link, useNavigate, useParams } from "@tanstack/react-router";
import { Brain, Eye, LayoutDashboard, Menu, MessageCircle, Pause, Play, RotateCcw, Save, Settings2, Trash2, X, type LucideIcon } from "lucide-react";
import { type FormEvent, type ReactNode, useState } from "react";

import { createDirectMessage, getAgent, getAgentRuntime, grantMemberPermission, listComputers, listMembers, readAgentMemory, retireAgent, revokeMemberPermission, updateAgent } from "../api/client";
import { activityLabel } from "../agentActivity";
import { PresenceIdentity, SpaceShell } from "../components/SpaceShell";
import { formatBytes } from "../format";

type AgentTab = "overview" | "memory" | "inbox" | "settings";

const agentTabs: { id: AgentTab; label: string; icon: LucideIcon }[] = [
  { id: "overview", label: "Overview", icon: LayoutDashboard },
  { id: "memory", label: "Memory", icon: Brain },
  { id: "settings", label: "Settings", icon: Settings2 },
];

export function AgentDetailPage() {
  const { spaceSlug, agentId } = useParams({ from: "/s/$spaceSlug/agents/$agentId" });
  return (
    <SpaceShell spaceSlug={spaceSlug} active="members">
      {({ space, currentMember, openNavigation }) => (
        <AgentWorkspace
          agentId={agentId}
          spaceId={space.id}
          spaceSlug={spaceSlug}
          canManage={currentMember.access_level === "owner" || currentMember.access_level === "admin"}
          openNavigation={openNavigation}
        />
      )}
    </SpaceShell>
  );
}

function AgentWorkspace({ agentId, spaceId, spaceSlug, canManage, openNavigation }: { agentId: string; spaceId: string; spaceSlug: string; canManage: boolean; openNavigation: () => void }) {
  const queryClient = useQueryClient();
  const navigate = useNavigate();
  const [tab, setTab] = useState<AgentTab>("overview");
  const [cancelNow, setCancelNow] = useState(false);
  const agent = useQuery({ queryKey: ["agent", agentId], queryFn: () => getAgent(agentId) });
  const runtime = useQuery({ queryKey: ["agent-runtime", agentId], queryFn: () => getAgentRuntime(agentId), retry: false });
  const members = useQuery({ queryKey: ["members", spaceId], queryFn: () => listMembers(spaceId) });
  const computers = useQuery({
    queryKey: ["computers", spaceId],
    queryFn: () => listComputers(spaceId),
    enabled: Boolean(agent.data?.computer_id),
    retry: false,
  });
  const update = useMutation({
    mutationFn: (input: Parameters<typeof updateAgent>[1]) => updateAgent(agentId, input),
    onSuccess: (updated) => {
      queryClient.setQueryData(["agent", agentId], updated);
      void queryClient.invalidateQueries({ queryKey: ["agents", updated.space_id] });
      void queryClient.invalidateQueries({ queryKey: ["members", updated.space_id] });
    },
  });
  const retirement = useMutation({
    mutationFn: () => retireAgent(agentId),
    onSuccess: (retired) => {
      queryClient.setQueryData(["agent", agentId], retired);
      void queryClient.invalidateQueries({ queryKey: ["agents", retired.space_id] });
      void queryClient.invalidateQueries({ queryKey: ["members", retired.space_id] });
    },
  });
  const memory = useMutation({ mutationFn: (path: string) => readAgentMemory(agentId, path) });
  const permission = useMutation({
    mutationFn: ({ action, enabled }: { action: string; enabled: boolean }) => enabled ? grantMemberPermission(agentId, action) : revokeMemberPermission(agentId, action),
    onSuccess: (member) => queryClient.setQueryData(["members", spaceId], (current: Awaited<ReturnType<typeof listMembers>> | undefined) => current?.map((item) => item.id === member.id ? member : item)),
  });
  const directMessage = useMutation({
    mutationFn: (memberId: string) => createDirectMessage(spaceId, memberId),
    onSuccess: (dm) => {
      void queryClient.invalidateQueries({ queryKey: ["direct-messages", spaceId] });
      void navigate({
        to: "/s/$spaceSlug/dm/$memberId",
        params: { spaceSlug, memberId: dm.other_member.id },
      });
    },
  });

  function save(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    const form = new FormData(event.currentTarget);
    if (!agent.data) return;
    update.mutate({ role_text: String(form.get("role_text") ?? "") });
  }

  if (agent.isPending) return <div className="detail-skeleton" aria-label="Loading Agent" />;
  if (agent.error || !agent.data) return <p className="route-status route-status--error">Agent unavailable. Check your permission and retry.</p>;
  const value = agent.data;
  const agentMember = members.data?.find((member) => member.id === value.member_id);

  return (
    <section className="agent-workspace" aria-labelledby="agent-heading">
      <header className="entity-detail-header">
        <button className="mobile-menu icon-button" type="button" aria-label="Open navigation" onClick={openNavigation}><Menu /></button>
        <PresenceIdentity name={value.name} kind="agent" seed={value.member_id} activityStatus={value.activity_status} />
        <div className="entity-detail-title">
          <div><h1 id="agent-heading" title={value.name}>{value.name}</h1><span className="agent-label">AGENT</span></div>
          <p>{value.role_text}</p>
        </div>
        <span className={`agent-state agent-state--${value.activity_status}`} role="status" aria-label={`Activity: ${activityLabel(value.activity_status)}`} title={`Activity: ${activityLabel(value.activity_status)}`}><i aria-hidden="true" />{activityLabel(value.activity_status)}</span>
        <button className="agent-message-action icon-button" type="button" aria-label={`Message ${value.name}`} title={`Message ${value.name}`} disabled={directMessage.isPending} onClick={() => directMessage.mutate(value.member_id)}><MessageCircle /></button>
      </header>
      <nav className="detail-tabs" aria-label="Agent detail">
        {agentTabs.map(({ id, label, icon: Icon }) => (
          <button key={id} type="button" aria-current={tab === id ? "page" : undefined} onClick={() => setTab(id)}>
            <Icon aria-hidden="true" />{label}
          </button>
        ))}
      </nav>
      <div className="agent-detail-scroll">
        {value.last_error_code ? <p className="inline-notice inline-notice--error" role="alert">Agent error: <code>{value.last_error_code}</code></p> : null}
        {tab === "overview" ? (
          <div className="agent-overview-grid">
            <DetailSection className="agent-work" title="Current work">
              {runtime.isPending ? <p>Loading current Run…</p> : null}
              {runtime.error ? <p className="inline-notice">Current Run is unavailable. Agent identity and Task facts remain available.</p> : null}
              {runtime.data ? (
                <div className="agent-work-facts">
                  <div><span>Task</span><p>{runtime.data.current_task ? <Link to="/s/$spaceSlug/tasks/$taskId" params={{ spaceSlug, taskId: runtime.data.current_task.id }}>{runtime.data.current_task.title}</Link> : "None"}</p></div>
                  <div><span>Focus</span><p>{runtime.data.focus ? <Link to="/s/$spaceSlug/channels/$channelSlug" params={{ spaceSlug, channelSlug: runtime.data.focus.channel_slug }} hash={`message-${runtime.data.focus.root_message_id}`}>#{runtime.data.focus.channel_slug} @{runtime.data.focus.root_message_seq}</Link> : "None"}</p></div>
                  <div><span>Run</span><p>{runtime.data.current_run ? runtime.data.current_run.status.replace("_", " ") : "No active Run"}</p></div>
                  <div><span>Session</span><p>{runtime.data.session_continuity.state.replace("_", " ")}</p></div>
                </div>
              ) : null}
              {runtime.data?.another_item_waiting ? <p className="inline-notice" role="status">Another item is waiting. It is not part of the current Focus.</p> : null}
            </DetailSection>
            <DetailSection title="Identity"><dl className="detail-grid"><Field label="Handle">{`@${value.handle}`}</Field><Field label="Access Level">{capitalize(value.access_level)}</Field><Field label="Created" tabular>{new Date(value.created_at).toLocaleDateString()}</Field></dl></DetailSection>
            <DetailSection title="Runtime"><dl className="detail-grid"><Field label="Driver" chip="runtime">{capitalize(value.driver_kind)}</Field><Field label="Computer">{value.computer_id ? <Link to="/s/$spaceSlug/computers" params={{ spaceSlug }} hash={`computer-${value.computer_id}`}>{computers.data?.find((computer) => computer.id === value.computer_id)?.name ?? value.computer_id}</Link> : undefined}</Field><Field label="Lifecycle">{capitalize(value.desired_lifecycle)}</Field><Field label="Provision">{capitalize(value.provision_status)}</Field></dl></DetailSection>
            <DetailSection title="Action permissions">
              <div className="permission-list" aria-label="Agent action permissions">
                {[{ action: "channel.create", description: "Create channels in this Space." }, { action: "agent.create", description: "Create Agents in this Space." }].map(({ action, description }) => {
                  const enabled = agentMember?.permissions.includes(action) ?? false;
                  return (
                    <label key={action} className="permission-row">
                      <input
                        type="checkbox"
                        checked={enabled}
                        disabled={!canManage || permission.isPending || !agentMember}
                        aria-label={`${action} permission`}
                        onChange={(event) => permission.mutate({ action, enabled: event.target.checked })}
                      />
                      <span className="permission-row-copy"><strong>{action}</strong><small>{description}</small></span>
                    </label>
                  );
                })}
              </div>
              {!canManage ? <p className="permission-hint">Only Owner or Admin can change permissions.</p> : null}
              {permission.error ? <p className="form-error" role="alert">Permission update failed.</p> : null}
            </DetailSection>
          </div>
        ) : null}
        {tab === "memory" ? (
          <section className="agent-memory" aria-labelledby="memory-heading">
            <div><Brain /><h2 id="memory-heading">Memory files</h2></div>
            <p className="inline-notice">Memory lives only on this Computer. If the Computer is lost, Sumi v1 cannot recover it.</p>
            {!canManage ? <p className="permission-notice" role="status">Permission denied. Only Owner or Admin can inspect Agent Memory metadata.</p> : null}
            {value.memory_files.length === 0 ? <div className="memory-empty" role="status"><Brain aria-hidden="true" /><span><strong>Memory is ready</strong><small>No Memory files have been written yet.</small></span></div> : <ul>{value.memory_files.map((file) => (
              <li key={file.path}><button className="memory-file-button" type="button" aria-label={`Read ${file.path}`} disabled={!canManage || memory.isPending} onClick={() => memory.mutate(file.path)}><Eye /><strong>{file.path}</strong></button><span>{formatBytes(file.size)}</span><time dateTime={file.updated_at}>{new Date(file.updated_at).toLocaleDateString()}</time></li>
            ))}</ul>}
            {memory.data ? <section className="memory-reader" aria-label={`${memory.data.path} contents`}><header><strong>{memory.data.path}</strong><button className="icon-button" type="button" aria-label="Close Memory file" onClick={() => memory.reset()}><X /></button></header><pre>{memory.data.content}</pre></section> : null}
            {memory.error ? <p className="form-error" role="alert">Memory unavailable. The Computer may be offline or you may lack permission.</p> : null}
          </section>
        ) : null}
        {tab === "settings" ? (
          <>
            {!canManage ? <p className="permission-notice" role="status">Permission denied. Only Owner or Admin can change Agent settings.</p> : null}
            <form className="agent-settings" onSubmit={save}>
              <label>Role<textarea name="role_text" defaultValue={value.role_text} maxLength={12000} required disabled={!canManage || value.desired_lifecycle === "retired"} /></label>
              <p className="section-kicker">ATTENTION POLICY / SERVER MANAGED</p>
              <dl className="detail-grid"><Field label="Ambient attention">{value.attention_config.ambient_enabled ? "Enabled" : "Disabled"}</Field><Field label="Debounce" tabular>{`${value.attention_config.ambient_debounce_seconds}s`}</Field><Field label="Maximum wait" tabular>{`${value.attention_config.ambient_max_wait_seconds}s`}</Field><Field label="Maximum retries" tabular>{String(value.attention_config.max_retry_count)}</Field></dl>
              {canManage && value.desired_lifecycle !== "retired" ? <button className="command-button command-button--accent" type="submit" disabled={update.isPending}><Save /> Save configuration</button> : null}
            </form>
            {canManage && value.desired_lifecycle !== "retired" ? <section className="agent-lifecycle" aria-label="Agent lifecycle"><h2>Lifecycle</h2>{value.desired_lifecycle === "active" ? <label className="agent-check"><input type="checkbox" checked={cancelNow} onChange={(event) => setCancelNow(event.target.checked)} /> Cancel the active run now</label> : null}{value.desired_lifecycle === "active" ? <button className="command-button" type="button" disabled={update.isPending} onClick={() => update.mutate({ lifecycle: { action: "suspend", mode: cancelNow ? "cancel_now" : "stop_after_current" } })}><Pause /> Suspend</button> : null}{value.desired_lifecycle === "suspended" ? <button className="command-button" type="button" disabled={update.isPending} onClick={() => update.mutate({ lifecycle: { action: "resume" } })}><Play /> Resume</button> : null}{value.provision_status === "error" ? <button className="command-button" type="button" disabled={update.isPending} onClick={() => update.mutate({ lifecycle: { action: "retry" } })}><RotateCcw /> Retry provision</button> : null}<button className="danger-button" type="button" disabled={update.isPending || retirement.isPending} onClick={() => retirement.mutate()}><Trash2 /> Retire permanently</button></section> : null}
            {update.error ? <p className="form-error" role="alert">The Agent update failed. Your changes remain on screen; retry when ready.</p> : null}
          </>
        ) : null}
      </div>
    </section>
  );
}

function DetailSection({ title, className, children }: { title: string; className?: string; children: ReactNode }) { return <section className={className ? `detail-section ${className}` : "detail-section"}><h2>{title}</h2>{children}</section>; }
function Field({ label, children, tabular = false, chip }: { label: string; children?: ReactNode; tabular?: boolean; chip?: "runtime" | "model" | "reasoning" | "mode" }) {
  const body = chip ? <dd><span className={`chip chip--${chip}`}>{children}</span></dd> : <dd className={tabular ? "tabular" : undefined}>{children ?? "Unassigned"}</dd>;
  return <div><dt>{label}</dt>{body}</div>;
}
function capitalize(value: string): string { return value.charAt(0).toUpperCase() + value.slice(1); }
