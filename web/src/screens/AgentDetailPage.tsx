import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useParams } from "@tanstack/react-router";
import { Brain, Menu, Pause, Play, Save, Trash2 } from "lucide-react";
import { type FormEvent, useState } from "react";

import { getAgent, updateAgent } from "../api/client";
import { SpaceShell } from "../components/SpaceShell";

export function AgentDetailPage() {
  const { spaceSlug, agentId } = useParams({ from: "/s/$spaceSlug/agents/$agentId" });
  return (
    <SpaceShell spaceSlug={spaceSlug} active="members">
      {({ currentMember, openNavigation }) => (
        <AgentWorkspace
          agentId={agentId}
          canManage={currentMember.access_level === "owner" || currentMember.access_level === "admin"}
          openNavigation={openNavigation}
        />
      )}
    </SpaceShell>
  );
}

function AgentWorkspace({
  agentId,
  canManage,
  openNavigation,
}: {
  agentId: string;
  canManage: boolean;
  openNavigation: () => void;
}) {
  const queryClient = useQueryClient();
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

  if (agent.isPending) return <p className="route-status">Loading Agent...</p>;
  if (agent.error || !agent.data) return <p className="route-status route-status--error">Agent unavailable.</p>;
  const value = agent.data;

  return (
    <section className="agent-workspace" aria-labelledby="agent-heading">
      <header className="members-header">
        <button className="mobile-menu icon-button" type="button" aria-label="Open navigation" onClick={openNavigation}><Menu /></button>
        <div className="members-title"><p className="section-kicker">AGENT MEMBER</p><h1 id="agent-heading">{value.name}</h1></div>
        <span className={`agent-state agent-state--${value.status}`}>{value.status}</span>
      </header>
      <div className="agent-detail-scroll">
        <dl className="agent-overview">
          <div><dt>Handle</dt><dd>@{value.handle}</dd></div>
          <div><dt>Computer</dt><dd>{value.computer_id}</dd></div>
          <div><dt>Driver</dt><dd>{value.driver_kind}</dd></div>
          <div><dt>Role revision</dt><dd>{value.role_revision}</dd></div>
        </dl>
        <form className="agent-settings" onSubmit={save}>
          <label>Role<textarea name="role_text" defaultValue={value.role_text} maxLength={12000} required disabled={!canManage || value.status === "retired"} /></label>
          <fieldset disabled={!canManage || value.status === "retired"}>
            <legend>Attention</legend>
            <label className="agent-check"><input name="ambient_enabled" type="checkbox" defaultChecked={value.attention_config.ambient_enabled} /> Ambient Channel attention</label>
            <label>Debounce seconds<input name="ambient_debounce_seconds" type="number" min={1} max={60} defaultValue={value.attention_config.ambient_debounce_seconds} /></label>
            <label>Maximum wait seconds<input name="ambient_max_wait_seconds" type="number" min={5} max={300} defaultValue={value.attention_config.ambient_max_wait_seconds} /></label>
            <label>Maximum retries<input name="max_retry_count" type="number" min={1} max={255} defaultValue={value.attention_config.max_retry_count} /></label>
          </fieldset>
          {canManage && value.status !== "retired" ? <button className="command-button command-button--accent" type="submit" disabled={update.isPending}><Save /> Save configuration</button> : null}
        </form>
        <section className="agent-memory" aria-labelledby="memory-heading">
          <div><Brain /><h2 id="memory-heading">Memory files</h2></div>
          <p>Memory lives only on this Computer. If the Computer is lost, Sumi v1 cannot recover it.</p>
          {value.memory_files.length === 0 ? <p>No Memory metadata reported.</p> : (
            <ul>{value.memory_files.map((file) => <li key={file.path}><strong>{file.path}</strong><span>{formatBytes(file.size)}</span><code>{file.sha256.slice(0, 12)}</code></li>)}</ul>
          )}
        </section>
        {canManage && value.status !== "retired" ? (
          <section className="agent-lifecycle" aria-label="Agent lifecycle">
            {value.status === "active" ? <label className="agent-check"><input type="checkbox" checked={cancelNow} onChange={(event) => setCancelNow(event.target.checked)} /> Cancel the active run now</label> : null}
            {value.status === "active" ? <button className="command-button" type="button" disabled={update.isPending} onClick={() => update.mutate({ lifecycle: { action: "suspend", mode: cancelNow ? "cancel_now" : "stop_after_current" } })}><Pause /> Suspend</button> : null}
            {value.status === "suspended" ? <button className="command-button" type="button" disabled={update.isPending} onClick={() => update.mutate({ lifecycle: { action: "resume" } })}><Play /> Resume</button> : null}
            <button className="danger-button" type="button" disabled={update.isPending} onClick={() => update.mutate({ lifecycle: { action: "retire" } })}><Trash2 /> Retire permanently</button>
          </section>
        ) : null}
        {update.error ? <p className="form-error" role="alert">{update.error.message}</p> : null}
      </div>
    </section>
  );
}

function formatBytes(size: number) {
  return size < 1024 ? `${size} B` : `${(size / 1024).toFixed(1)} KiB`;
}
