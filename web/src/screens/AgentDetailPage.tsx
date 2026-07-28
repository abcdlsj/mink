import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Link, useParams } from "@tanstack/react-router";
import { Brain, Eye, Inbox, LayoutDashboard, Menu, MessageCircle, Pause, Play, RotateCcw, Save, Settings2, Trash2, X, type LucideIcon } from "lucide-react";
import { type FormEvent, type ReactNode, useState } from "react";

import { getAgent, readAgentMemory, updateAgent } from "../api/client";
import { activityLabel } from "../agentActivity";
import { PresenceIdentity, SpaceShell } from "../components/SpaceShell";
import { formatBytes } from "../format";

type AgentTab = "overview" | "memory" | "inbox" | "settings";

const agentTabs: { id: AgentTab; label: string; icon: LucideIcon }[] = [
  { id: "overview", label: "Overview", icon: LayoutDashboard },
  { id: "memory", label: "Memory", icon: Brain },
  { id: "inbox", label: "Inbox", icon: Inbox },
  { id: "settings", label: "Settings", icon: Settings2 },
];

export function AgentDetailPage() {
  const { spaceSlug, agentId } = useParams({ from: "/s/$spaceSlug/agents/$agentId" });
  return (
    <SpaceShell spaceSlug={spaceSlug} active="members">
      {({ currentMember, openNavigation }) => (
        <AgentWorkspace
          agentId={agentId}
          spaceSlug={spaceSlug}
          canManage={currentMember.access_level === "owner" || currentMember.access_level === "admin"}
          openNavigation={openNavigation}
        />
      )}
    </SpaceShell>
  );
}

