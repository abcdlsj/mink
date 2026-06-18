import { useEffect, useState } from "react";
import { ChevronRight, Square } from "lucide-react";
import { useStore } from "@/lib/store";
import { Button } from "@/components/ui/button";
import { Dot } from "./LeftPane";
import { api } from "@/lib/api";
import { cn, relTime } from "@/lib/utils";
import { MemoryOverviewCard } from "./MemoryOverviewCard";
import type {
  ActionProposalCard,
  AgentItem,
  AgentRun,
  CapabilityView,
  ContextInspectView,
  SkillView,
  TaskStateCard,
  ThreadItem,
} from "@/lib/types";

export function RightPane() {
  const view = useStore((s) => s.view);
  const state = useStore((s) => s.state);
  const detail = useStore((s) => s.detail);
  const threadDetail = useStore((s) => s.threadDetail);
  const channels = useStore((s) => s.channels);
  const threads = useStore((s) => s.threads);
  const agents = useStore((s) => s.agents);
  const personas = useStore((s) => s.personas);
  const agentDMs = useStore((s) => s.agentDMs);
  const activeChannel = useStore((s) => s.activeChannel);
  const activeAgentSpace = useStore((s) => s.activeAgentSpace);
  const participants = useStore((s) => s.participants);
  const tools = useStore((s) => s.tools);
  const streamingByID = useStore((s) => s.streamingByID);
  const capabilities = useStore((s) => s.capabilities);
  const [moreOpen, setMoreOpen] = useState(true);

  const inThread = !!threadDetail && !threadDetail.unsupported && !threadDetail.not_found;
  const threadParticipants = inThread ? threadDetail!.participants : null;
  const threadRecentRuns = inThread ? threadDetail!.recent_runs : null;
  const workbenchView = inThread ? "thread" : view;

  if (!detail && !inThread) return <aside className="h-full border-l-hard border-border bg-panel-3 px-3 py-4" />;

  const liveRuns: AgentRun[] = Object.values(streamingByID).map((s) => ({
    id: s.messageID,
    agent_id: s.agentID || "agent",
    title: "Working now",
    status: "running",
    lifecycle: "active",
    time: s.startedAt,
  }));
  const runtimeRuns: AgentRun[] = liveRuns.length > 0 ? liveRuns : participants?.active_runs || [];
  const taskRuns: AgentRun[] = participants?.active_runs || [];
  const activePersona = personaForRuntime(activeAgentSpace, detail?.item?.persona_id, personas, agentDMs);
  const activeChannelItem = view === "channel" ? channels.find((c) => c.id === activeChannel) : undefined;
  const scopeRecentRuns = inThread
    ? (threadRecentRuns || [])
    : (participants?.recent_runs || []);
  const activeScopeRuns = activeRuns(scopeRecentRuns.length > 0 ? scopeRecentRuns : taskRuns);
  const archivedScopeRuns = inThread
    ? (threadDetail?.archived_runs_count || 0)
    : (participants?.archived_runs_count || 0);
  const scopeSpaceID = inThread ? threadDetail?.space_id : detail?.item.id;
  const scopeAgentIDs = activePersona
    ? [activePersona.id]
    : (inThread ? (threadParticipants || []) : (participants?.agents || [])).map((a) => a.id);
  const contextAgentID = activePersona?.id || scopeAgentIDs[0] || "";
  const agentModes = inThread
    ? threadDetail?.agent_modes
    : view === "channel"
      ? activeChannelItem?.agent_modes
      : undefined;
  const runtimeContextSec = (
    <RuntimeContextSection
      spaceID={scopeSpaceID}
      parentMessageID={inThread ? threadDetail?.parent_id : undefined}
      agentID={contextAgentID}
    />
  );
  const memorySec = (
    <Section label="Memory">
      <MemoryOverviewCard
        personaID={activePersona?.id}
        spaceID={scopeSpaceID}
      />
    </Section>
  );

  let main: React.ReactNode = null;
  let more: React.ReactNode = null;

  const workbenchSec = (
    <AgentWorkbenchPanel
      view={workbenchView}
      stateRuntime={state?.runtime}
      activePersona={activePersona}
      runs={runtimeRuns}
      participants={inThread ? (threadParticipants || []) : (participants?.agents || [])}
      personas={personas}
      agentDMs={agentDMs}
      agents={agents}
      tools={tools}
      capabilities={capabilities}
      recentRuns={scopeRecentRuns}
      agentModes={agentModes}
    />
  );

  const activeTasksSec = (
    <ActiveTasksSection runs={activeScopeRuns} archived={archivedScopeRuns} />
  );

  if (view === "channel" && !inThread) {
    const ch = activeChannelItem;
    const channelThreads = threads.filter((t) => t.channel_id === activeChannel).slice(0, 3);
    main = (
      <>
        {ch?.topic && (
          <Section label="Topic">
            <div className="text-[13px] text-text">{ch.topic}</div>
          </Section>
        )}
        {workbenchSec}
        {activeTasksSec}
        {channelThreads.length > 0 && (
          <Section label="Recent Threads">
            {channelThreads.map((t) => (
              <ThreadMiniCard key={t.id} thread={t} showChannel={false} />
            ))}
          </Section>
        )}
      </>
    );
    more = (
      <>
        {runtimeContextSec}
        {memorySec}
        <CapabilitiesSection capabilities={capabilities} scopeSpaceID={scopeSpaceID} scopeAgentIDs={scopeAgentIDs} />
      </>
    );
  } else if (view === "direct" || inThread) {
    main = (
      <>
        {workbenchSec}
        {activeTasksSec}
      </>
    );
    more = (
      <>
        {runtimeContextSec}
        {memorySec}
        <CapabilitiesSection capabilities={capabilities} scopeSpaceID={scopeSpaceID} scopeAgentIDs={scopeAgentIDs} />
      </>
    );
  } else if (view === "agent") {
    main = (
      <>
        {workbenchSec}
        {activeTasksSec}
        {threads.length > 0 && (
          <Section label="Recent Threads">
            {threads.slice(0, 3).map((t) => (
              <ThreadMiniCard key={t.id} thread={t} showChannel={true} />
            ))}
          </Section>
        )}
      </>
    );
    more = (
      <>
        {runtimeContextSec}
        {memorySec}
        <CapabilitiesSection capabilities={capabilities} scopeSpaceID={scopeSpaceID} scopeAgentIDs={scopeAgentIDs} />
      </>
    );
  } else {
    main = <>{workbenchSec}{activeTasksSec}</>;
    more = (
      <>
        {runtimeContextSec}
        {memorySec}
        <CapabilitiesSection capabilities={capabilities} scopeSpaceID={scopeSpaceID} scopeAgentIDs={scopeAgentIDs} />
      </>
    );
  }

  return (
    <aside className="h-full overflow-y-auto border-l-hard border-border bg-panel-3 px-5 pb-8 pt-5">
      <div>{main}</div>
      {more && (
        <>
          <button
            onClick={() => setMoreOpen((v) => !v)}
            className="mt-2 flex w-full items-center justify-between border-y border-border py-2.5 text-[11.5px] font-semibold uppercase text-text-muted hover:text-text"
          >
            <span>{moreOpen ? "Hide details" : "More details"}</span>
            <ChevronRight
              className={cn("size-3 text-text-faint transition-transform", moreOpen && "rotate-90")}
            />
          </button>
          {moreOpen && <div className="pt-5">{more}</div>}
        </>
      )}
    </aside>
  );
}

