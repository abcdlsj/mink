import { useEffect, useState } from "react";
import { ChevronRight, Square } from "lucide-react";
import { useStore } from "@/lib/store";
import { Identicon } from "@/components/Identicon";
import { Button } from "@/components/ui/button";
import { Dot } from "./LeftPane";
import { api } from "@/lib/api";
import { cn, relTime } from "@/lib/utils";
import type { AgentItem, AgentRun, RunDetail, ThreadItem } from "@/lib/types";

export function RightPane() {
  const view = useStore((s) => s.view);
  const detail = useStore((s) => s.detail);
  const threadDetail = useStore((s) => s.threadDetail);
  const channels = useStore((s) => s.channels);
  const threads = useStore((s) => s.threads);
  const agents = useStore((s) => s.agents);
  const activeChannel = useStore((s) => s.activeChannel);
  const activeAgent = useStore((s) => s.activeAgent);
  const participants = useStore((s) => s.participants);
  const tools = useStore((s) => s.tools);
  const streaming = useStore((s) => s.streaming);
  const [moreOpen, setMoreOpen] = useState(true);

  const inThread = !!threadDetail && !threadDetail.unsupported && !threadDetail.not_found;
  const threadParticipants = inThread ? threadDetail!.participants : null;
  const threadRecentRuns = inThread ? threadDetail!.recent_runs : null;

  if (!detail && !inThread) return <aside className="h-full border-l-hard border-border bg-panel-3 px-3 py-4" />;

  const runtimeRuns: AgentRun[] = streaming
    ? [
        {
          id: streaming.messageID,
          agent_id: agents[0]?.id || "agent",
          title: "Current turn",
          status: "running",
          time: streaming.startedAt,
        },
      ]
    : participants?.active_runs || [];

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
  const runlogSec = (
    <Section label="Runlog">
      <a className="text-[12.5px] text-text-muted hover:text-text hover:underline" href="#">
        Open timeline
      </a>
    </Section>
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
        <Section label="Participants">
          {participantsList.length > 0 ? (
            <ParticipantsRow agents={participantsList} />
          ) : (
            <div className="text-[12px] text-text-faint leading-[1.4]">
              No collaborators yet. Mention an agent to invite them.
            </div>
          )}
        </Section>
        {runtimeRuns.length > 0 && (
          <Section label="Active Runs">
            {runtimeRuns.map((r) => (
              <RunCard key={r.id} run={r} />
            ))}
          </Section>
        )}
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
        {toolsSec}
        {runlogSec}
      </>
    );
  } else if (view === "thread" || inThread) {
    const item = detail?.item;
    const participantsList = inThread
      ? (threadParticipants || [])
      : (participants?.agents || []);
    const recent = inThread
      ? (threadRecentRuns || [])
      : (participants?.recent_runs || []);
    const status = item?.running ? "running" : (inThread ? "thread open" : "open");
    main = (
      <>
        <Section label="Status">
          <div className="text-[13px] text-text">{status}</div>
        </Section>
        {runtimeRuns.length > 0 && (
          <Section label="Active Run">
            {runtimeRuns.map((r) => (
              <RunCard key={r.id} run={r} />
            ))}
          </Section>
        )}
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
        {toolsSec}
        {runlogSec}
      </>
    );
  } else if (view === "agent") {
    const ag = agents.find((a) => a.id === activeAgent);
    const agentRunning = !!streaming || (detail?.item.running ?? false) || ag?.status === "running";
    main = (
      <>
        <Section label="Status">
          <div className="text-[13px] text-text">{agentRunning ? "running" : "idle"}</div>
        </Section>
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
        {runtimeRuns.length > 0 && (
          <Section label="Active Run">
            {runtimeRuns.map((r) => (
              <RunCard key={r.id} run={r} />
            ))}
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
            title={p.display + " · " + (p.status === "running" ? "running" : "available")}
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

function RunCard({ run }: { run: AgentRun }) {
  const agents = useStore((s) => s.agents);
  const streaming = useStore((s) => s.streaming);
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

  const isCurrent = streaming?.messageID === run.id;
  const startedAt = isCurrent && streaming ? streaming.startedAt : run.time;
  const liveElapsed = run.status === "running" ? Date.now() - new Date(startedAt).getTime() : 0;
  const finishedDuration = run.status !== "running" && run.duration_ms ? run.duration_ms : 0;
  const elapsedMs = liveElapsed || finishedDuration;
  const elapsedLabel =
    elapsedMs > 0 ? fmtDur(elapsedMs) : relTime(run.time);

  let currentStep = "";
  if (isCurrent && streaming) {
    const calls = Array.from(streaming.toolCalls.values());
    const lastRunning = [...calls].reverse().find((c) => c.status === "running");
    if (lastRunning) currentStep = lastRunning.tool_name || "running";
    else if (streaming.content) currentStep = "writing reply";
    else if (streaming.reasoning) currentStep = "reasoning";
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
