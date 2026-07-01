import { useMemo, useState } from "react";
import { AtSign, ChevronRight, MessageCircle, Plus } from "lucide-react";
import { Identicon } from "@/components/Identicon";
import { Button } from "@/components/ui/button";
import { useStore } from "@/lib/store";
import { cn, relTime } from "@/lib/utils";
import type { ChannelItem, TaskStateCard } from "@/lib/types";
import { shortenText, statusLabel } from "@/lib/task-helpers";
import { MemoryOverviewCard } from "./MemoryOverviewCard";
import { recentThreadsForAgent, reliabilitySummary, scopeSummary, tasksForAgent } from "./agent-profile-model";

export function AgentDetailPane() {
  const agentID = useStore((s) => s.activeAgentID);
  const agents = useStore((s) => s.agents);
  const personas = useStore((s) => s.personas);
  const channels = useStore((s) => s.channels);
  const threads = useStore((s) => s.threads);
  const directChats = useStore((s) => s.directChats);
  const agentDMs = useStore((s) => s.agentDMs);
  const capabilities = useStore((s) => s.capabilities);
  const openAgent = useStore((s) => s.openAgent);
  const newAgentChat = useStore((s) => s.newAgentChat);
  const openChannel = useStore((s) => s.openChannel);
  const openThread = useStore((s) => s.openThread);
  const agent = agents.find((a) => a.id === agentID);
  const persona = personas.find((p) => p.id === agentID);
  const [configOpen, setConfigOpen] = useState(false);
  const stableAgentID = agentID || "";
  const display = persona?.display || agent?.display || stableAgentID;
  const defaultDM = directChats.find((d) => d.kind === "agent_dm" && d.persona_id === stableAgentID);
  const namedChats = agentDMs.filter((d) => d.persona_id === stableAgentID);
  const runtime = persona?.runtime || agent?.runtime || "default";
  const model = persona?.model || agent?.model || "";
  const caps = persona?.capabilities || [];
  const tools = persona?.tools || [];
  const tasks = capabilities?.tasks || [];
  const proposals = capabilities?.action_proposals || [];
  const scopedTasks = useMemo(
    () => tasksForAgent(tasks, stableAgentID),
    [tasks, stableAgentID],
  );
  const reliability = useMemo(
    () => reliabilitySummary(scopedTasks, proposals, stableAgentID, persona?.task_policy || "default"),
    [scopedTasks, proposals, stableAgentID, persona?.task_policy],
  );
  const scope = scopeSummary(!!defaultDM, caps, namedChats);
  const recentThreads = useMemo(
    () => recentThreadsForAgent(scopedTasks, threads, channels),
    [scopedTasks, threads, channels],
  );
  if (!stableAgentID || (!agent && !persona)) {
    return (
      <main className="h-full min-w-0 bg-panel px-6 py-6 text-[13px] leading-[20px] text-text-muted">
        <div className="font-semibold text-text">Agent not found.</div>
        <div>Pick another agent from Direct Messages.</div>
      </main>
    );
  }
  return (
    <main className="h-full min-w-0 overflow-y-auto bg-panel px-5 py-5">
      <div className="mx-auto max-w-[980px]">
        <section className="border-b-hard border-border pb-5">
          <div className="flex flex-wrap items-start justify-between gap-4">
            <div className="flex min-w-0 items-start gap-4">
              <div className="size-16 shrink-0 overflow-hidden border border-agent-border bg-agent-bg">
                <Identicon seed={stableAgentID} kind="agent" />
              </div>
              <div className="min-w-0">
                <div className="flex flex-wrap items-center gap-2">
                  <div className="flex items-center gap-2 font-display text-[26px] font-extrabold text-text">
                    <AtSign className="size-5 text-text-muted" />
                    <span className="truncate">{display}</span>
                  </div>
                  <StatusPill status={agent?.status || "idle"} />
                  <span className="border border-border-soft bg-panel-2 px-1.5 py-px font-mono text-[10.5px] font-semibold uppercase text-text">
                    Agent Profile
                  </span>
                </div>
                <div className="mt-1 font-mono text-[11px] text-text-faint">{stableAgentID}</div>
                {(persona?.description || agent?.role) && (
                  <div className="mt-3 max-w-[720px] text-[14px] leading-[22px] text-text-muted">
                    {persona?.description || agent?.role}
                  </div>
                )}
                <div className="mt-3 grid gap-2 md:grid-cols-3">
                  <SummaryCell label="Role" value={shortenText(persona?.description || agent?.role || "General agent", 72)} />
                  <SummaryCell label="Scope" value={scope} />
                  <SummaryCell label="Current status" value={statusLabel(agent?.status || "idle")} />
                </div>
              </div>
            </div>
            <div className="flex gap-2">
              <Button variant="default" onClick={() => void openAgent(defaultDM?.id || stableAgentID)}>
                <MessageCircle className="size-3" />
                <span>Open default DM</span>
              </Button>
              <Button variant="primary" onClick={() => void newAgentChat(stableAgentID)}>
                <Plus className="size-3" />
                <span>New Agent Chat</span>
              </Button>
            </div>
          </div>
        </section>

        <section className="py-5">
          <SectionTitle title="Reliability" detail="Explainable current signals, not a score." />
          <div className="grid gap-3 md:grid-cols-5">
            <MetricCard label="Working" value={String(reliability.doing)} tone={reliability.doing > 0 ? "active" : "idle"} />
            <MetricCard label="In review" value={String(reliability.review)} tone={reliability.review > 0 ? "review" : "idle"} />
            <MetricCard label="Recent failures" value={String(reliability.failed)} tone={reliability.failed > 0 ? "error" : "idle"} />
            <MetricCard label="Pending proposals" value={String(reliability.pendingProposals)} tone={reliability.pendingProposals > 0 ? "review" : "idle"} />
            <MetricCard label="Task policy" value={reliability.taskPolicy} tone="idle" />
          </div>
          <div className="mt-3 flex flex-wrap gap-1.5">
            {reliability.notes.map((note) => (
              <span key={note} className="border border-border-soft bg-panel-2 px-1.5 py-px font-mono text-[10.5px] text-text-muted">
                {note}
              </span>
            ))}
          </div>
        </section>

        <section className="grid gap-4 pb-5 md:grid-cols-2">
          <InfoCard title="Recent Flow Work">
            {scopedTasks.length === 0 ? (
              <EmptyHint text="No active or reviewable task projection for this agent yet." />
            ) : (
              <div className="flex flex-col gap-2">
                {scopedTasks.slice(0, 5).map((task) => (
                  <TaskHistoryCard key={task.id} task={task} channels={channels} />
                ))}
              </div>
            )}
          </InfoCard>

          <InfoCard title="Recent Conversations">
            {namedChats.length === 0 && !defaultDM ? (
              <EmptyHint text="No agent DM history yet." />
            ) : (
              <div className="flex flex-col gap-1.5">
                {defaultDM && (
                  <ChatRow
                    title="Default DM"
                    meta="Default Agent DM"
                    onClick={() => void openAgent(defaultDM.id)}
                  />
                )}
                {namedChats.slice(0, 4).map((chat) => (
                  <ChatRow
                    key={chat.id}
                    title={chat.title || "New chat"}
                    meta={`${chat.message_count} msgs · ${relTime(chat.updated_at)}`}
                    onClick={() => void openAgent(chat.id)}
                  />
                ))}
              </div>
            )}
          </InfoCard>
        </section>

        <section className="grid gap-4 pb-5 md:grid-cols-[minmax(0,1.1fr)_minmax(0,0.9fr)]">
          <InfoCard title="Thread Touchpoints">
            {recentThreads.length === 0 ? (
              <EmptyHint text="No thread-linked task history yet." />
            ) : (
              <div className="flex flex-col gap-1.5">
                {recentThreads.map((thread) => (
                  <button
                    key={thread.threadID + thread.taskID}
                    type="button"
                    onClick={() => void openThreadTouchpoint(openChannel, openThread, thread.channelID, thread.threadID)}
                    className="flex items-center justify-between border border-border bg-panel px-2.5 py-2 text-left text-[12.5px] text-text-muted hover:text-text"
                  >
                    <span className="min-w-0">
                      <span className="block truncate text-text">{thread.title}</span>
                      <span className="block truncate font-mono text-[10.5px] text-text-faint">
                        {thread.channelLabel} · via {thread.taskTitle}
                      </span>
                    </span>
                    <span className="shrink-0 font-mono text-[10.5px] text-text-faint">{relTime(thread.updatedAt)}</span>
                  </button>
                ))}
              </div>
            )}
          </InfoCard>

          <InfoCard title="Memory">
            <MemoryOverviewCard personaID={stableAgentID} />
          </InfoCard>
        </section>

        <section className="border-t border-border-soft py-4">
          <button
            type="button"
            onClick={() => setConfigOpen((v) => !v)}
            className="flex w-full items-center justify-between py-1 text-left"
          >
            <div>
              <div className="font-display text-[11px] font-extrabold uppercase text-text-muted">Config Details</div>
              <div className="mt-1 text-[12px] text-text-faint">Runtime, model, tools, capabilities, and sidebar policy.</div>
            </div>
            <ChevronRight className={cn("size-4 text-text-faint transition-transform", configOpen && "rotate-90")} />
          </button>
          {configOpen && (
            <div className="grid gap-4 pt-4 md:grid-cols-2">
              <InfoCard title="Runtime">
                <InfoLine label="runtime" value={runtime} />
                {model && <InfoLine label="model" value={model} />}
                <InfoLine label="status" value={statusLabel(agent?.status || "idle")} />
              </InfoCard>
              <InfoCard title="Task Policy">
                <InfoLine label="policy" value={persona?.task_policy || "default"} />
                <InfoLine label="memory scopes" value={`persona:${stableAgentID} + workspace + global`} />
                <InfoLine label="sidebar" value={persona?.show_in_sidebar === false ? "hidden" : "visible"} />
              </InfoCard>
              <InfoCard title="Capabilities">
                <PillList items={caps.length ? caps : ["none configured"]} />
              </InfoCard>
              <InfoCard title="Tools">
                <PillList items={tools.length ? tools : ["runtime default"]} />
              </InfoCard>
            </div>
          )}
        </section>
      </div>
    </main>
  );
}

