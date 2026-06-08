import { Fragment, useEffect, useState } from "react";
import { ChevronRight, Square } from "lucide-react";
import { useStore } from "@/lib/store";
import { Identicon } from "@/components/Identicon";
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
  const activePersona = personaForRuntime(activeAgent, personas, agentDMs);

  let main: React.ReactNode = null;
  let more: React.ReactNode = null;

  const toolsSec = (
    <Section label="Tools">
      {tools.length > 0 ? (
        <div className="flex flex-wrap gap-2">
          {tools.map((t) => (
            <span
              key={t.name}
              className="border border-border bg-panel px-2 py-1 font-mono text-[11.5px] text-text-muted"
            >
              {t.name}
            </span>
          ))}
        </div>
      ) : (
        <div className="text-[12.5px] leading-[1.6] text-text-muted">No tools enabled in this channel.</div>
      )}
    </Section>
  );
  const runtimeSec = (
    <Section label="Runtime">
      <RuntimeDetail
        view={view}
        stateRuntime={state?.runtime}
        activePersona={activePersona}
        runs={runtimeRuns}
        personas={personas}
        agentDMs={agentDMs}
        agents={agents}
      />
    </Section>
  );
  const agentDirectorySec = (
    <AgentDirectory
      personas={personas}
      agents={agents}
      agentDMs={agentDMs}
      stateRuntime={state?.runtime}
    />
  );

  if (view === "channel" && !inThread) {
    const ch = channels.find((c) => c.id === activeChannel);
    const participantsList = participants?.agents || [];
    const channelThreads = threads.filter((t) => t.channel_id === activeChannel).slice(0, 3);
    main = (
      <>
        {ch?.topic && (
          <Section label="Topic">
            <div className="text-[13px] text-text">{ch.topic}</div>
          </Section>
        )}
        {runtimeSec}
        <Section label="Participants">
          {participantsList.length > 0 ? (
            <ParticipantsRow agents={participantsList} />
          ) : (
            <div className="text-[12px] text-text-faint leading-[1.4]">
              No collaborators yet. Mention an agent to invite them.
            </div>
          )}
        </Section>
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
        {agentDirectorySec}
        {toolsSec}
      </>
    );
  } else if (view === "thread" || inThread) {
    const participantsList = inThread
      ? (threadParticipants || [])
      : (participants?.agents || []);
    const recent = inThread
      ? (threadRecentRuns || [])
      : (participants?.recent_runs || []);
    main = (
      <>
        {runtimeSec}
        <Section label="Participants">
          {participantsList.length > 0 ? (
            <ParticipantsRow agents={participantsList} />
          ) : (
            <div className="text-[12px] text-text-faint leading-[1.4]">
              No collaborators yet. Mention an agent to invite them.
            </div>
          )}
        </Section>
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
        {agentDirectorySec}
        {toolsSec}
      </>
    );
  } else if (view === "agent") {
    const ag = agents.find((a) => a.id === activeAgent);
    main = (
      <>
        {runtimeSec}
        {ag?.role && (
          <Section label="Role">
            <div className="text-[12.5px] text-text leading-[1.55]">{firstSentence(ag.role)}</div>
            {personaTools(activeAgent, useStore.getState().personas).length > 0 && (
              <div className="mt-2 flex flex-wrap gap-1">
                {personaTools(activeAgent, useStore.getState().personas).map((t) => (
                  <span
                    key={t}
                    className="border border-border bg-panel-2 px-1.5 py-px font-mono text-[11px] text-text-muted"
                  >
                    {t}
                  </span>
                ))}
              </div>
            )}
          </Section>
        )}
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
        {agentDirectorySec}
        {toolsSec}
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

function RuntimeDetail({
  view,
  stateRuntime,
  activePersona,
  runs,
  personas,
  agentDMs,
  agents,
}: {
  view: string;
  stateRuntime?: string;
  activePersona?: import("@/lib/types").PersonaItem;
  runs: AgentRun[];
  personas: import("@/lib/types").PersonaItem[];
  agentDMs: import("@/lib/types").AgentDMItem[];
  agents: AgentItem[];
}) {
  const stop = useStore((s) => s.stop);
  const running = new Map<string, string>();
  runs.forEach((run) => {
    if (run.status !== "running" || !run.agent_id) return;
    const persona = personaForRuntime(run.agent_id, personas, agentDMs);
    const display = persona?.display || agents.find((a) => a.id === run.agent_id)?.display || run.agent_id;
    running.set(display, agentRuntimeSummary(persona?.runtime, persona?.model, stateRuntime));
  });
  if (running.size > 0) {
    return (
      <div className="flex flex-col gap-1.5 text-[12.5px] leading-[1.45]">
        {Array.from(running, ([display, runtime]) => (
          <div
            key={display}
            className="flex min-w-0 items-center justify-between gap-2"
            title={`@${display}\n${runtime}`}
          >
            <span className="truncate text-text">@{display}</span>
            <span className="ml-auto shrink-0 font-mono text-text-muted">{runtime}</span>
            <Button variant="danger" size="xs" onClick={() => void stop()}>
              <Square className="size-2.5" />
              <span>Stop</span>
            </Button>
          </div>
        ))}
      </div>
    );
  }

  if (view === "agent" && activePersona) {
    const rows: [string, string][] = [
      ["Agent", "@" + (activePersona.display || activePersona.id)],
      ["Runtime", runtimeLabel(activePersona.runtime, stateRuntime)],
    ];
    if (activePersona.model) rows.push(["Model", activePersona.model]);
    return (
      <RuntimeRows rows={rows} />
    );
  }

  return (
    <RuntimeRows
      rows={[
        ["Mode", view === "channel" ? "Per channel agent" : "Per participant"],
        ["Runtime", "Selected per agent"],
      ]}
    />
  );
}

function RuntimeRows({ rows }: { rows: [string, string][] }) {
  return (
    <div className="grid grid-cols-[64px_1fr] gap-y-1 text-[12.5px] leading-[1.55]">
      {rows.map(([label, value]) => (
        <Fragment key={label}>
          <span className="text-text-faint">{label}</span>
          <span className="truncate font-mono text-text">{value}</span>
        </Fragment>
      ))}
    </div>
  );
}

function AgentDirectory({
  personas,
  agents,
  agentDMs,
  stateRuntime,
}: {
  personas: import("@/lib/types").PersonaItem[];
  agents: AgentItem[];
  agentDMs: import("@/lib/types").AgentDMItem[];
  stateRuntime?: string;
}) {
  const openAgent = useStore((s) => s.openAgent);
  const newAgentChat = useStore((s) => s.newAgentChat);
  if (personas.length === 0) return null;
  const statusFor = (id: string) => agents.find((a) => a.id === id)?.status || "idle";
  const hasDM = (id: string) => agentDMs.some((d) => d.persona_id === id);
  return (
    <Section label="Agent Directory">
      <div className="flex flex-col gap-2">
        {personas.map((p) => {
          const display = p.display || p.id;
          const runtime = runtimeLabel(p.runtime, stateRuntime);
          const visible = p.show_in_sidebar !== false;
          const title = [
            "@" + display,
            "runtime: " + runtime,
          ];
          if (p.model) title.push("model: " + p.model);
          title.push("dm shortcut: " + (visible ? "shown" : "hidden"));
          return (
            <div
              key={p.id}
              className="border border-border bg-panel px-2.5 py-2"
              title={title.join("\n")}
            >
              <div className="flex min-w-0 items-center gap-2">
                <Dot status={statusFor(p.id) === "running" ? "running" : "idle"} />
                <span className="min-w-0 flex-1 truncate text-[12.5px] font-semibold text-text">
                  @{display}
                </span>
                <span className="shrink-0 font-mono text-[10.5px] uppercase text-text-faint">
                  {hasDM(p.id) ? "dm" : "ready"}
                </span>
              </div>
              <div className="mt-1 truncate font-mono text-[10.5px] text-text-muted">
                {runtime}{p.model ? " / " + p.model : ""}
              </div>
              <div className="mt-2 flex gap-1.5">
                <Button
                  variant="default"
                  size="sm"
                  className="h-6 px-2 text-[11px]"
                  onClick={() => void openAgent(p.id)}
                >
                  DM
                </Button>
                <Button
                  variant="default"
                  size="sm"
                  className="h-6 px-2 text-[11px]"
                  onClick={() => void newAgentChat(p.id)}
                >
                  Chat
                </Button>
                {!visible && (
                  <span className="ml-auto self-center font-mono text-[10px] text-text-faint">hidden</span>
                )}
              </div>
            </div>
          );
        })}
      </div>
    </Section>
  );
}

function personaForRuntime(
  id: string | null,
  personas: import("@/lib/types").PersonaItem[],
  agentDMs: import("@/lib/types").AgentDMItem[],
): import("@/lib/types").PersonaItem | undefined {
  if (!id) return undefined;
  const dm = agentDMs.find((d) => d.id === id);
  const personaID = dm?.persona_id || id;
  return personas.find((p) => p.id === personaID);
}

function runtimeLabel(runtime: string | undefined, fallback: string | undefined): string {
  return runtime || fallback || "default";
}

function agentRuntimeSummary(runtime: string | undefined, model: string | undefined, fallbackRuntime: string | undefined): string {
  const label = runtimeLabel(runtime, fallbackRuntime);
  return model ? `${label} / ${model}` : label;
}

function ParticipantsRow({ agents }: { agents: AgentItem[] }) {
  const runningCount = agents.filter((a) => a.status === "running").length;
  return (
    <div className="flex items-center gap-2.5">
      <div className="inline-flex">
        {agents.slice(0, 3).map((p, i) => (
          <span
            key={p.id}
            className={cn(
              "relative size-[22px] overflow-hidden border-2 border-border bg-panel",
              i > 0 && "-ml-1.5",
            )}
            title={agentTooltip(p)}
          >
            <Identicon seed={p.id || p.display} kind="agent" />
            {p.status === "running" && (
              <span className="absolute -bottom-0.5 -right-0.5 size-[7px] rounded-full border border-border bg-running" />
            )}
          </span>
        ))}
        {agents.length > 3 && (
          <span className="-ml-1.5 inline-flex size-[22px] items-center justify-center border-2 border-border bg-panel-2 text-[10.5px] text-text-muted">
            +{agents.length - 3}
          </span>
        )}
      </div>
      <span className="text-[12px] text-text-muted">
        {agents.length} participant{agents.length === 1 ? "" : "s"}
        {runningCount > 0 && (
          <span className="text-text"> · {runningCount} running</span>
        )}
      </span>
    </div>
  );
}

function agentTooltip(agent: AgentItem) {
  const status = agent.status === "running" ? "running" : "available";
  const lines = [
    "@" + agent.display,
    "status: " + status,
    "runtime: " + (agent.runtime || "default"),
  ];
  if (agent.model) lines.push("model: " + agent.model);
  return lines.join("\n");
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
  if (!capabilities) {
    return (
      <Section label="Capabilities">
        <div className="text-[12px] text-text-faint">Loading capability state...</div>
      </Section>
    );
  }
  const skills = capabilities.skills.slice(0, 4);
  const tasks = capabilities.tasks.slice(0, 3);
  const proposals = capabilities.action_proposals.slice(0, 3);
  const empty = skills.length === 0 && tasks.length === 0 && proposals.length === 0;
  return (
    <Section label="Capabilities">
      {empty ? (
        <div className="text-[12px] text-text-faint">No capability state recorded.</div>
      ) : (
        <div className="flex flex-col gap-3">
          {skills.length > 0 && (
            <CapabilityGroup label="Skills">
              {skills.map((s) => (
                <CapabilityCard key={s.name}>
                  <div className="flex items-center justify-between gap-2">
                    <span className="truncate font-semibold text-text">{s.name}</span>
                    <span className="shrink-0 border border-border bg-panel-2 px-1.5 py-px font-mono text-[10.5px] text-text-muted">
                      {s.risk || "skill"}
                    </span>
                  </div>
                  {(s.when || s.description) && (
                    <div className="mt-1 line-clamp-2 text-[11.5px] leading-[1.4] text-text-muted">
                      {s.when || s.description}
                    </div>
                  )}
                </CapabilityCard>
              ))}
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

function personaTools(id: string | null, personas: import("@/lib/types").PersonaItem[]): string[] {
  if (!id) return [];
  const p = personas.find((p) => p.id === id);
  return p?.tools || [];
}