function AgentWorkspace({ agentId, spaceSlug, canManage, openNavigation }: { agentId: string; spaceSlug: string; canManage: boolean; openNavigation: () => void }) {
  const queryClient = useQueryClient();
  const [tab, setTab] = useState<AgentTab>("overview");
  const [cancelNow, setCancelNow] = useState(false);
  const agent = useQuery({ queryKey: ["agent", agentId], queryFn: () => getAgent(agentId) });
  const update = useMutation({
    mutationFn: (input: Parameters<typeof updateAgent>[1]) => updateAgent(agentId, input),
    onSuccess: (updated) => {
      queryClient.setQueryData(["agent", agentId], updated);
      void queryClient.invalidateQueries({ queryKey: ["agents", updated.space_id] });
      void queryClient.invalidateQueries({ queryKey: ["members", updated.space_id] });
    },
  });
  const memory = useMutation({ mutationFn: (path: string) => readAgentMemory(agentId, path) });

  function save(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    const form = new FormData(event.currentTarget);
    const current = agent.data;
    if (!current) return;
    update.mutate({
      role_text: String(form.get("role_text") ?? ""),
      attention_config: {
        ...current.attention_config,
        ambient_enabled: form.get("ambient_enabled") === "on",
        ambient_debounce_seconds: Number(form.get("ambient_debounce_seconds")),
        ambient_max_wait_seconds: Number(form.get("ambient_max_wait_seconds")),
        max_retry_count: Number(form.get("max_retry_count")),
      },
    });
  }

  if (agent.isPending) return <div className="detail-skeleton" aria-label="Loading Agent" />;
  if (agent.error || !agent.data) return <p className="route-status route-status--error">Agent unavailable. Check your permission and retry.</p>;
  const value = agent.data;

  return (
    <section className="agent-workspace" aria-labelledby="agent-heading">
      <header className="entity-detail-header">
        <button className="mobile-menu icon-button" type="button" aria-label="Open navigation" onClick={openNavigation}><Menu /></button>
        <PresenceIdentity name={value.name} kind="agent" seed={value.member_id} activityStatus={value.activity_status} />
        <div className="entity-detail-title">
          <div><h1 id="agent-heading" title={value.name}>{value.name}</h1><span className="agent-label">AGENT</span></div>
          <p>@{value.handle} · {value.role_text}</p>
        </div>
        <span className={`agent-state agent-state--${value.activity_status}`} role="status"><i />{activityLabel(value.activity_status)}</span>
        <Link className="agent-message-action icon-button" to="/s/$spaceSlug/dm/$memberId" params={{ spaceSlug, memberId: value.member_id }} aria-label={`Message ${value.name}`} title={`Message ${value.name}`}><MessageCircle /></Link>
      </header>
      <nav className="detail-tabs" aria-label="Agent detail">
        {agentTabs.map(({ id, label, icon: Icon }) => (
          <button key={id} type="button" aria-current={tab === id ? "page" : undefined} onClick={() => setTab(id)}>
            <Icon aria-hidden="true" />{label}
          </button>
        ))}
      </nav>
      <div className="agent-detail-scroll">
        {value.last_error_code ? <p className="agent-error" role="alert">Agent error: <code>{value.last_error_code}</code></p> : null}
        {tab === "overview" ? (
          <>
            <DetailSection title="Identity"><dl className="detail-grid"><Field label="Display name" value={value.name} /><Field label="Handle" value={`@${value.handle}`} tabular /></dl></DetailSection>
            <DetailSection title="Access"><dl className="detail-grid"><Field label="Access Level" value={capitalize(value.access_level)} /><Field label="Role" value={value.role_text} /></dl></DetailSection>
            <DetailSection title="Runtime"><dl className="detail-grid"><Field label="Computer" value={value.computer_id} tabular /><Field label="Driver" value={capitalize(value.driver_kind)} chip="runtime" /><Field label="Status" value={activityLabel(value.activity_status)} /><Field label="Activity" value={value.activity?.label ?? "No active operation"} /><Field label="Lifecycle" value={capitalize(value.desired_lifecycle)} /><Field label="Provision" value={capitalize(value.provision_status)} /><Field label="Role revision" value={String(value.role_revision)} tabular /></dl></DetailSection>
            <DetailSection title="Created"><dl className="detail-grid"><Field label="Created at" value={new Date(value.created_at).toLocaleString()} tabular /><Field label="Last updated" value={new Date(value.updated_at).toLocaleString()} tabular /></dl></DetailSection>
          </>
        ) : null}
        {tab === "memory" ? (
          <section className="agent-memory" aria-labelledby="memory-heading">
            <div><Brain /><h2 id="memory-heading">Memory files</h2></div>
            <p className="inline-notice">Memory lives only on this Computer. If the Computer is lost, Sumi v1 cannot recover it.</p>
            {!canManage ? <p className="permission-notice" role="status">Permission denied. Only Owner or Admin can inspect Agent Memory metadata.</p> : null}
            {value.memory_files.length === 0 ? <p>No Memory metadata reported.</p> : <ul>{value.memory_files.map((file) => (
              <li key={file.path}><button className="memory-file-button" type="button" aria-label={`Read ${file.path}`} disabled={!canManage || memory.isPending} onClick={() => memory.mutate(file.path)}><Eye /><strong>{file.path}</strong></button><span>{formatBytes(file.size)}</span><time dateTime={file.updated_at}>{new Date(file.updated_at).toLocaleDateString()}</time></li>
            ))}</ul>}
            {memory.data ? <section className="memory-reader" aria-label={`${memory.data.path} contents`}><header><strong>{memory.data.path}</strong><button className="icon-button" type="button" aria-label="Close Memory file" onClick={() => memory.reset()}><X /></button></header><pre>{memory.data.content}</pre></section> : null}
            {memory.error ? <p className="form-error" role="alert">Memory unavailable. The Computer may be offline or you may lack permission.</p> : null}
          </section>
        ) : null}
        {tab === "inbox" ? <DetailSection title="Inbox"><div className="agent-inbox-state"><Inbox aria-hidden="true" /><h2>Attention status</h2><p>Agent Inbox contents stay private. This view exposes only safe runtime status.</p><dl className="detail-grid"><Field label="Lifecycle" value={capitalize(value.desired_lifecycle)} /><Field label="Provision" value={capitalize(value.provision_status)} /><Field label="Last failure" value={value.last_error_code ?? "No recent failure reported"} tabular={Boolean(value.last_error_code)} /></dl></div></DetailSection> : null}
        {tab === "settings" ? (
          <>
            {!canManage ? <p className="permission-notice" role="status">Permission denied. Only Owner or Admin can change Agent settings.</p> : null}
            <form className="agent-settings" onSubmit={save}>
              <label>Role<textarea name="role_text" defaultValue={value.role_text} maxLength={12000} required disabled={!canManage || value.desired_lifecycle === "retired"} /></label>
              <fieldset disabled={!canManage || value.desired_lifecycle === "retired"}><legend>Attention</legend><label className="agent-check"><input name="ambient_enabled" type="checkbox" defaultChecked={value.attention_config.ambient_enabled} /> Ambient Channel attention</label><label>Debounce seconds<input name="ambient_debounce_seconds" type="number" min={1} max={60} defaultValue={value.attention_config.ambient_debounce_seconds} /></label><label>Maximum wait seconds<input name="ambient_max_wait_seconds" type="number" min={5} max={300} defaultValue={value.attention_config.ambient_max_wait_seconds} /></label><label>Maximum retries<input name="max_retry_count" type="number" min={1} max={255} defaultValue={value.attention_config.max_retry_count} /></label></fieldset>
              {canManage && value.desired_lifecycle !== "retired" ? <button className="command-button command-button--accent" type="submit" disabled={update.isPending}><Save /> Save configuration</button> : null}
            </form>
            {canManage && value.desired_lifecycle !== "retired" ? <section className="agent-lifecycle" aria-label="Agent lifecycle"><h2>Lifecycle</h2>{value.desired_lifecycle === "active" ? <label className="agent-check"><input type="checkbox" checked={cancelNow} onChange={(event) => setCancelNow(event.target.checked)} /> Cancel the active run now</label> : null}{value.desired_lifecycle === "active" ? <button className="command-button" type="button" disabled={update.isPending} onClick={() => update.mutate({ lifecycle: { action: "suspend", mode: cancelNow ? "cancel_now" : "stop_after_current" } })}><Pause /> Suspend</button> : null}{value.desired_lifecycle === "suspended" ? <button className="command-button" type="button" disabled={update.isPending} onClick={() => update.mutate({ lifecycle: { action: "resume" } })}><Play /> Resume</button> : null}{value.provision_status === "error" ? <button className="command-button" type="button" disabled={update.isPending} onClick={() => update.mutate({ lifecycle: { action: "retry" } })}><RotateCcw /> Retry provision</button> : null}<button className="danger-button" type="button" disabled={update.isPending} onClick={() => update.mutate({ lifecycle: { action: "retire" } })}><Trash2 /> Retire permanently</button></section> : null}
            {update.error ? <p className="form-error" role="alert">The Agent update failed. Your changes remain on screen; retry when ready.</p> : null}
          </>
        ) : null}
      </div>
    </section>
  );
}

function DetailSection({ title, children }: { title: string; children: ReactNode }) { return <section className="detail-section"><h2>{title}</h2>{children}</section>; }
function Field({ label, value, tabular = false, chip }: { label: string; value: string; tabular?: boolean; chip?: "runtime" | "model" | "reasoning" | "mode" }) {
  const body = chip ? <dd><span className={`chip chip--${chip}`}>{value}</span></dd> : <dd className={tabular ? "tabular" : undefined}>{value}</dd>;
  return <div><dt>{label}</dt>{body}</div>;
}
function capitalize(value: string): string { return value.charAt(0).toUpperCase() + value.slice(1); }
