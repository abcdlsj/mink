import { useEffect, useState } from "react";
import { ChevronRight, Square } from "lucide-react";
import { useStore } from "@/lib/store";
import { Button } from "@/components/ui/button";
import { Dot } from "./LeftPane";
import { api } from "@/lib/api";
import { cn, relTime } from "@/lib/utils";
import { failureStatus } from "@/lib/task-helpers";
import { MemoryOverviewCard } from "./MemoryOverviewCard";
import type {
  AgentItem,
  AgentRun,
  ActionProposalCard,
  CapabilityView,
  ContextInspectView,
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
  const streamingByID = useStore((s) => s.streamingByID);
  const capabilities = useStore((s) => s.capabilities);
  const [moreOpen, setMoreOpen] = useState(false);

  const inThread = !!threadDetail && !threadDetail.unsupported && !threadDetail.not_found;
  const threadParticipants = inThread ? threadDetail!.participants : null;
  const threadRecentRuns = inThread ? threadDetail!.recent_runs : null;
  const workbenchView = inThread ? "thread" : view;

  if (!detail && !inThread) return <aside className="h-full border-l border-border-soft bg-panel-3 px-3 py-4" />;

  const liveRuns: AgentRun[] = Object.values(streamingByID).map((s) => ({
    id: s.messageID,
    agent_id: s.agentID || "agent",
    title: "Working now",
    status: "running",
    lifecycle: "active",
    time: s.startedAt,
  }));
  const activeTaskRuns: AgentRun[] = participants?.active_runs || [];
  const runtimeRuns: AgentRun[] = liveRuns.length > 0 ? liveRuns : activeTaskRuns;
  const activePersona = personaForRuntime(activeAgentSpace, detail?.item?.persona_id, personas, agentDMs);
  const activeChannelItem = view === "channel" ? channels.find((c) => c.id === activeChannel) : undefined;
  const scopeRecentRuns = inThread
    ? (threadRecentRuns || [])
    : (participants?.recent_runs || []);
  const activeScopeRuns = activeRuns(scopeRecentRuns.length > 0 ? scopeRecentRuns : activeTaskRuns);
  const activeScopeSpaceID = inThread ? threadDetail?.space_id : detail?.item.id;
  const activeScopeAgentIDs = activePersona
    ? [activePersona.id]
    : (inThread ? (threadParticipants || []) : (participants?.agents || [])).map((a) => a.id);
  const archivedScopeRuns = inThread
    ? (threadDetail?.archived_runs_count || 0)
    : (participants?.archived_runs_count || 0);
  const contextAgentID = activePersona?.id || activeScopeAgentIDs[0] || "";
  const agentModes = inThread
    ? threadDetail?.agent_modes
    : view === "channel"
      ? activeChannelItem?.agent_modes
      : undefined;
  const runtimeContextSec = (
    <RuntimeContextSection
      spaceID={activeScopeSpaceID}
      parentMessageID={inThread ? threadDetail?.parent_id : undefined}
      agentID={contextAgentID}
    />
  );
  const memorySec = (
    <Section label="Memory">
      <MemoryOverviewCard
        personaID={activePersona?.id}
        spaceID={activeScopeSpaceID}
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
        capabilities={capabilities}
        recentRuns={activeScopeRuns}
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
        <CapabilitySummarySection capabilities={capabilities} scopeSpaceID={activeScopeSpaceID} scopeAgentIDs={activeScopeAgentIDs} />
      </>
    );
  } else if (view === "direct" || inThread) {
    main = (
      <>
        {workbenchSec}
        {activeTasksSec}
        {!inThread && memorySec}
      </>
    );
    more = (
      <>
        {runtimeContextSec}
        {inThread && memorySec}
        <CapabilitySummarySection capabilities={capabilities} scopeSpaceID={activeScopeSpaceID} scopeAgentIDs={activeScopeAgentIDs} />
      </>
    );
  } else if (view === "agent") {
    main = (
      <>
        {workbenchSec}
        {activeTasksSec}
        {memorySec}
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
        <CapabilitySummarySection capabilities={capabilities} scopeSpaceID={activeScopeSpaceID} scopeAgentIDs={activeScopeAgentIDs} />
      </>
    );
  } else {
    main = <>{workbenchSec}{activeTasksSec}</>;
    more = (
      <>
        {runtimeContextSec}
        {memorySec}
        <CapabilitySummarySection capabilities={capabilities} scopeSpaceID={activeScopeSpaceID} scopeAgentIDs={activeScopeAgentIDs} />
      </>
    );
  }

  return (
    <aside className="h-full overflow-y-auto border-l border-border-soft bg-panel-3 px-4 pb-8 pt-4">
      <div>{main}</div>
      {more && (
        <>
          <button
            onClick={() => setMoreOpen((v) => !v)}
            className="mt-1 flex w-full items-center justify-between border-t border-border-soft py-2.5 text-[11.5px] font-semibold uppercase text-text-muted hover:text-text"
          >
            <span>{moreOpen ? "Hide details" : "More details"}</span>
            <ChevronRight
              className={cn("size-3 text-text-faint transition-transform", moreOpen && "rotate-90")}
            />
          </button>
          {moreOpen && <div className="pt-4">{more}</div>}
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
        <div className="px-0.5 py-1 text-[12px] text-text-faint">
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
      <div className="bg-panel/60 px-2.5 py-2">
        <div className="grid grid-cols-3 gap-1 text-[10.5px]">
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
          <div className="mt-2 border-l border-border-soft pl-2 text-[11px] leading-[1.35] text-text-muted">
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
          Clear cache resets the model's runtime token only. Clear summary removes compressed context memory. Neither deletes chat history.
        </div>
        {lastReset && <div className="mt-2 text-[11px] text-text-muted">{lastReset}</div>}
        {error && <div className="mt-2 text-[11px] text-error">{error}</div>}
        <div className="mt-2 flex flex-wrap gap-1.5">
          <Button
            variant="outline"
            size="xs"
            disabled={!!busy}
            onClick={() => void load()}
            title="Reload context data from the backend"
          >
            Refresh
          </Button>
          <Button
            variant="danger"
            size="xs"
            disabled={!!busy}
            onClick={() => void reset("runtime_session")}
            title="Clears the model's runtime token cache. Chat history is preserved."
          >
            {busy === "runtime_session" ? "Clearing…" : "Clear cache"}
          </Button>
          <Button
            variant="danger"
            size="xs"
            disabled={!!busy}
            onClick={() => void reset("summary")}
            title="Clears the compressed context summary. Chat history is preserved."
          >
            {busy === "summary" ? "Clearing…" : "Clear summary"}
          </Button>
        </div>
      </div>
    </Section>
  );
}

function ContextMetric({ label, value }: { label: string; value: string }) {
  return (
    <div className="bg-panel-2/70 px-2 py-1.5">
      <div className="font-mono font-medium uppercase text-text-muted">{label}</div>
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
    <div className="border border-border-soft bg-panel/70 px-2.5 py-2">
      <div className="flex items-start justify-between gap-2">
        <div className="line-clamp-2 break-words text-[12.5px] font-semibold leading-[1.35] text-text">
          {run.title || "Untitled task"}
        </div>
        <span className="shrink-0 border border-border bg-panel-2 px-1.5 py-px font-mono text-[10px] font-medium text-text">
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
    <section className="mb-5">
      <div className="mb-2 font-display text-[11px] font-bold uppercase tracking-[0.35px] text-text-muted">
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
  capabilities: CapabilityView | null;
  recentRuns: AgentRun[];
  agentModes?: Record<string, string>;
}) {
  const stop = useStore((s) => s.stop);
  const openAgent = useStore((s) => s.openAgent);
  const newAgentChat = useStore((s) => s.newAgentChat);
  const panelAgents = workbenchAgents(view, activePersona, participants, personas, agents);
  const runningIDs = new Set(runs.filter((r) => r.status === "running").map((r) => r.agent_id));
  const runningByAgent = new Map<string, AgentRun>();
  for (const run of runs) {
    if (run.status !== "running") continue;
    if (!runningByAgent.has(run.agent_id)) runningByAgent.set(run.agent_id, run);
  }
  const summary = capabilitySummary(capabilities);
  const failures = recentFailures(recentRuns, capabilities);
  const permission = permissionSummary(view);
  const runningCount = runningByAgent.size;
  const queuedCount = recentRuns.filter((r) => r.status === "queued").length;
  return (
    <Section label="Agent Workbench">
      <div className="mb-2 flex flex-wrap gap-x-2 gap-y-1 text-[11.5px] leading-[1.4] text-text-muted">
        <span>{permission.label}</span>
        {runningCount > 0 && <span className="text-running">· {runningCount} working</span>}
        {queuedCount > 0 && <span>· {queuedCount} queued</span>}
        <span className={summary.missing > 0 ? "text-error" : "text-text-faint"}>
          · {summary.total > 0 ? `${summary.ready} skills ready` : "no skills"}
          {summary.missing > 0 ? `, ${summary.missing} missing` : ""}
        </span>
        {recentRuns.length > 0 && <span className="text-error">· {failures.length} failure</span>}
      </div>
      <div className="flex flex-col gap-2">
        {panelAgents.map((p) => {
          const display = p.display || p.id;
          const runtime = agentRuntimeSummary(p.runtime, p.model, stateRuntime);
          const runState = agentRunState(p.id, runs, recentRuns);
          const isRunning = runningIDs.has(p.id) || p.status === "running" || runState.running;
          const hasDM = agentDMs.some((d) => d.persona_id === p.id);
          const routeMode = routeModeLabel(view, agentModes?.[p.id]);
          return (
            <div key={p.id} className={cn(
              "border bg-panel/70 px-2.5 py-2",
              isRunning ? "border-border-soft border-l-2 border-l-running" : "border-border-soft",
            )}>
              <div className="flex min-w-0 items-start gap-2">
                <Dot status={isRunning ? "running" : "idle"} className="mt-1" />
                <div className="min-w-0 flex-1">
                  <div className="flex min-w-0 items-center gap-2">
                    <span className="min-w-0 flex-1 truncate text-[12.5px] font-semibold text-text">
                      @{display}
                    </span>
                    <span className="shrink-0 font-mono text-[10.5px] uppercase text-text-faint">
                      {agentStatusLabel(isRunning, runState.queued, hasDM)}
                    </span>
                  </div>
                  <div className="mt-0.5 flex flex-wrap gap-x-1.5 gap-y-0.5 text-[10.5px] text-text-faint">
                    <span className="truncate font-mono">{runtime || runtimeLabel(undefined, stateRuntime)}</span>
                    {routeMode && <span>· {routeMode}</span>}
                    {runState.queued > 0 && <span>· queued {runState.queued}</span>}
                    {summary.total > 0 && <span>· {summary.ready} skills ready</span>}
                  </div>
                </div>
              </div>
              {failures.length > 0 && (
                <div className="mt-1.5 line-clamp-2 text-[11px] leading-[1.35] text-error">
                  Recent failure: {failures[0]}
                </div>
              )}
              <div className="mt-2 flex gap-1.5">
                {p.id !== "sumi" && (
                  <>
                    <Button variant="outline" size="sm" className="h-6 px-2 text-[11px]" onClick={() => void openAgent(p.id)}>
                      DM
                    </Button>
                    <Button variant="outline" size="sm" className="h-6 px-2 text-[11px]" onClick={() => void newAgentChat(p.id)}>
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
        <div className="px-0.5 py-1 text-[12px] text-text-faint">
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
  }];
}

function personaWorkbenchAgent(p: import("@/lib/types").PersonaItem, status: string): WorkbenchAgent {
  return {
    id: p.id,
    display: p.display || p.id,
    runtime: p.runtime,
    model: p.model,
    status,
  };
}

function permissionSummary(view: string): { label: string } {
  if (view === "channel") {
    return { label: "Routed channel" };
  }
  if (view === "thread") {
    return { label: "Routed thread" };
  }
  if (view === "direct") {
    return { label: "Direct chat" };
  }
  if (view === "agent") {
    return { label: "Direct agent" };
  }
  return { label: "Direct chat" };
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
      error ? "border-error-border text-error" : "border-border-soft text-text-faint",
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

function CapabilitySummarySection({
  capabilities,
  scopeSpaceID,
  scopeAgentIDs,
}: {
  capabilities: CapabilityView | null;
  scopeSpaceID?: string;
  scopeAgentIDs: string[];
}) {
  if (!capabilities) {
    return (
      <Section label="Capabilities">
        <div className="text-[12px] text-text-faint">Loading capability state...</div>
      </Section>
    );
  }
  const summary = capabilitySummary(capabilities);
  const scopedTasks = capabilities.tasks.filter((t) => taskInScope(t, scopeSpaceID, scopeAgentIDs));
  const activeTasks = scopedTasks.filter(activeTask).length;
  const archivedTasks = capabilities.archived_task_state_count || 0;
  const proposals = capabilities.action_proposals.length;
  const empty = summary.total === 0 && activeTasks === 0 && proposals === 0 && archivedTasks === 0;
  return (
    <Section label="Capabilities">
      {empty ? (
        <div className="text-[12px] text-text-faint">No capability state recorded.</div>
      ) : (
        <div className="space-y-1.5 text-[12px] leading-[1.45] text-text-muted">
          {summary.total > 0 && (
            <div className={summary.missing > 0 ? "text-error" : "text-text-muted"}>
              {summary.ready} skills ready{summary.missing > 0 ? ` · ${summary.missing} missing config` : ""}
            </div>
          )}
          {activeTasks > 0 && <div>{activeTasks} active flow task{activeTasks === 1 ? "" : "s"}</div>}
          {proposals > 0 && (
            <>
              <div>{proposals} action proposal{proposals === 1 ? "" : "s"}</div>
              <div className="space-y-1.5">
                {capabilities.action_proposals.slice(0, 4).map((proposal) => (
                  <TaskProposalCard key={proposal.id || proposal.preview || proposal.time} proposal={proposal} />
                ))}
              </div>
            </>
          )}
          {archivedTasks > 0 && <div className="text-text-faint">{archivedTasks} archived tasks hidden</div>}
          <div className="pt-0.5 text-[11px] text-text-faint">
            Skill details live in the agent profile; this rail only shows current readiness.
          </div>
        </div>
      )}
    </Section>
  );
}

function TaskProposalCard({ proposal }: { proposal: ActionProposalCard }) {
  const refreshCapabilities = useStore((s) => s.refreshCapabilities);
  const openCurrentRoute = useStore((s) => s.openCurrentRoute);
  const [busy, setBusy] = useState<"" | "commit" | "reject">("");
  const [error, setError] = useState("");

  const act = async (kind: "commit" | "reject") => {
    if (!proposal.id || busy) return;
    setBusy(kind);
    setError("");
    try {
      if (kind === "commit") {
        await api.commitTaskProposal(proposal.id);
      } else {
        await api.rejectTaskProposal(proposal.id);
      }
      await Promise.all([refreshCapabilities(), openCurrentRoute()]);
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err));
    } finally {
      setBusy("");
    }
  };

  return (
    <div className="border border-border-soft bg-panel/70 px-2.5 py-2">
      <div className="flex items-start justify-between gap-2">
        <div className="min-w-0">
          <div className="truncate text-[12.5px] font-semibold text-text">{proposal.title || proposal.intent || "Task proposal"}</div>
          <div className="mt-0.5 flex flex-wrap gap-x-1.5 gap-y-0.5 text-[10.5px] text-text-faint">
            <span>{proposal.target || "unknown target"}</span>
            {proposal.assignee && <span>· @{proposal.assignee}</span>}
            <span>· {proposal.risk || "safe"}</span>
          </div>
        </div>
        <span className="shrink-0 border border-border bg-panel-2 px-1.5 py-px font-mono text-[10px] text-text">
          {proposal.status || proposal.result || "prepared"}
        </span>
      </div>
      {proposal.expected_outcome && (
        <div className="mt-1 text-[11px] leading-[1.4] text-text-muted">
          {proposal.expected_outcome}
        </div>
      )}
      {proposal.acceptance_criteria && (
        <div className="mt-1 text-[11px] leading-[1.4] text-text-faint">
          {proposal.acceptance_criteria}
        </div>
      )}
      {error && <div className="mt-1 text-[11px] text-error">{error}</div>}
      <div className="mt-2 flex gap-1.5">
        <Button variant="outline" size="xs" disabled={!proposal.id || !!busy} onClick={() => void act("reject")}>
          {busy === "reject" ? "Rejecting…" : "Reject"}
        </Button>
        <Button variant="default" size="xs" disabled={!proposal.id || !!busy} onClick={() => void act("commit")}>
          {busy === "commit" ? "Committing…" : "Commit"}
        </Button>
      </div>
    </div>
  );
}