function SectionTitle({ title, detail }: { title: string; detail?: string }) {
  return (
    <div className="mb-3">
      <div className="font-display text-[11px] font-extrabold uppercase text-text">{title}</div>
      {detail && <div className="mt-1 text-[12px] text-text-faint">{detail}</div>}
    </div>
  );
}

function SummaryCell({ label, value }: { label: string; value: string }) {
  return (
    <div className="border border-border bg-panel-2 px-2.5 py-2">
      <div className="font-mono text-[10.5px] uppercase text-text-muted">{label}</div>
      <div className="mt-1 text-[12.5px] leading-[1.4] text-text">{value}</div>
    </div>
  );
}

function StatusPill({ status }: { status: string }) {
  const normalized = (status || "idle").toLowerCase();
  const tone = normalized === "running" || normalized === "working" || normalized === "queued"
    ? "border-running-border bg-running-bg text-running"
    : "border-border-soft bg-panel-2 text-text-muted";
  return (
    <span className={cn("border px-1.5 py-px font-mono text-[10.5px] font-semibold uppercase", tone)}>
      {statusLabel(status)}
    </span>
  );
}

function MetricCard({
  label,
  value,
  tone,
}: {
  label: string;
  value: string;
  tone: "active" | "review" | "error" | "idle";
}) {
  const toneClass = tone === "active"
    ? "border-running-border"
    : tone === "review"
      ? "border-action-border"
      : tone === "error"
        ? "border-error-border"
        : "border-border";
  return (
    <div className={cn("border bg-panel-2 px-3 py-2.5", toneClass)}>
      <div className="font-mono text-[10.5px] uppercase text-text-muted">{label}</div>
      <div className="mt-1 text-[18px] font-semibold text-text">{value}</div>
    </div>
  );
}