function ActiveTasksSection({ runs, archived }: { runs: AgentRun[]; archived: number }) {
  if (runs.length === 0) {
    if (archived === 0) return null;
    return (
      <Section label="Flow Tasks">
        <div className="border border-dashed border-border-soft bg-panel px-2.5 py-2 text-[12px] text-text-faint">
          No active flow tasks here. {archived} archived hidden.
        </div>
      </Section>
    );
  }
  return (
    <Section label="Flow Tasks">
      <div className="flex flex-col gap-1.5">
        {runs.slice(0, 4).map((r) => (
          <ActiveTaskMiniCard key={r.id} run={r} />
        ))}
      </div>
      {archived > 0 && (
        <div className="mt-1 text-[11px] text-text-faint">
          {archived} archived hidden
        </div>
      )}
    </Section>
  );
}

function RuntimeContextSection({
  spaceID,
  parentMessageID,
  agentID,
}: {
  spaceID?: string;
  parentMessageID?: string;
  agentID?: string;
}) {
  const [inspect, setInspect] = useState<ContextInspectView | null>(null);
  const [error, setError] = useState("");
  const [busy, setBusy] = useState("");
  const [lastReset, setLastReset] = useState("");

  const load = async () => {
    if (!spaceID) {
      setInspect(null);
      return;
    }
    try {
      const next = await api.contextInspect({
        space_id: spaceID,
        parent_message_id: parentMessageID,
        agent_id: agentID,
      });
      setInspect(next);
      setError("");
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err));
    }
  };

  useEffect(() => {
    void load();
  }, [spaceID, parentMessageID, agentID]);

  const reset = async (action: "runtime_session" | "summary") => {
    if (!spaceID) return;
    setBusy(action);
    try {
      const res = await api.contextReset({
        action,
        space_id: spaceID,
        parent_message_id: parentMessageID,
        agent_id: agentID,
      });
      setLastReset(res.note || (action === "runtime_session" ? "Runtime session reset." : "Summary reset."));
      await load();
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err));
    } finally {
      setBusy("");
    }
  };

  if (!spaceID) return null;
  return (
    <Section label="Runtime Context">
      <div className="border border-border bg-panel px-2.5 py-2">
        <div className="grid grid-cols-3 gap-px border border-border bg-border text-[10.5px]">
          <ContextMetric label="Profile" value={inspect?.profile || "-"} />
          <ContextMetric label="Selected" value={String(inspect?.selected_count ?? "-")} />
          <ContextMetric label="Filtered" value={String(totalFiltered(inspect) || 0)} />
        </div>
        <div className="mt-2 space-y-1 font-mono text-[10.5px] text-text-faint">
          <div className="truncate">session {inspect?.session_id || "-"}</div>
          <div className="truncate">source {inspect?.session_source || inspect?.source || "-"}</div>
          {inspect?.token_limit ? <div>budget {inspect.token_limit} tokens</div> : null}
        </div>
        {inspect?.filtered_counts && inspect.filtered_counts.length > 0 && (
          <div className="mt-2 flex flex-wrap gap-1">
            {inspect.filtered_counts.map((c) => (
              <CapabilityPill key={c.reason} label={`${c.reason} ${c.count}`} />
            ))}
          </div>
        )}
        {(inspect?.summary || inspect?.session_summary) && (
          <div className="mt-2 border-l-2 border-border pl-2 text-[11px] leading-[1.35] text-text-muted">
            <div className="mb-1 font-mono text-[10px] uppercase text-text-faint">
              {inspect.summary ? "Context summary" : "Session summary"}
            </div>
            <div className="line-clamp-4 whitespace-pre-wrap break-words">
              {inspect.summary || inspect.session_summary}
            </div>
          </div>
        )}
        {inspect?.messages && inspect.messages.length > 0 && (
          <div className="mt-2 space-y-1">
            {inspect.messages.slice(-3).map((m) => (
              <div key={m.id} className="truncate text-[11px] text-text-muted">
                <span className="font-mono text-text-faint">{m.role}</span> {m.content || "(empty)"}
              </div>
            ))}
          </div>
        )}
        <div className="mt-2 text-[11px] leading-[1.35] text-text-faint">
          Reset session clears model cache only. Reset summary clears compressed runtime memory. Neither deletes chat history.
        </div>
        {lastReset && <div className="mt-2 text-[11px] text-text-muted">{lastReset}</div>}
        {error && <div className="mt-2 text-[11px] text-error">{error}</div>}
        <div className="mt-2 flex flex-wrap gap-1.5">
          <Button
            variant="outline"
            size="xs"
            disabled={!!busy}
            onClick={() => void load()}
          >
            Refresh
          </Button>
          <Button
            variant="danger"
            size="xs"
            disabled={!!busy}
            onClick={() => void reset("runtime_session")}
          >
            {busy === "runtime_session" ? "Resetting..." : "Reset session"}
          </Button>
          <Button
            variant="danger"
            size="xs"
            disabled={!!busy}
            onClick={() => void reset("summary")}
          >
            {busy === "summary" ? "Resetting..." : "Reset summary"}
          </Button>
        </div>
      </div>
    </Section>
  );
}

