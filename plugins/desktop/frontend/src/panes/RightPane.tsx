import { useEffect, useState } from "react";
import { ChevronRight, Square } from "lucide-react";
import { useStore } from "@/lib/store";
import { Button } from "@/components/ui/button";
import { Dot } from "./LeftPane";
import { api } from "@/lib/api";
import { cn, relTime } from "@/lib/utils";
import type {
  ActionProposalCard,
  AgentItem,
  AgentRun,
  CapabilityView,
  RunDetail,
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
  const activeAgent = useStore((s) => s.activeAgent);
  const participants = useStore((s) => s.participants);
  const tools = useStore((s) => s.tools);
  const streamingByID = useStore((s) => s.streamingByID);
  const capabilities = useStore((s) => s.capabilities);
  const [moreOpen, setMoreOpen] = useState(true);

  const inThread = !!threadDetail && !threadDetail.unsupported && !threadDetail.not_found;
  const threadParticipants = inThread ? threadDetail!.participants : null;
  const threadRecentRuns = inThread ? threadDetail!.recent_runs : null;

  if (!detail && !inThread) return <aside className="h-full border-l-hard border-border bg-panel-3 px-3 py-4" />;

  const liveRuns: AgentRun[] = Object.values(streamingByID).map((s) => ({
    id: s.messageID,
    agent_id: s.agentID || "agent",
    title: "Current turn",
    status: "running",
    time: s.startedAt,
  }));
  const runtimeRuns: AgentRun[] = liveRuns.length > 0 ? liveRuns : participants?.active_runs || [];
  const activePersona = personaForRuntime(activeAgent, detail?.item?.persona_id, personas, agentDMs);

  let main: React.ReactNode = null;
  let more: React.ReactNode = null;

  const workbenchSec = (
    <AgentWorkbenchPanel
      view={view}
      stateRuntime={state?.runtime}
      activePersona={activePersona}
      runs={runtimeRuns}
      participants={inThread ? (threadParticipants || []) : (participants?.agents || [])}
      personas={personas}
      agentDMs={agentDMs}
      agents={agents}
      tools={tools}
      capabilities={capabilities}
      recentRuns={inThread ? (threadRecentRuns || []) : (participants?.recent_runs || [])}
    />
  );

  if (view === "channel" && !inThread) {
    const ch = channels.find((c) => c.id === activeChannel);
    const channelThreads = threads.filter((t) => t.channel_id === activeChannel).slice(0, 3);
    main = (
      <>
        {ch?.topic && (
          <Section label="Topic">
            <div className="text-[13px] text-text">{ch.topic}</div>
          </Section>
        )}
        {workbenchSec}
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
        <CapabilitiesSection capabilities={capabilities} />
      </>
    );
  } else if (view === "thread" || inThread) {
    const recent = inThread
      ? (threadRecentRuns || [])
      : (participants?.recent_runs || []);
    main = (
      <>
        {workbenchSec}
        {recent.length > 0 && (
          <Section label="Background Tasks">
            {recent.slice(0, 4).map((r) => (
              <RunCard key={r.id} run={r} />
            ))}
          </Section>
        )}
      </>
    );
    more = (
      <>
        <CapabilitiesSection capabilities={capabilities} />
      </>
    );
  } else if (view === "agent") {
    main = (
      <>
        {workbenchSec}
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
        <CapabilitiesSection capabilities={capabilities} />
      </>
    );
  } else {
    main = <>{workbenchSec}</>;
    more = (
      <>
        <CapabilitiesSection capabilities={capabilities} />
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
            className="mt-2 flex w-full items-center justify-between border-y border-border py-2.5 text-[11.5px] font-semibold uppercase tracking-[0.4px] text-text-muted hover:text-text"
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

function Section({ label, children }: { label: string; children: React.ReactNode }) {
  return (
    <section className="mb-6">
      <div className="mb-2 font-display text-[10px] font-black uppercase tracking-[1px] text-text-muted">
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
      <div className="mb-2 grid grid-cols-3 border border-border bg-panel text-[11px]">
        <div className="border-r border-border px-2 py-1.5">
          <div className="font-mono uppercase text-text-faint">Mode</div>
          <div className="truncate text-text">{permission.label}</div>
        </div>
        <div className="border-r border-border px-2 py-1.5">
          <div className="font-mono uppercase text-text-faint">Skills</div>
          <div className={summary.missing > 0 ? "text-error" : "text-text"}>
            {summary.ready}/{summary.total} ready
          </div>
        </div>
        <div className="px-2 py-1.5">
          <div className="font-mono uppercase text-text-faint">Failures</div>
          <div className={failures.length > 0 ? "text-error" : "text-text"}>{failures.length || "none"}</div>
        </div>
      </div>
      <div className="flex flex-col gap-2">
        {panelAgents.map((p) => {
          const display = p.display || p.id;
          const runtime = agentRuntimeSummary(p.runtime, p.model, stateRuntime);
          const isRunning = runningIDs.has(p.id) || p.status === "running";
          const hasDM = agentDMs.some((d) => d.persona_id === p.id);
          const agentTools = p.tools && p.tools.length > 0 ? p.tools : globalTools;
          return (
            <div key={p.id} className="border border-border bg-panel px-2.5 py-2">
              <div className="flex min-w-0 items-center gap-2">
                <Dot status={isRunning ? "running" : "idle"} />
                <span className="min-w-0 flex-1 truncate text-[12.5px] font-semibold text-text">
                  @{display}
                </span>
                <span className="shrink-0 font-mono text-[10.5px] uppercase text-text-faint">
                  {isRunning ? "working" : hasDM ? "dm" : "ready"}
                </span>
              </div>
              <div className="mt-1 truncate font-mono text-[10.5px] text-text-muted">
                {runtime || runtimeLabel(undefined, stateRuntime)}
              </div>
              {p.description && (
                <div className="mt-1 line-clamp-2 text-[11.5px] leading-[1.4] text-text-muted">
                  {p.description}
                </div>
              )}
              <div className="mt-2 flex flex-wrap gap-1">
                <CapabilityPill label={permission.short} />
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
        <div className="border border-border bg-panel px-2.5 py-2 text-[12px] text-text-muted">
          No agent is attached to this scope. Mention an agent or open an Agent DM to start.
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
  if (view === "thread" || view === "channel") {
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
    .filter((r) => failureStatus(r.status))
    .map((r) => `${r.title || r.agent_id} · ${r.status}`);
  const failedTasks = (capabilities?.tasks || [])
    .filter((t) => failureStatus(t.run_status || t.status))
    .map((t) => `${t.title} · ${t.run_status || t.status}`);
  return [...failedRuns, ...failedTasks].slice(0, 3);
}

function failureStatus(status: string | undefined): boolean {
  const s = (status || "").toLowerCase();
  return s === "failed" || s === "error" || s === "canceled" || s === "rollback_failed" || s === "no_output";
}

function CapabilityPill({ label, error }: { label: string; error?: boolean }) {
  return (
    <span className={cn(
      "border px-1.5 py-px font-mono text-[10.5px]",
      error ? "border-error/50 text-error" : "border-border text-text-faint",
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

function RunCard({ run }: { run: AgentRun }) {
  const agents = useStore((s) => s.agents);
  const streaming = useStore((s) => s.streaming);
  const streamingByID = useStore((s) => s.streamingByID);
  const stop = useStore((s) => s.stop);
  const expandedTaskID = useStore((s) => s.expandedTaskID);
  const collapseTaskInRail = useStore((s) => s.collapseTaskInRail);
  const ag = agents.find((a) => a.id === run.agent_id);
  const [tick, setTick] = useState(0);
  const [expanded, setExpanded] = useState(false);
  const [detail, setDetail] = useState<RunDetail | null>(null);
  const [detailErr, setDetailErr] = useState<string | null>(null);
  const [detailLoading, setDetailLoading] = useState(false);

  const externallyExpanded = expandedTaskID === run.id;
  const effectivelyExpanded = expanded || externallyExpanded;

  useEffect(() => {
    if (run.status !== "running") return;
    const id = setInterval(() => setTick((n) => n + 1), 250);
    return () => clearInterval(id);
  }, [run.status]);

  useEffect(() => {
    if (!effectivelyExpanded || run.status === "running") return;
    let cancelled = false;
    setDetailLoading(true);
    setDetailErr(null);
    api
      .run(run.id)
      .then((d) => {
        if (cancelled) return;
        if (!d.task_id) {
          setDetail(null);
          setDetailErr("Task not found");
        } else {
          setDetail(d);
        }
      })
      .catch((e: Error) => {
        if (!cancelled) setDetailErr(e.message);
      })
      .finally(() => {
        if (!cancelled) setDetailLoading(false);
      });
    return () => {
      cancelled = true;
    };
  }, [effectivelyExpanded, run.id, run.status]);

  const liveStream = Object.values(streamingByID).find((s) => s.messageID === run.id) ||
    (streaming?.messageID === run.id ? streaming : null);
  const startedAt = liveStream ? liveStream.startedAt : run.time;
  const liveElapsed = run.status === "running" ? Date.now() - new Date(startedAt).getTime() : 0;
  const finishedDuration = run.status !== "running" && run.duration_ms ? run.duration_ms : 0;
  const elapsedMs = liveElapsed || finishedDuration;
  const elapsedLabel =
    elapsedMs > 0 ? fmtDur(elapsedMs) : relTime(run.time);

  let currentStep = "";
  if (liveStream) {
    const calls = Array.from(liveStream.toolCalls.values());
    const lastRunning = [...calls].reverse().find((c) => c.status === "running");
    if (lastRunning) currentStep = lastRunning.tool_name || "running";
    else if (liveStream.content) currentStep = "writing reply";
    else if (liveStream.reasoning) currentStep = "reasoning";
    else currentStep = "starting";
  }

  const onStop = (e: React.MouseEvent) => {
    e.stopPropagation();
    void stop();
  };

  void tick;
  const canExpand = run.status !== "running";
  const onCardClick = () => {
    if (!canExpand) return;
    if (externallyExpanded) {
      collapseTaskInRail();
      setExpanded(false);
      return;
    }
    setExpanded((e) => !e);
  };

  return (
    <div
      className={cn(
        "mb-1.5 border-2 border-border bg-panel px-2.5 py-2 transition-colors",
        run.status === "running" && "border-l-[6px] border-l-running",
        (run.status === "done" || run.status === "finished") && "border-l-[6px] border-l-done",
        (run.status === "error" || run.status === "failed") && "border-l-[6px] border-l-error",
        run.status === "no_output" && "border-l-[6px] border-l-text-faint",
        canExpand && "cursor-pointer hover:bg-accent",
      )}
      onClick={onCardClick}
    >
      <div className="flex items-start justify-between gap-2">
        <div
          className="line-clamp-2 break-words text-[12.5px] font-semibold leading-[1.45] text-text"
          title={run.title}
        >
          {run.title}
        </div>
        {run.status === "running" && (
          <Button variant="danger" size="xs" onClick={onStop}>
            <Square className="size-2.5" />
            <span>Stop</span>
          </Button>
        )}
      </div>
      <div className="mt-0.5 font-mono text-[11px] text-text-muted tabular-nums">
        {(ag?.display || run.agent_id) + " · " + (currentStep || statusLabel(run.status)) + " · " + elapsedLabel}
      </div>
      {effectivelyExpanded && canExpand && (
        <RunCardDetail loading={detailLoading} error={detailErr} detail={detail} />
      )}
    </div>
  );
}

function RunCardDetail({
  loading,
  error,
  detail,
}: {
  loading: boolean;
  error: string | null;
  detail: RunDetail | null;
}) {
  if (loading) {
    return <div className="mt-2 text-[11.5px] text-text-faint">Loading…</div>;
  }
  if (error) {
    return <div className="mt-2 text-[11.5px] text-error">{error}</div>;
  }
  if (!detail) {
    return <div className="mt-2 text-[11.5px] text-text-faint">No detail.</div>;
  }
  const isEmpty = detail.status === "no_output";
  const hasSteps = (detail.key_steps?.length ?? 0) > 0;
  return (
    <div className="mt-2 flex flex-col gap-1.5 border-t border-border pt-2">
      {detail.outcome && (
        <div className="text-[11.5px] text-text leading-[1.45] break-words">
          {detail.outcome}
        </div>
      )}
      {!isEmpty && detail.result_message_id && (
        <div className="text-[11px] text-text-faint">
          Reply: <span className="font-mono">{detail.result_message_id.slice(0, 8)}</span>
        </div>
      )}
      {hasSteps ? (
        <ul className="flex flex-col gap-1 mt-0.5">
          {detail.key_steps!.map((s, i) => (
            <li
              key={i}
              className="text-[11.5px] text-text-muted leading-[1.4] flex gap-1.5"
            >
              <span className="text-text-faint shrink-0">{stepKindLabel(s.kind)}</span>
              <span className="break-words">{s.title}</span>
            </li>
          ))}
        </ul>
      ) : (
        <div className="text-[11px] text-text-faint">No recorded steps.</div>
      )}
    </div>
  );
}

function stepKindLabel(kind: string): string {
  switch (kind) {
    case "read":
      return "·";
    case "write":
      return "·";
    case "run":
      return "·";
    case "subtask":
      return "·";
    case "summary":
      return "·";
    case "error":
      return "!";
    default:
      return "·";
  }
}

function statusLabel(status: string): string {
  switch (status) {
    case "no_output":
      return "Finished with no output";
    case "finished":
      return "Finished";
    case "failed":
      return "Failed";
    case "canceled":
      return "Canceled";
    case "queued":
      return "Queued";
    case "running":
      return "Running";
    default:
      return status;
  }
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

function CapabilitiesSection({ capabilities }: { capabilities: CapabilityView | null }) {
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
      return capabilities.skills[0]?.name || null;
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
  const tasks = capabilities.tasks.slice(0, 3);
  const proposals = capabilities.action_proposals.slice(0, 3);
  const empty = skills.length === 0 && tasks.length === 0 && proposals.length === 0;
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
                    onClick={() => setSelectedSkill(s.name)}
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
              {selected && (
                <CapabilityCard>
                  <div className="flex items-center justify-between gap-2">
                    <span className="font-mono text-[10.5px] uppercase tracking-[0.6px] text-text-faint">Skill detail</span>
                    {selected.last_action && <span className="text-[11px] text-text-faint">last {selected.last_action}</span>}
                  </div>
                  <div className="mt-1.5 font-semibold text-text">{selected.name}</div>
                  {(selected.when || selected.description) && (
                    <div className="mt-1 text-[11.5px] leading-[1.45] text-text-muted">
                      {selected.when || selected.description}
                    </div>
                  )}
                  {selected.risk && (
                    <div className="mt-1 font-mono text-[10.5px] uppercase tracking-[0.5px] text-text-faint">
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
            <CapabilityGroup label="Task State">
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
      <div className="mb-1.5 font-mono text-[10.5px] uppercase tracking-[0.6px] text-text-faint">
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

function fmtDur(ms: number): string {
  if (ms < 1000) return ms + "ms";
  const totalSec = Math.round(ms / 1000);
  if (totalSec < 60) return Math.round(ms / 100) / 10 + "s";
  const m = Math.floor(totalSec / 60);
  const s = totalSec % 60;
  if (m < 60) return s ? m + "m " + s + "s" : m + "m";
  const h = Math.floor(m / 60);
  const mm = m % 60;
  return mm ? h + "h " + mm + "m" : h + "h";
}
