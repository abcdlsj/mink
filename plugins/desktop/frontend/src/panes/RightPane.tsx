import { useEffect, useState } from "react";
import { ChevronRight, Square } from "lucide-react";
import { useStore } from "@/lib/store";
import { Identicon } from "@/components/Identicon";
import { Button } from "@/components/ui/button";
import { Dot } from "./LeftPane";
import { cn, relTime } from "@/lib/utils";
import type { AgentItem, AgentRun, ThreadItem } from "@/lib/types";

export function RightPane() {
  const view = useStore((s) => s.view);
  const detail = useStore((s) => s.detail);
  const channels = useStore((s) => s.channels);
  const threads = useStore((s) => s.threads);
  const agents = useStore((s) => s.agents);
  const activeChannel = useStore((s) => s.activeChannel);
  const activeAgent = useStore((s) => s.activeAgent);
  const participants = useStore((s) => s.participants);
  const state = useStore((s) => s.state);
  const tools = useStore((s) => s.tools);
  const streaming = useStore((s) => s.streaming);
  const [moreOpen, setMoreOpen] = useState(false);

  if (!detail) return <aside className="h-full border-l border-border bg-panel-3 px-4 py-4" />;

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

  const modelSec = (
    <Section label="Current Model">
      <div className="text-[13px] text-text">{state?.model || "—"}</div>
      <div className="text-[11px] text-text-faint mt-0.5">Used by all new agent runs</div>
    </Section>
  );
  const execSec = (
    <Section label="Execution">
      <div className="text-[13px] text-text">Local</div>
      <div className="text-[11px] text-text-faint mt-0.5">Configured in settings</div>
    </Section>
  );
  const toolsSec = (
    <Section label="Tools">
      <div className="flex flex-wrap gap-1.5">
        {tools.map((t) => (
          <span
            key={t.name}
            className="text-[11.5px] text-text-muted bg-panel border border-border rounded-[3px] px-2 py-px font-mono"
          >
            {t.name}
          </span>
        ))}
      </div>
    </Section>
  );
  const runlogSec = (
    <Section label="Runlog">
      <a className="text-[12px] text-accent hover:underline" href="#">
        Open timeline
      </a>
    </Section>
  );

  if (view === "channel") {
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
        {participantsList.length > 0 && (
          <Section label="Participants">
            <ParticipantsRow agents={participantsList} />
          </Section>
        )}
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
        {modelSec}
        {execSec}
        {toolsSec}
        {runlogSec}
      </>
    );
  } else if (view === "thread") {
    const item = detail.item;
    const participantsList = participants?.agents || [];
    const recent = participants?.recent_runs || [];
    const status = item.running ? "running" : "open";
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
        {participantsList.length > 0 && (
          <Section label="Participants">
            <ParticipantsRow agents={participantsList} />
          </Section>
        )}
        {recent.length > 0 && (
          <Section label="Subtasks">
            {recent.slice(0, 3).map((r) => (
              <RunCard key={r.id} run={r} />
            ))}
          </Section>
        )}
      </>
    );
    more = (
      <>
        {modelSec}
        {execSec}
        {toolsSec}
        {runlogSec}
      </>
    );
  } else if (view === "agent") {
    const ag = agents.find((a) => a.id === activeAgent);
    const agentRunning = !!streaming || detail.item.running || ag?.status === "running";
    main = (
      <>
        <Section label="Status">
          <div className="text-[13px] text-text">{agentRunning ? "running" : "idle"}</div>
        </Section>
        {ag?.role && (
          <Section label="Role">
            <div className="text-[13px] text-text">{ag.role}</div>
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
        {modelSec}
        {execSec}
        {toolsSec}
      </>
    );
  }

  return (
    <aside className="h-full border-l border-border bg-panel-3 overflow-y-auto px-4 pt-4 pb-6">
      <div>{main}</div>
      {more && (
        <>
          <button
            onClick={() => setMoreOpen((v) => !v)}
            className="mt-1 w-full flex items-center justify-between border-t border-border-soft pt-2 pb-2 text-[11.5px] font-medium text-text-muted hover:text-text"
          >
            <span>{moreOpen ? "Hide details" : "More details"}</span>
            <ChevronRight
              className={cn("size-3 text-text-faint transition-transform", moreOpen && "rotate-90")}
            />
          </button>
          {moreOpen && <div className="pt-3">{more}</div>}
        </>
      )}
    </aside>
  );
}

function Section({ label, children }: { label: string; children: React.ReactNode }) {
  return (
    <div className="mb-5">
      <div className="text-[10.5px] uppercase tracking-[0.7px] text-text-faint mb-1.5 font-semibold">
        {label}
      </div>
      <div>{children}</div>
    </div>
  );
}

function ParticipantsRow({ agents }: { agents: AgentItem[] }) {
  return (
    <div className="flex items-center gap-2.5">
      <div className="inline-flex">
        {agents.slice(0, 3).map((p, i) => (
          <span
            key={p.id}
            className={cn(
              "size-[22px] rounded-[4px] overflow-hidden border-[1.5px] border-panel-3 bg-panel",
              i > 0 && "-ml-1.5",
            )}
            title={p.display + (p.status === "running" ? " · running" : "")}
          >
            <Identicon seed={p.id || p.display} kind="agent" />
          </span>
        ))}
        {agents.length > 3 && (
          <span className="-ml-1.5 size-[22px] rounded-[4px] inline-flex items-center justify-center border-[1.5px] border-panel-3 bg-panel-2 text-[10.5px] text-text-muted">
            +{agents.length - 3}
          </span>
        )}
      </div>
      <span className="text-[12px] text-text-muted">
        {agents.length} participant{agents.length === 1 ? "" : "s"}
      </span>
    </div>
  );
}

function RunCard({ run }: { run: AgentRun }) {
  const agents = useStore((s) => s.agents);
  const streaming = useStore((s) => s.streaming);
  const stop = useStore((s) => s.stop);
  const ag = agents.find((a) => a.id === run.agent_id);
  const [tick, setTick] = useState(0);

  useEffect(() => {
    if (run.status !== "running") return;
    const id = setInterval(() => setTick((n) => n + 1), 250);
    return () => clearInterval(id);
  }, [run.status]);

  const isCurrent = streaming?.messageID === run.id;
  const startedAt = isCurrent && streaming ? streaming.startedAt : run.time;
  const elapsedMs = run.status === "running" ? Date.now() - new Date(startedAt).getTime() : 0;
  const elapsedLabel =
    elapsedMs > 0 ? Math.round(elapsedMs / 100) / 10 + "s" : relTime(run.time);

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

  return (
    <div
      className={cn(
        "border border-border-soft rounded-md bg-panel px-2.5 py-2 mb-1.5 transition-colors",
        run.status === "running" && "border-l-2 border-l-running",
        run.status === "done" && "border-l-2 border-l-done",
        run.status === "error" && "border-l-2 border-l-error",
      )}
    >
      <div className="flex items-center justify-between gap-2">
        <div className="text-[12.5px] text-text">{run.title}</div>
        {run.status === "running" && (
          <Button variant="danger" size="xs" onClick={onStop}>
            <Square className="size-2.5" />
            <span>Stop</span>
          </Button>
        )}
      </div>
      <div className="text-[11px] text-text-faint mt-0.5 tabular-nums">
        {(ag?.display || run.agent_id) + " · " + (currentStep || run.status) + " · " + elapsedLabel}
      </div>
    </div>
  );
}

function ThreadMiniCard({ thread, showChannel }: { thread: ThreadItem; showChannel: boolean }) {
  const channels = useStore((s) => s.channels);
  const openThread = useStore((s) => s.openThread);
  const ch = channels.find((c) => c.id === thread.channel_id);
  return (
    <button
      onClick={() => void openThread(thread.id)}
      className="w-full text-left px-2 py-1.5 rounded-sm hover:bg-panel-2 cursor-pointer transition-colors"
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