function ContextMetric({ label, value }: { label: string; value: string }) {
  return (
    <div className="bg-panel-2 px-2 py-1.5">
      <div className="font-mono uppercase text-text-faint">{label}</div>
      <div className="truncate text-[11.5px] text-text">{value}</div>
    </div>
  );
}

function totalFiltered(inspect: ContextInspectView | null): number {
  return (inspect?.filtered_counts || []).reduce((sum, item) => sum + item.count, 0);
}

function ActiveTaskMiniCard({ run }: { run: AgentRun }) {
  const agents = useStore((s) => s.agents);
  const ag = agents.find((a) => a.id === run.agent_id);
  const status = taskStatusLabel(run.status);
  return (
    <div className="border border-border bg-panel px-2.5 py-2">
      <div className="flex items-start justify-between gap-2">
        <div className="line-clamp-2 break-words text-[12.5px] font-semibold leading-[1.35] text-text">
          {run.title || "Untitled task"}
        </div>
        <span className="shrink-0 border border-border bg-panel-2 px-1.5 py-px font-mono text-[10px] text-text-muted">
          {status}
        </span>
      </div>
      <div className="mt-1 flex items-center justify-between gap-1 text-[10.5px] text-text-faint">
        <span className="truncate">@{ag?.display || run.agent_id || "agent"}</span>
        <span className="shrink-0 font-mono">{relTime(run.time)}</span>
      </div>
    </div>
  );
}

