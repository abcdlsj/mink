import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Link, useParams } from "@tanstack/react-router";
import { Activity, Brain, Eye, LayoutDashboard, Pause, Play, RotateCcw, Save, Settings2, Trash2, X, type LucideIcon } from "lucide-react";
import { type FormEvent, type KeyboardEvent as ReactKeyboardEvent, type ReactNode, useRef, useState } from "react";

import { getAgent, getAgentRuntime, grantMemberPermission, listComputers, listMemberDirectMessages, listMembers, readAgentMemory, retireAgent, revokeMemberPermission, updateAgent, type Channel } from "../api/client";
import { activityLabel, useAgentActivity, type AgentActivityItem } from "../agentActivity";
import { DialogFrame } from "../components/DialogFrame";
import { PresenceIdentity, SpaceShell } from "../components/SpaceShell";
import { formatBytes } from "../format";

type AgentTab = "activity" | "overview" | "memory" | "settings";

const agentTabs: { id: AgentTab; label: string; icon: LucideIcon }[] = [
  { id: "activity", label: "Activity", icon: Activity },
  { id: "overview", label: "Overview", icon: LayoutDashboard },
  { id: "memory", label: "Memory", icon: Brain },
  { id: "settings", label: "Settings", icon: Settings2 },
];

export function AgentDetailPage() {
  const { spaceSlug, agentId } = useParams({ from: "/s/$spaceSlug/agents/$agentId" });
  return (
    <SpaceShell spaceSlug={spaceSlug} active="agents">
      {({ space, currentMember, channels }) => (
        <AgentWorkspace
          agentId={agentId}
          spaceId={space.id}
          spaceSlug={spaceSlug}
          channels={channels}
          canManage={currentMember.access_level === "owner" || currentMember.access_level === "admin"}
        />
      )}
    </SpaceShell>
  );
}

