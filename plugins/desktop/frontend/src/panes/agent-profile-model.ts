import type { AgentDMItem, ChannelItem, TaskStateCard, ThreadItem } from "@/lib/types";
import { failureStatus, normalizeCapability, taskColumn } from "@/lib/task-helpers";

export type ReliabilitySummary = {
  doing: number;
  review: number;
  failed: number;
  pendingProposals: number;
  taskPolicy: string;
  notes: string[];
};

export type ThreadTouchpoint = {
  threadID: string;
  taskID: string;
  taskTitle: string;
  title: string;
  updatedAt: string;
  channelID: string;
  channelLabel: string;
};

export function reliabilitySummary(
  tasks: TaskStateCard[],
  proposals: Array<{ assignee?: string; created_by?: string; status?: string }>,
  agentID: string,
  taskPolicy: string,
): ReliabilitySummary {
  const doing = tasks.filter((task) => taskColumn(task.run_status || task.status) === "doing").length;
  const review = tasks.filter((task) => taskColumn(task.status) === "review").length;
  const failed = tasks.filter((task) => failureStatus(task.run_status || task.status)).length;
  const pendingProposals = proposals.filter((proposal) =>
    proposal.status === "prepared" && (proposal.assignee === agentID || proposal.created_by === agentID),
  ).length;
  const notes = [
    doing > 0 ? `${doing} active work item${doing === 1 ? "" : "s"}` : "no active work",
    review > 0 ? `${review} waiting for review` : "no review queue",
    failed > 0 ? `${failed} recent failure signal${failed === 1 ? "" : "s"}` : "no recent failure signal",
  ];
  return { doing, review, failed, pendingProposals, taskPolicy, notes };
}

export function tasksForAgent(tasks: TaskStateCard[], agentID: string): TaskStateCard[] {
  return tasks
    .filter((task) => task.worker_id === agentID || task.assignee_id === agentID)
    .sort((a, b) => new Date(b.run_started || b.updated_at).getTime() - new Date(a.run_started || a.updated_at).getTime());
}

export function recentThreadsForAgent(tasks: TaskStateCard[], threads: ThreadItem[], channels: ChannelItem[]): ThreadTouchpoint[] {
  const seen = new Set<string>();
  return tasks
    .filter((task) => task.source_thread_id)
    .map((task) => {
      const threadID = task.source_thread_id || "";
      const thread = threads.find((item) => item.id === threadID);
      const channel = channels.find((item) => item.id === task.space_id);
      return {
        threadID,
        taskID: task.id,
        taskTitle: task.title || "task",
        title: thread?.title || task.source_thread || "Thread",
        updatedAt: task.updated_at,
        channelID: channel?.id || "",
        channelLabel: channel ? `#${channel.name}` : (task.space_id || "workspace"),
      };
    })
    .filter((item) => {
      if (!item.threadID || seen.has(item.threadID)) return false;
      seen.add(item.threadID);
      return true;
    })
    .slice(0, 4);
}

export function scopeSummary(hasDefaultDM: boolean, caps: string[], namedChats: AgentDMItem[]): string {
  const parts = [];
  if (hasDefaultDM || namedChats.length > 0) parts.push("DMs");
  if (caps.some((cap) => normalizeCapability(cap) === "task.execute")) parts.push("task execution");
  if (caps.some((cap) => normalizeCapability(cap) === "task.assign")) parts.push("task assignment");
  parts.push("workspace memory");
  return parts.join(" · ");
}