function taskStatusLabel(status: string): string {
  const s = status.toLowerCase();
  if (s === "queued" || s === "todo") return "Todo";
  if (s === "in_review" || s === "in-review" || s === "review") return "Review";
  return "Doing";
}

function Section({ label, children }: { label: string; children: React.ReactNode }) {
  return (
    <section className="mb-6">
      <div className="mb-2 border-b border-border-soft pb-1 font-display text-[11px] font-extrabold uppercase tracking-[0.3px] text-text-muted">
        {label}
      </div>
      <div>{children}</div>
    </section>
  );
}

function AgentWorkbenchPanel({
  view,
  stateRuntime,
  activePersona,
  runs,
  participants,
  personas,
  agentDMs,
  agents,
  tools,
  capabilities,
  recentRuns,
  agentModes,
}: {
  view: string;
  stateRuntime?: string;
  activePersona?: import("@/lib/types").PersonaItem;
  runs: AgentRun[];
  participants: AgentItem[];
  personas: import("@/lib/types").PersonaItem[];
  agentDMs: import("@/lib/types").AgentDMItem[];
  agents: AgentItem[];
  tools: import("@/lib/types").ToolItem[];
  capabilities: CapabilityView | null;
  recentRuns: AgentRun[];
  agentModes?: Record<string, string>;
}) {
  const stop = useStore((s) => s.stop);
  const openAgent = useStore((s) => s.openAgent);
  const newAgentChat = useStore((s) => s.newAgentChat);
  const panelAgents = workbenchAgents(view, activePersona, participants, personas, agents);
  const runningIDs = new Set(runs.filter((r) => r.status === "running").map((r) => r.agent_id));
  const summary = capabilitySummary(capabilities);
  const failures = recentFailures(recentRuns, capabilities);
  const globalTools = tools.map((t) => t.name);
  const permission = permissionSummary(view);
  return (
    <Section label="Agent Workbench">
      <div className="mb-2 grid grid-cols-3 border border-border-soft bg-panel text-[11px]">
        <div className="border-r border-border-soft px-2 py-1.5">
          <div className="font-mono text-[10.5px] uppercase text-text-faint">Mode</div>
          <div className="truncate text-text">{permission.label}</div>
        </div>
        <div className="border-r border-border-soft px-2 py-1.5">
          <div className="font-mono text-[10.5px] uppercase text-text-faint">Skills</div>
          <div className={summary.missing > 0 ? "text-error" : "text-text"}>
            {summary.ready}/{summary.total} ready
          </div>
        </div>
        <div className="px-2 py-1.5">
          <div className="font-mono text-[10.5px] uppercase text-text-faint">Failures</div>
          <div className={failures.length > 0 ? "text-error" : "text-text"}>{failures.length || "none"}</div>
        </div>
      </div>
      <div className="flex flex-col gap-2">
        {panelAgents.map((p) => {
          const display = p.display || p.id;
          const runtime = agentRuntimeSummary(p.runtime, p.model, stateRuntime);
          const runState = agentRunState(p.id, runs, recentRuns);
          const isRunning = runningIDs.has(p.id) || p.status === "running" || runState.running;
          const hasDM = agentDMs.some((d) => d.persona_id === p.id);
          const agentTools = p.tools && p.tools.length > 0 ? p.tools : globalTools;
          const routeMode = routeModeLabel(view, agentModes?.[p.id]);
          return (
            <div key={p.id} className={cn(
              "border bg-panel px-2.5 py-2",
              isRunning ? "border-border border-l-4 border-l-running" : "border-border-soft",
            )}>
              <div className="flex min-w-0 items-center gap-2">
                <Dot status={isRunning ? "running" : "idle"} />
                <span className="min-w-0 flex-1 truncate text-[12.5px] font-semibold text-text">
                  @{display}
                </span>
                <span className="shrink-0 font-mono text-[10.5px] uppercase text-text-faint">
                  {agentStatusLabel(isRunning, runState.queued, hasDM)}
                </span>
              </div>
              <div className="mt-1 truncate font-mono text-[10.5px] text-text-muted">
                {runtime || runtimeLabel(undefined, stateRuntime)}
              </div>
              {p.description && !isRunning && (
                <div className="mt-1 line-clamp-2 text-[11.5px] leading-[1.4] text-text-muted">
                  {p.description}
                </div>
              )}
              <div className="mt-2 flex flex-wrap gap-1">
                <CapabilityPill label={permission.short} />
                {routeMode && <CapabilityPill label={routeMode} />}
                {runState.queued > 0 && <CapabilityPill label={`queued ${runState.queued}`} />}
                <CapabilityPill label={agentTools.length > 0 ? `${agentTools.length} tools` : "no tools"} />
                <CapabilityPill label={summary.missing > 0 ? `${summary.missing} skill config missing` : `${summary.ready} skills ready`} error={summary.missing > 0} />
              </div>
              {agentTools.length > 0 && (
                <div className="mt-1.5 line-clamp-1 font-mono text-[10.5px] text-text-faint">
                  {agentTools.slice(0, 5).join(" · ")}
                </div>
              )}
              {failures.length > 0 && (
                <div className="mt-1.5 line-clamp-2 text-[11px] leading-[1.35] text-error">
                  Recent failure: {failures[0]}
                </div>
              )}
              <div className="mt-2 flex gap-1.5">
                {p.id !== "sumi" && (
                  <>
                    <Button variant="default" size="sm" className="h-6 px-2 text-[11px]" onClick={() => void openAgent(p.id)}>
                      DM
                    </Button>
                    <Button variant="default" size="sm" className="h-6 px-2 text-[11px]" onClick={() => void newAgentChat(p.id)}>
                      Chat
                    </Button>
                  </>
                )}
                {isRunning && (
                  <Button variant="danger" size="sm" className="ml-auto h-6 px-2 text-[11px]" onClick={() => void stop()}>
                    <Square className="size-2.5" />
                    <span>Stop</span>
                  </Button>
                )}
              </div>
            </div>
          );
        })}
      </div>
      {panelAgents.length === 0 && (
        <div className="border border-dashed border-border-soft bg-panel px-2.5 py-2 text-[12px] text-text-muted">
          No agent attached here.
        </div>
      )}
    </Section>
  );
}