function AgentWorkspace({ agentId, spaceId, spaceSlug, channels, canManage }: { agentId: string; spaceId: string; spaceSlug: string; channels: Channel[]; canManage: boolean }) {
  const queryClient = useQueryClient();
  const [tab, setTab] = useState<AgentTab>("activity");
  const [cancelNow, setCancelNow] = useState(false);
  const [retireConfirmOpen, setRetireConfirmOpen] = useState(false);
  const tabRefs = useRef<Array<HTMLButtonElement | null>>([]);
  const agent = useQuery({ queryKey: ["agent", agentId], queryFn: () => getAgent(agentId) });
  const runtime = useQuery({ queryKey: ["agent-runtime", agentId], queryFn: () => getAgentRuntime(agentId), retry: false, refetchInterval: 2000, refetchIntervalInBackground: true });
  const members = useQuery({ queryKey: ["members", spaceId], queryFn: () => listMembers(spaceId) });
  const agentDirectMessages = useQuery({
    queryKey: ["agent-direct-messages", agentId],
    queryFn: () => listMemberDirectMessages(agentId),
    enabled: canManage,
    retry: false,
  });
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
      setRetireConfirmOpen(false);
    },
  });
  const memory = useMutation({ mutationFn: (path: string) => readAgentMemory(agentId, path) });
  const permission = useMutation({
    mutationFn: ({ action, enabled }: { action: string; enabled: boolean }) => enabled ? grantMemberPermission(agentId, action) : revokeMemberPermission(agentId, action),
    onSuccess: (member) => queryClient.setQueryData(["members", spaceId], (current: Awaited<ReturnType<typeof listMembers>> | undefined) => current?.map((item) => item.id === member.id ? member : item)),
  });
  function save(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    const form = new FormData(event.currentTarget);
    if (!agent.data) return;
    update.mutate({ role_text: String(form.get("role_text") ?? "") });
  }

  function moveTab(event: ReactKeyboardEvent<HTMLButtonElement>, index: number) {
    if (event.key !== "ArrowRight" && event.key !== "ArrowLeft" && event.key !== "Home" && event.key !== "End") return;
    event.preventDefault();
    const nextIndex = event.key === "Home"
      ? 0
      : event.key === "End"
        ? agentTabs.length - 1
        : (index + (event.key === "ArrowRight" ? 1 : -1) + agentTabs.length) % agentTabs.length;
    const nextTab = agentTabs[nextIndex].id;
    setTab(nextTab);
    tabRefs.current[nextIndex]?.focus();
  }

  if (agent.isPending) return <div className="detail-skeleton" role="status" aria-label="Loading Agent" />;
  if (agent.error || !agent.data) return <p className="route-status route-status--error">Agent unavailable. Check your permission and retry.</p>;
  const value = agent.data;
  const agentMember = members.data?.find((member) => member.id === value.member_id);

  return (
    <section className="agent-workspace" aria-labelledby="agent-heading">
      <header className="entity-detail-header">
        <PresenceIdentity name={value.name} kind="agent" seed={value.member_id} activityStatus={value.activity_status} />
        <div className="entity-detail-title">
          <div><h1 id="agent-heading" title={value.name}>{value.name}</h1><span className="agent-label">AGENT</span></div>
          <p>{value.role_text}</p>
        </div>
        <div className="agent-header-signals">
          <span className={`agent-state agent-state--${value.activity_status}`} role="status" aria-label={`Activity: ${activityLabel(value.activity_status)}`} title={`Activity: ${activityLabel(value.activity_status)}`}><i aria-hidden="true" />{activityLabel(value.activity_status)}</span>
          <span className={`agent-connection-signal${value.computer_reachable ? " agent-connection-signal--online" : " agent-connection-signal--offline"}`} aria-label={value.computer_reachable ? "Computer online" : "Computer offline"} title={value.computer_reachable ? "Computer online" : "Computer offline"}>
            <i aria-hidden="true" />{value.computer_reachable ? "Computer online" : "Computer offline"}
          </span>
          {value.last_error_code ? (
            <span className="agent-error-signal" role="status" aria-label={`Agent error: ${value.last_error_code}`} title={`Agent error: ${value.last_error_code}`}>
              <i aria-hidden="true" />error
            </span>
          ) : null}
        </div>
      </header>
      <dl className="agent-fact-strip" aria-label="Agent summary">
        <SummaryFact label="Lifecycle"><StateSignal tone={value.desired_lifecycle} label={humanize(value.desired_lifecycle)} /></SummaryFact>
        <SummaryFact label="Computer">{value.computer_id ? <Link to="/s/$spaceSlug/computers" params={{ spaceSlug }} hash={`computer-${value.computer_id}`}>{computers.data?.find((computer) => computer.id === value.computer_id)?.name ?? "Assigned Computer"}</Link> : "Unassigned"}</SummaryFact>
        <SummaryFact label="Driver"><span className="summary-value--mono">{humanize(value.driver_kind)}</span></SummaryFact>
        <SummaryFact label="Session">{runtime.data ? <StateSignal tone={runtime.data.session_continuity.state} label={humanize(runtime.data.session_continuity.state)} /> : "Checking"}</SummaryFact>
      </dl>
      <nav className="detail-tabs" aria-label="Agent detail" role="tablist">
        {agentTabs.map(({ id, label, icon: Icon }, index) => (
          <button
            key={id}
            ref={(element) => { tabRefs.current[index] = element; }}
            id={`agent-tab-${id}`}
            type="button"
            role="tab"
            aria-selected={tab === id}
            aria-controls={`agent-panel-${id}`}
            tabIndex={tab === id ? 0 : -1}
            onClick={() => setTab(id)}
            onKeyDown={(event) => moveTab(event, index)}
          >
            <Icon aria-hidden="true" />{label}
          </button>
        ))}
      </nav>
      <div className="agent-detail-scroll">
        {!value.computer_id ? <p className="inline-notice">No Computer is assigned. Pair a Computer before starting work.</p> : !value.computer_reachable ? <p className="inline-notice">Computer unreachable. Work already in progress keeps its status until the Computer reports the outcome.</p> : null}
        {tab === "activity" ? (
          <div className="agent-tab-panel agent-activity-panel" id="agent-panel-activity" role="tabpanel" aria-labelledby="agent-tab-activity" tabIndex={0}>
            <DetailSection className="agent-activity" title="Activity">
              <AgentActivityFeed agentId={value.member_id} spaceSlug={spaceSlug} channels={channels} />
            </DetailSection>
          </div>
        ) : null}
        {tab === "overview" ? (
          <div className="agent-tab-panel" id="agent-panel-overview" role="tabpanel" aria-labelledby="agent-tab-overview" tabIndex={0}>
            <div className="agent-overview-grid">
            <DetailSection className="agent-work" title="Current work">
              {runtime.isPending ? <p>Loading current Run…</p> : null}
              {runtime.error ? <p className="inline-notice">Current Run is unavailable. Agent identity and Task facts remain available.</p> : null}
              {runtime.data ? (
                <div className="agent-work-facts">
                  <div><span>Task</span><p>{runtime.data.current_task ? <Link to="/s/$spaceSlug/tasks/$taskId" params={{ spaceSlug, taskId: runtime.data.current_task.id }}>{runtime.data.current_task.title}</Link> : "None"}</p></div>
                  <div><span>Focus</span><p>{runtime.data.focus ? runtime.data.focus.channel_slug ? <Link to="/s/$spaceSlug/channels/$channelSlug" params={{ spaceSlug, channelSlug: runtime.data.focus.channel_slug }} hash={`message-${runtime.data.focus.root_message_id}`}>#{runtime.data.focus.channel_slug}:{runtime.data.focus.root_message_seq}</Link> : <span>DM · message {runtime.data.focus.root_message_seq}</span> : "None"}</p></div>
                  <div><span>Run</span><p>{runtime.data.current_run ? humanize(runtime.data.current_run.status) : "No active Run"}</p></div>
                  <div><span>Computer</span><p><StateSignal tone={value.computer_reachable ? "warm" : "unavailable"} label={value.computer_id ? value.computer_reachable ? "Online" : "Offline" : "Unassigned"} /></p></div>
                  <div><span>Session</span><p>{humanize(runtime.data.session_continuity.state)}</p></div>
                </div>
              ) : null}
              {runtime.data?.another_item_waiting ? <p className="inline-notice" role="status">Another item is waiting. It is not part of the current Focus.</p> : null}
            </DetailSection>
            <DetailSection className="agent-identity" title="Identity"><dl className="detail-grid"><Field label="Access Level">{capitalize(value.access_level)}</Field><Field label="Created" tabular>{new Date(value.created_at).toLocaleDateString()}</Field></dl></DetailSection>
            <DetailSection className="agent-runtime" title="Runtime"><dl className="detail-grid"><Field label="Provision">{capitalize(value.provision_status)}</Field><Field label="Computer">{value.computer_id ? <Link to="/s/$spaceSlug/computers" params={{ spaceSlug }} hash={`computer-${value.computer_id}`}>{computers.data?.find((computer) => computer.id === value.computer_id)?.name ?? value.computer_id}</Link> : undefined}</Field><Field label="Last error">{value.last_error_code ? <code>{value.last_error_code}</code> : "None recorded"}</Field></dl></DetailSection>
            {runtime.data?.diagnostics ? <DetailSection className="agent-runtime-diagnostics" title="Live diagnostics">
              <dl className="detail-grid">
                <Field label="Local Run">{runtime.data.diagnostics.local_run_id ? <code title={runtime.data.diagnostics.local_run_id}>{runtime.data.diagnostics.local_run_state ? `${humanize(runtime.data.diagnostics.local_run_state)} · ${runtime.data.diagnostics.local_run_id.slice(0, 8)}` : runtime.data.diagnostics.local_run_id.slice(0, 8)}</code> : "No local Run"}</Field>
                <Field label="Local queue" tabular>{runtime.data.diagnostics.queued_runs}</Field>
                <Field label="Active local Runs" tabular>{runtime.data.diagnostics.active_runs}</Field>
                <Field label="Pending commands" tabular>{runtime.data.diagnostics.pending_commands}</Field>
                <Field label="Pending results" tabular>{runtime.data.diagnostics.pending_result_events}</Field>
                <Field label="Sessions" tabular>{`${runtime.data.diagnostics.warm_sessions} warm · ${runtime.data.diagnostics.cold_sessions} cold · ${runtime.data.diagnostics.reset_required_sessions} reset`}</Field>
              </dl>
              {runtime.data.current_run && !runtime.data.diagnostics.local_run_id ? <p className="inline-notice inline-notice--error" role="alert">Server Run is active, but the Computer reports no local Run.</p> : null}
              {runtime.data.current_run && runtime.data.diagnostics.local_run_id && runtime.data.diagnostics.local_run_id !== runtime.data.current_run.id ? <p className="inline-notice inline-notice--error" role="alert">Server and Computer report different active Runs.</p> : null}
              {runtime.data.diagnostics.pending_result_events > 0 ? <p className="inline-notice" role="status">The Computer has result events waiting for Server receipts.</p> : null}
            </DetailSection> : runtime.data ? <DetailSection className="agent-runtime-diagnostics" title="Live diagnostics"><p className="inline-notice">Computer-local diagnostics are unavailable.</p></DetailSection> : null}
            {canManage ? <DetailSection className="agent-direct-messages" title="Agent DMs">
              {agentDirectMessages.isPending ? <p>Loading Agent DMs...</p> : null}
              {agentDirectMessages.error ? <p className="inline-notice" role="alert">Agent DMs are unavailable.</p> : null}
              {!agentDirectMessages.isPending && !agentDirectMessages.error && agentDirectMessages.data?.length === 0 ? <p className="agent-direct-messages-empty">No Agent-Agent DMs yet.</p> : null}
              {agentDirectMessages.data?.length ? <ul className="agent-direct-message-list" aria-label={`Agent DMs for ${value.name}`}>
                {agentDirectMessages.data.map((dm) => (
                  <li key={dm.channel_id}>
                    <PresenceIdentity name={dm.other_member.display_name} kind="agent" seed={dm.other_member.id} />
                    <span><Link to="/s/$spaceSlug/agents/$agentId" params={{ spaceSlug, agentId: dm.other_member.id }}>{dm.other_member.display_name}</Link><small>Private Agent DM</small></span>
                    <time dateTime={dm.created_at}>{activityTime(dm.created_at)}</time>
                  </li>
                ))}
              </ul> : null}
            </DetailSection> : null}
            <DetailSection className="agent-permissions" title="Action permissions">
              <div className="permission-list" aria-label="Agent action permissions">
                {[{ action: "channel.create", description: "Create channels in this Space." }, { action: "channel.invite", description: "Invite Members to a Channel." }, { action: "channel.remove", description: "Remove Members from a Channel." }, { action: "agent.create", description: "Create Agents in this Space." }].map(({ action, description }) => {
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
          </div>
        ) : null}
        {tab === "memory" ? (
          <section className="agent-tab-panel agent-memory" id="agent-panel-memory" role="tabpanel" aria-labelledby="agent-tab-memory" tabIndex={0}>
            <div className="agent-memory-heading"><Brain /><h2 id="memory-heading">Memory files</h2></div>
            <p className="inline-notice">Memory lives only on this Computer. If the Computer is lost, Sumi cannot recover it.</p>
            {!canManage ? <p className="permission-notice" role="status">Permission denied. Only Owner or Admin can inspect Agent Memory metadata.</p> : null}
            {value.memory_files.length === 0 ? <div className="memory-empty" role="status"><Brain aria-hidden="true" /><span><strong>No Memory files yet</strong><small>Memory files appear here when the Agent writes them.</small></span></div> : <ul>{value.memory_files.map((file) => (
              <li key={file.path}><button className="memory-file-button" type="button" aria-label={`Read ${file.path}`} disabled={!canManage || memory.isPending} onClick={() => memory.mutate(file.path)}><Eye /><strong>{file.path}</strong></button><span>{formatBytes(file.size)}</span><time dateTime={file.updated_at}>{new Date(file.updated_at).toLocaleDateString()}</time></li>
            ))}</ul>}
            {memory.data ? <section className="memory-reader" aria-label={`${memory.data.path} contents`}><header><strong>{memory.data.path}</strong><button className="icon-button" type="button" aria-label="Close Memory file" onClick={() => memory.reset()}><X /></button></header><pre>{memory.data.content}</pre></section> : null}
            {memory.error ? <p className="form-error" role="alert">Memory unavailable. The Computer may be offline or you may lack permission.</p> : null}
          </section>
        ) : null}
        {tab === "settings" ? (
          <div className="agent-tab-panel" id="agent-panel-settings" role="tabpanel" aria-labelledby="agent-tab-settings" tabIndex={0}>
            {!canManage ? <p className="permission-notice" role="status">Permission denied. Only Owner or Admin can change Agent settings.</p> : null}
            <form className="agent-settings" onSubmit={save}>
              <label>Role<textarea name="role_text" defaultValue={value.role_text} maxLength={12000} required disabled={!canManage || value.desired_lifecycle === "retired"} /></label>
              <div className="agent-settings-policy-heading"><h2>Attention policy</h2><span>Server managed</span></div>
              <dl className="detail-grid"><Field label="Ambient attention">{value.attention_config.ambient_enabled ? "Enabled" : "Disabled"}</Field><Field label="Debounce" tabular>{`${value.attention_config.ambient_debounce_seconds}s`}</Field><Field label="Maximum wait" tabular>{`${value.attention_config.ambient_max_wait_seconds}s`}</Field><Field label="Maximum retries" tabular>{String(value.attention_config.max_retry_count)}</Field></dl>
              {canManage && value.desired_lifecycle !== "retired" ? <button className="command-button command-button--accent" type="submit" disabled={update.isPending}><Save /> Save configuration</button> : null}
            </form>
            {canManage && value.desired_lifecycle !== "retired" ? <section className="agent-lifecycle" aria-label="Agent lifecycle"><h2>Lifecycle</h2>{value.desired_lifecycle === "active" || value.desired_lifecycle === "suspended" ? <button className="command-button" type="button" disabled={update.isPending} onClick={() => update.mutate({ lifecycle: { action: "restart" } })}><RotateCcw aria-hidden="true" /> Restart Agent</button> : null}{value.desired_lifecycle === "active" ? <label className="agent-check"><input type="checkbox" checked={cancelNow} onChange={(event) => setCancelNow(event.target.checked)} /> Cancel the active run now</label> : null}{value.desired_lifecycle === "active" ? <button className="command-button" type="button" disabled={update.isPending} onClick={() => update.mutate({ lifecycle: { action: "suspend", mode: cancelNow ? "cancel_now" : "stop_after_current" } })}><Pause /> Suspend</button> : null}{value.desired_lifecycle === "suspended" ? <button className="command-button" type="button" disabled={update.isPending} onClick={() => update.mutate({ lifecycle: { action: "resume" } })}><Play /> Resume</button> : null}{value.provision_status === "error" ? <button className="command-button" type="button" disabled={update.isPending} onClick={() => update.mutate({ lifecycle: { action: "retry" } })}><RotateCcw /> Retry provision</button> : null}<button className="danger-button" type="button" disabled={update.isPending || retirement.isPending} onClick={() => setRetireConfirmOpen(true)}><Trash2 /> Retire permanently</button></section> : null}
            {update.error ? <p className="form-error" role="alert">The Agent update failed. Your changes remain on screen; retry when ready.</p> : null}
            {retirement.error ? <p className="form-error" role="alert">Unable to retire the Agent. No lifecycle change was applied.</p> : null}
          </div>
        ) : null}
      </div>
      {retireConfirmOpen ? (
        <DialogFrame close={() => setRetireConfirmOpen(false)} labelId="retire-agent-title" className="confirm-dialog agent-retire-dialog">
          <header>
            <div><p className="section-kicker">AGENT LIFECYCLE</p><h2 id="retire-agent-title">Retire {value.name}?</h2></div>
            <button className="icon-button" type="button" aria-label="Close retirement confirmation" title="Close retirement confirmation" onClick={() => setRetireConfirmOpen(false)}><X aria-hidden="true" /></button>
          </header>
          <p>Retirement cannot be undone. {value.name} will stop accepting new work and lose its Computer assignment.</p>
          <p>Historical Messages, Tasks, Runs and Results remain available.</p>
          <footer>
            <button className="command-button" type="button" onClick={() => setRetireConfirmOpen(false)}>Cancel</button>
            <button className="danger-button" type="button" disabled={retirement.isPending} onClick={() => retirement.mutate()}><Trash2 aria-hidden="true" /> Retire {value.name}</button>
          </footer>
        </DialogFrame>
      ) : null}
    </section>
  );
}

function DetailSection({ title, className, children }: { title: string; className?: string; children: ReactNode }) { return <section className={className ? `detail-section ${className}` : "detail-section"}><h2>{title}</h2>{children}</section>; }
function SummaryFact({ label, children }: { label: string; children: ReactNode }) {
  return <div><dt>{label}</dt><dd>{children}</dd></div>;
}
function StateSignal({ tone, label }: { tone: string; label: string }) {
  return <span className={`agent-summary-signal agent-summary-signal--${tone}`} role="status" aria-label={label} title={label}><i aria-hidden="true" />{label}</span>;
}
function Field({ label, children, tabular = false, chip }: { label: string; children?: ReactNode; tabular?: boolean; chip?: "runtime" | "model" | "reasoning" | "mode" }) {
  const body = chip ? <dd><span className={`chip chip--${chip}`}>{children}</span></dd> : <dd className={tabular ? "tabular" : undefined}>{children ?? "Unassigned"}</dd>;
  return <div><dt>{label}</dt>{body}</div>;
}
function capitalize(value: string): string { return value.charAt(0).toUpperCase() + value.slice(1); }
function humanize(value: string): string { return value.split("_").map(capitalize).join(" "); }

const activityLabels: Record<AgentActivityItem["kind"], string> = {
  "message.send": "Sent a message",
  "task.create": "Created task",
  "task.link_thread": "Linked a thread to task",
  "task.unlink_thread": "Unlinked a thread from task",
  "task.submit_review": "Submitted task for review",
  "task.done": "Completed task",
  "task.close": "Closed task",
  "channel.create": "Created channel",
  "channel.leave": "Left channel",
  "channel.invite": "Invited member to channel",
  "channel.remove": "Removed member from channel",
  "agent.create": "Created agent",
  "inbox.ack": "Acknowledged an inbox item",
  "inbox.defer": "Deferred an inbox item",
  "run.yield": "Yielded the current run",
  "run.delivery_rejected": "Recovered a rejected delivery",
};

function AgentActivityFeed({ agentId, spaceSlug, channels }: { agentId: string; spaceSlug: string; channels: Channel[] }) {
  const items = useAgentActivity(agentId);
  const channelById = new Map(channels.map((channel) => [channel.id, channel]));
  if (items.length === 0) {
    return <p className="agent-activity-empty">No visible activity yet.</p>;
  }
  return (
    <ol className="agent-activity-list" aria-label="Agent activity">
      {items.map((item) => {
        const link = activityLink(item, spaceSlug, channelById);
        return (
          <li className="agent-activity-row" key={item.eventId}>
            <div className="agent-activity-main">
              <p>
                <span className="agent-activity-kind">{activityLabels[item.kind]}</span>
                {link ? <span className="agent-activity-link">{link}</span> : null}
              </p>
              {item.arguments.length || item.messagePreview ? (
                <div className="agent-activity-details">
                  {item.arguments.length ? (
                    <ul className="agent-activity-arguments" aria-label="Arguments">
                      {item.arguments.slice(0, 3).map((argument) => (
                        <li key={argument.name}><code>{argument.name}</code><span>=</span><code>{argument.value}</code></li>
                      ))}
                      {item.arguments.length > 3 ? <li className="agent-activity-more">+{item.arguments.length - 3} more</li> : null}
                    </ul>
                  ) : null}
                  {item.messagePreview ? (
                    <pre className="agent-activity-message"><code>{item.messagePreview}</code>{item.messageTruncated ? <span className="agent-activity-truncated">truncated</span> : null}</pre>
                  ) : null}
                </div>
              ) : null}
            </div>
            <time dateTime={item.occurredAt}>{activityTime(item.occurredAt)}</time>
          </li>
        );
      })}
    </ol>
  );
}

function activityLink(item: AgentActivityItem, spaceSlug: string, channelById: Map<string, Channel>): ReactNode {
  if ((item.kind === "message.send" || item.kind === "channel.create" || item.kind === "channel.leave" || item.kind === "channel.invite" || item.kind === "channel.remove") && item.channelId) {
    const channel = channelById.get(item.channelId);
    if (!channel) return null;
    return <Link
      to="/s/$spaceSlug/channels/$channelSlug"
      params={{ spaceSlug, channelSlug: channel.slug }}
      hash={item.kind === "message.send" && item.messageId ? `message-${item.messageId}` : undefined}
    >#{channel.slug}</Link>;
  }
  if (item.kind.startsWith("task.") && item.taskId) {
    return <Link to="/s/$spaceSlug/tasks/$taskId" params={{ spaceSlug, taskId: item.taskId }}>View task</Link>;
  }
  if (item.kind === "agent.create" && item.targetMemberId) {
    return <Link to="/s/$spaceSlug/agents/$agentId" params={{ spaceSlug, agentId: item.targetMemberId }}>View agent</Link>;
  }
  return null;
}

function activityTime(occurredAt: string): string {
  const date = new Date(occurredAt);
  if (Number.isNaN(date.getTime())) return "";
  const time = date.toLocaleTimeString([], { hour: "2-digit", minute: "2-digit" });
  return new Date().toDateString() === date.toDateString()
    ? time
    : `${date.toLocaleDateString()} ${time}`;
}