function EmptyHint({ text }: { text: string }) {
  return <div className="text-[12.5px] leading-[19px] text-text-faint">{text}</div>;
}

function ChatRow({ title, meta, onClick }: { title: string; meta: string; onClick: () => void }) {
  return (
    <button
      type="button"
      onClick={onClick}
      className="flex items-center justify-between border border-border bg-panel px-2.5 py-2 text-left text-[12.5px] text-text-muted hover:text-text"
    >
      <span className="truncate">{title}</span>
      <span className="shrink-0 font-mono text-[10.5px] text-text-faint">{meta}</span>
    </button>
  );
}

function TaskHistoryCard({ task, channels }: { task: TaskStateCard; channels: ChannelItem[] }) {
  const channel = channels.find((item) => item.id === task.space_id);
  const location = channel
    ? task.parent_message_id ? `#${channel.name} · thread` : `#${channel.name}`
    : task.space_id || "workspace";
  return (
    <div className="border border-border bg-panel px-2.5 py-2">
      <div className="flex items-start justify-between gap-2">
        <div className="min-w-0">
          <div className="line-clamp-2 text-[12.5px] font-semibold leading-[1.35] text-text">
            {task.title || "Untitled task"}
          </div>
          <div className="mt-1 flex flex-wrap gap-x-1.5 gap-y-0.5 font-mono text-[10.5px] text-text-faint">
            <span>{statusLabel(task.run_status || task.status)}</span>
            <span>· {location}</span>
            {task.latest_run && <span>· run linked</span>}
          </div>
        </div>
        <span className="shrink-0 font-mono text-[10.5px] text-text-faint">{relTime(task.updated_at)}</span>
      </div>
      {task.acceptance_criteria && (
        <div className="mt-1 line-clamp-2 text-[11px] leading-[1.35] text-text-muted">
          {task.acceptance_criteria}
        </div>
      )}
    </div>
  );
}

async function openThreadTouchpoint(
  openChannel: (id: string) => Promise<void>,
  openThread: (id: string) => Promise<void>,
  channelID: string,
  threadID: string,
) {
  if (!threadID) return;
  if (channelID) await openChannel(channelID);
  await openThread(threadID);
}

function InfoCard({ title, children }: { title: string; children: React.ReactNode }) {
  return (
    <section className="border border-border bg-panel-2 px-3 py-3">
      <div className="mb-2 font-display text-[11px] font-extrabold uppercase text-text">{title}</div>
      {children}
    </section>
  );
}

function InfoLine({ label, value }: { label: string; value: string }) {
  return (
    <div className="flex items-center justify-between gap-3 border-b border-border-soft py-1.5 last:border-b-0">
      <span className="font-mono text-[10.5px] font-medium uppercase text-text-muted">{label}</span>
      <span className="truncate text-[12.5px] text-text">{value}</span>
    </div>
  );
}

function PillList({ items }: { items: string[] }) {
  return (
    <div className="flex flex-wrap gap-1.5">
      {items.map((item) => (
        <span key={item} className="border border-border bg-panel px-1.5 py-px font-mono text-[10.5px] font-medium text-text">
          {item}
        </span>
      ))}
    </div>
  );
}