function personaForRuntime(
  id: string | null,
  detailPersonaID: string | undefined,
  personas: import("@/lib/types").PersonaItem[],
  agentDMs: import("@/lib/types").AgentDMItem[],
): import("@/lib/types").PersonaItem | undefined {
  const dm = id ? agentDMs.find((d) => d.id === id) : undefined;
  const personaID = detailPersonaID || dm?.persona_id || id || "";
  return personas.find((p) => p.id === personaID);
}

type WorkbenchAgent = {
  id: string;
  display: string;
  runtime?: string;
  model?: string;
  status: string;
  description?: string;
  tools?: string[];
};

function workbenchAgents(
  view: string,
  activePersona: import("@/lib/types").PersonaItem | undefined,
  participants: AgentItem[],
  personas: import("@/lib/types").PersonaItem[],
  agents: AgentItem[],
): WorkbenchAgent[] {
  if (view === "agent" && activePersona) {
    return [personaWorkbenchAgent(activePersona, agents.find((a) => a.id === activePersona.id)?.status || "idle")];
  }
  if (participants.length > 0) {
    return participants.map((agent) => {
      const p = personas.find((item) => item.id === agent.id);
      return {
        id: agent.id,
        display: agent.display || p?.display || agent.id,
        runtime: p?.runtime || agent.runtime,
        model: p?.model || agent.model,
        status: agent.status,
        description: p?.description || agent.role,
        tools: p?.tools,
      };
    });
  }
  if (view === "direct" || view === "channel" || view === "thread") {
    return [];
  }
  return [{
    id: "sumi",
    display: "Sumi",
    status: "idle",
    description: "Default assistant for direct conversation.",
  }];
}

function personaWorkbenchAgent(p: import("@/lib/types").PersonaItem, status: string): WorkbenchAgent {
  return {
    id: p.id,
    display: p.display || p.id,
    runtime: p.runtime,
    model: p.model,
    status,
    description: p.description,
    tools: p.tools,
  };
}

function permissionSummary(view: string): { label: string; short: string } {
  if (view === "channel") {
    return { label: "Routed channel", short: "routed" };
  }
  if (view === "thread") {
    return { label: "Routed thread", short: "thread" };
  }
  if (view === "direct") {
    return { label: "Direct chat", short: "direct" };
  }
  if (view === "agent") {
    return { label: "Direct agent", short: "direct" };
  }
  return { label: "Direct chat", short: "direct" };
}

function capabilitySummary(capabilities: CapabilityView | null): { total: number; ready: number; missing: number } {
  const skills = capabilities?.skills || [];
  const missing = skills.filter((s) => !s.configured).length;
  return { total: skills.length, ready: skills.length - missing, missing };
}

function recentFailures(runs: AgentRun[], capabilities: CapabilityView | null): string[] {
  const failedRuns = runs
    .filter((r) => activeStatus(r.status) && failureStatus(r.status))
    .map((r) => `${r.title || r.agent_id} · ${r.status}`);
  const failedTasks = (capabilities?.tasks || [])
    .filter((t) => activeTask(t) && failureStatus(t.run_status || t.status))
    .map((t) => `${t.title} · ${t.run_status || t.status}`);
  return [...failedRuns, ...failedTasks].slice(0, 3);
}

function failureStatus(status: string | undefined): boolean {
  const s = (status || "").toLowerCase();
  return s === "failed" || s === "error" || s === "canceled" || s === "rollback_failed" || s === "no_output";
}

function agentRunState(agentID: string, runs: AgentRun[], recentRuns: AgentRun[]): { running: boolean; queued: number } {
  const all = [...runs, ...recentRuns].filter((r) => r.agent_id === agentID);
  return {
    running: all.some((r) => r.status === "running"),
    queued: recentRuns.filter((r) => r.agent_id === agentID && r.status === "queued").length,
  };
}

function activeRuns(runs: AgentRun[]): AgentRun[] {
  const seen = new Set<string>();
  const out: AgentRun[] = [];
  for (const run of runs) {
    if ((run.lifecycle || "active") !== "active" || seen.has(run.id)) continue;
    seen.add(run.id);
    out.push(run);
  }
  return out;
}

function activeStatus(status: string | undefined): boolean {
  const s = (status || "").toLowerCase();
  return s === "queued" || s === "running" || s === "in_progress" || s === "in-review" || s === "in_review";
}

function activeTask(t: TaskStateCard): boolean {
  return (t.lifecycle || "active") === "active";
}

function taskInScope(t: TaskStateCard, spaceID: string | undefined, agentIDs: string[]): boolean {
  if (spaceID && t.space_id === spaceID) return true;
  if (t.worker_id && agentIDs.includes(t.worker_id)) return true;
  return false;
}

function agentStatusLabel(running: boolean, queued: number, hasDM: boolean): string {
  if (running && queued > 0) return `working · q${queued}`;
  if (running) return "working";
  if (queued > 0) return `queued ${queued}`;
  return hasDM ? "dm" : "ready";
}

function routeModeLabel(view: string, mode: string | undefined): string {
  if (view !== "channel" && view !== "thread") return "";
  return mode === "listen" ? "listening" : "mention-only";
}

function CapabilityPill({ label, error }: { label: string; error?: boolean }) {
  return (
    <span className={cn(
      "border px-1.5 py-px font-mono text-[10.5px]",
      error ? "border-error/50 text-error" : "border-border-soft text-text-faint",
    )}>
      {label}
    </span>
  );
}

function runtimeLabel(runtime: string | undefined, fallback: string | undefined): string {
  return runtime || fallback || "default";
}

function agentRuntimeSummary(runtime: string | undefined, model: string | undefined, fallbackRuntime: string | undefined): string {
  const label = runtimeLabel(runtime, fallbackRuntime);
  return model ? `${label} / ${model}` : label;
}

function ThreadMiniCard({ thread, showChannel }: { thread: ThreadItem; showChannel: boolean }) {
  const channels = useStore((s) => s.channels);
  const openThread = useStore((s) => s.openThread);
  const ch = channels.find((c) => c.id === thread.channel_id);
  return (
    <button
      onClick={() => void openThread(thread.id)}
      className="w-full cursor-pointer border-2 border-transparent px-2 py-1.5 text-left text-text-muted transition-colors hover:border-border hover:bg-panel hover:text-text"
    >
      <div className="flex items-center gap-1.5 text-[12.5px] text-text">
        {thread.has_running && <Dot status="running" />}
        <span className="truncate">{thread.title}</span>
      </div>
      <div className="text-[11px] text-text-faint mt-0.5">
        {(showChannel && ch ? `#${ch.name} · ` : "") + relTime(thread.updated_at)}
      </div>
    </button>
  );
}

function CapabilitiesSection({
  capabilities,
  scopeSpaceID,
  scopeAgentIDs,
}: {
  capabilities: CapabilityView | null;
  scopeSpaceID?: string;
  scopeAgentIDs: string[];
}) {
  const [selectedSkill, setSelectedSkill] = useState<string | null>(null);
  const [skillDetail, setSkillDetail] = useState<SkillView | null>(null);
  const [skillError, setSkillError] = useState<string>("");

  useEffect(() => {
    if (!capabilities || capabilities.skills.length === 0) {
      setSelectedSkill(null);
      setSkillDetail(null);
      return;
    }
    setSelectedSkill((current) => {
      if (current && capabilities.skills.some((s) => s.name === current)) return current;
      return null;
    });
  }, [capabilities]);

  useEffect(() => {
    if (!selectedSkill) {
      setSkillDetail(null);
      return;
    }
    let alive = true;
    setSkillError("");
    api.skill(selectedSkill)
      .then((detail) => {
        if (alive) setSkillDetail(detail?.name ? detail : null);
      })
      .catch((err) => {
        if (alive) setSkillError(err instanceof Error ? err.message : String(err));
      });
    return () => {
      alive = false;
    };
  }, [selectedSkill]);

  if (!capabilities) {
    return (
      <Section label="Capabilities">
        <div className="text-[12px] text-text-faint">Loading capability state...</div>
      </Section>
    );
  }
  const skills = capabilities.skills;
  const scopedTasks = capabilities.tasks.filter((t) => taskInScope(t, scopeSpaceID, scopeAgentIDs));
  const tasks = scopedTasks.filter(activeTask).slice(0, 3);
  const archivedTasks = capabilities.archived_task_state_count || 0;
  const proposals = capabilities.action_proposals.slice(0, 3);
  const empty = skills.length === 0 && tasks.length === 0 && proposals.length === 0 && archivedTasks === 0;
  const selected = skillDetail?.name === selectedSkill ? skillDetail : skills.find((s) => s.name === selectedSkill) || null;
  return (
    <Section label="Capabilities">
      {empty ? (
        <div className="text-[12px] text-text-faint">No capability state recorded.</div>
      ) : (
        <div className="flex flex-col gap-3">
          {skills.length > 0 && (
            <CapabilityGroup label={`Skills · ${skills.length}`}>
              <div className="flex flex-col gap-1.5">
                {skills.map((s) => (
                  <button
                    key={s.name}
                    type="button"
                    onClick={() => setSelectedSkill((current) => (current === s.name ? null : s.name))}
                    className={cn(
                      "border border-border bg-panel px-2.5 py-2 text-left text-[12px] transition-colors hover:border-text-faint",
                      selectedSkill === s.name && "border-text-muted bg-panel-2",
                    )}
                  >
                    <div className="flex items-center justify-between gap-2">
                      <span className="truncate font-semibold text-text">{s.name}</span>
                      <span className={cn(
                        "shrink-0 border px-1.5 py-px font-mono text-[10.5px]",
                        s.configured ? "border-border text-text-muted" : "border-error/50 text-error",
                      )}>
                        {skillStatus(s)}
                      </span>
                    </div>
                    <div className="mt-1 line-clamp-2 text-[11.5px] leading-[1.4] text-text-muted">
                      {s.when || s.description || s.risk || "Skill card"}
                    </div>
                  </button>
                ))}
              </div>
              {!selected && (
                <div className="border border-dashed border-border bg-panel px-2.5 py-2 text-[11.5px] text-text-faint">
                  Select a skill to inspect its detail.
                </div>
              )}
              {selected && (
                <CapabilityCard>
                  <div className="flex items-center justify-between gap-2">
                    <span className="font-mono text-[10.5px] uppercase text-text-faint">Skill detail</span>
                    <span className="flex items-center gap-2">
                      {selected.last_action && <span className="text-[11px] text-text-faint">last {selected.last_action}</span>}
                      <button
                        type="button"
                        onClick={() => setSelectedSkill(null)}
                        className="border border-border bg-panel-2 px-1.5 py-px text-[10.5px] text-text-muted hover:text-text"
                      >
                        Close
                      </button>
                    </span>
                  </div>
                  <div className="mt-1.5 font-semibold text-text">{selected.name}</div>
                  {(selected.when || selected.description) && (
                    <div className="mt-1 text-[11.5px] leading-[1.45] text-text-muted">
                      {selected.when || selected.description}
                    </div>
                  )}
                  {selected.risk && (
                    <div className="mt-1 font-mono text-[10.5px] uppercase text-text-faint">
                      Risk · {selected.risk}
                    </div>
                  )}
                  {selected.env_needs && selected.env_needs.length > 0 && (
                    <div className="mt-2 flex flex-col gap-1">
                      {selected.env_needs.map((need) => (
                        <div key={need.name} className="text-[11.5px] leading-[1.35] text-text-muted">
                          <span className={need.configured ? "text-text-faint" : "text-error"}>
                            {need.configured ? "Configured" : "Missing"}
                          </span>
                          {" · "}
                          <span className="font-mono">{need.name}</span>
                          {!need.configured && need.hint && (
                            <div className="mt-0.5 text-text-faint">{need.hint}</div>
                          )}
                        </div>
                      ))}
                    </div>
                  )}
                  {selected.examples && selected.examples.length > 0 && (
                    <div className="mt-2 text-[11.5px] leading-[1.4] text-text-muted">
                      {selected.examples.slice(0, 2).join(" · ")}
                    </div>
                  )}
                  {selected.body && (
                    <div className="mt-2 max-h-32 overflow-auto border border-border bg-bg px-2 py-1.5 font-mono text-[10.5px] leading-[1.45] text-text-faint">
                      {skillBodyPreview(selected.body)}
                    </div>
                  )}
                  {skillError && <div className="mt-2 text-[11.5px] text-error">{skillError}</div>}
                </CapabilityCard>
              )}
            </CapabilityGroup>
          )}
          {tasks.length > 0 && (
            <CapabilityGroup label="Flow Task State">
              {tasks.map((t) => (
                <CapabilityCard key={t.id}>
                  <div className="flex items-center justify-between gap-2">
                    <span className="line-clamp-1 font-semibold text-text">{t.title}</span>
                    <span className="shrink-0 font-mono text-[10.5px] text-text-muted">{t.run_status || t.status}</span>
                  </div>
                  <div className="mt-1 text-[11.5px] leading-[1.4] text-text-muted">
                    {taskStateLine(t)}
                  </div>
                </CapabilityCard>
              ))}
            </CapabilityGroup>
          )}
          {archivedTasks > 0 && (
            <div className="border border-dashed border-border bg-panel px-2.5 py-2 text-[11.5px] text-text-faint">
              {archivedTasks} archived tasks hidden
            </div>
          )}
          {proposals.length > 0 && (
            <CapabilityGroup label="Action Proposals">
              {proposals.map((p, i) => (
                <CapabilityCard key={`${p.time}-${i}`}>
                  <div className="flex items-center justify-between gap-2">
                    <span className="truncate font-semibold text-text">{p.tool || "action"}</span>
                    <span className="shrink-0 font-mono text-[10.5px] text-text-muted">{p.result || relTime(p.time)}</span>
                  </div>
                  <div className="mt-1 line-clamp-2 text-[11.5px] leading-[1.4] text-text-muted">
                    {proposalLine(p)}
                  </div>
                </CapabilityCard>
              ))}
            </CapabilityGroup>
          )}
        </div>
      )}
    </Section>
  );
}

function CapabilityGroup({ label, children }: { label: string; children: React.ReactNode }) {
  return (
    <div>
      <div className="mb-1.5 font-mono text-[10.5px] uppercase text-text-faint">
        {label}
      </div>
      <div className="flex flex-col gap-1.5">{children}</div>
    </div>
  );
}

function CapabilityCard({ children }: { children: React.ReactNode }) {
  return (
    <div className="border border-border bg-panel px-2.5 py-2 text-[12px]">
      {children}
    </div>
  );
}

function taskStateLine(t: TaskStateCard): string {
  const checkpoint = t.state?.checkpoint || t.outcome || "No checkpoint";
  const blockers = t.state?.blockers?.length || 0;
  if (blockers > 0) {
    return `${checkpoint} · ${blockers} blocker${blockers === 1 ? "" : "s"}`;
  }
  return checkpoint;
}

function proposalLine(p: ActionProposalCard): string {
  const parts = [p.intent, p.target, p.risk].filter((v): v is string => !!v && v.trim() !== "");
  if (parts.length > 0) {
    return parts.join(" · ");
  }
  return p.source || "No proposal detail";
}

function skillStatus(s: SkillView): string {
  if (!s.configured) {
    const count = s.missing_env?.length || 0;
    return count > 1 ? `missing ${count}` : "missing";
  }
  return s.risk || "ready";
}

function skillBodyPreview(body: string): string {
  let text = body.trim();
  if (text.startsWith("---")) {
    const end = text.indexOf("\n---", 3);
    if (end >= 0) {
      text = text.slice(end + 4).trim();
    }
  }
  text = text.replace(/^#\s+/gm, "").trim();
  return firstSentence(text);
}

function firstSentence(s: string): string {
  const trimmed = s.trim();
  const m = trimmed.match(/^(.{0,140}?[.。!?！？])/);
  if (m) return m[1];
  if (trimmed.length <= 140) return trimmed;
  return trimmed.slice(0, 140) + "…";
}
