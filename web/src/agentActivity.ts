import { useEffect, useState } from "react";
import type { Agent } from "./api/client";

export function activityLabel(status?: Agent["activity_status"]): string {
  return status ? status.charAt(0).toUpperCase() + status.slice(1) : "Agent";
}

export type AgentActivityKind =
  | "message.send"
  | "task.create"
  | "task.update"
  | "task.link_thread"
  | "task.unlink_thread"
  | "task.submit_review"
  | "task.done"
  | "task.close"
  | "channel.create"
  | "agent.create"
  | "inbox.ack"
  | "inbox.defer"
  | "run.yield";

export interface AgentActivityItem {
  eventId: string;
  agentMemberId: string;
  kind: AgentActivityKind;
  occurredAt: string;
  runId?: string;
  channelId?: string;
  threadId?: string;
  messageId?: string;
  taskId?: string;
  itemId?: string;
  targetMemberId?: string;
}

export interface AgentActivityEvent {
  event_id: string;
  occurred_at: string;
  data: {
    member_id: string;
    kind: AgentActivityKind;
    run_id?: string;
    channel_id?: string;
    thread_id?: string;
    message_id?: string;
    task_id?: string;
    item_id?: string;
    target_member_id?: string;
  };
}

const MAX_ITEMS_PER_AGENT = 50;
const listeners = new Set<() => void>();
const activityByAgent = new Map<string, AgentActivityItem[]>();

function dedupeKey(item: AgentActivityItem): string {
  const resource =
    item.messageId ??
    item.taskId ??
    item.channelId ??
    item.itemId ??
    item.targetMemberId ??
    item.runId ??
    item.eventId;
  return `${item.kind}:${resource}`;
}

function notify() {
  for (const listener of listeners) listener();
}

/** Records one `agent.activity` event. Replays of the same action dedupe on the action kind plus
 *  its primary resource, so reconnects and idempotent retries do not duplicate feed rows. */
export function recordAgentActivity(event: AgentActivityEvent): void {
  const data = event.data;
  const item: AgentActivityItem = {
    eventId: event.event_id,
    agentMemberId: data.member_id,
    kind: data.kind,
    occurredAt: event.occurred_at,
    runId: data.run_id,
    channelId: data.channel_id,
    threadId: data.thread_id,
    messageId: data.message_id,
    taskId: data.task_id,
    itemId: data.item_id,
    targetMemberId: data.target_member_id,
  };
  const current = activityByAgent.get(item.agentMemberId) ?? [];
  const key = dedupeKey(item);
  if (current.some((existing) => dedupeKey(existing) === key)) return;
  activityByAgent.set(item.agentMemberId, [item, ...current].slice(0, MAX_ITEMS_PER_AGENT));
  notify();
}

export function activityForAgent(agentMemberId: string): readonly AgentActivityItem[] {
  return activityByAgent.get(agentMemberId) ?? [];
}

export function clearAgentActivity(): void {
  activityByAgent.clear();
  notify();
}

export function useAgentActivity(agentMemberId: string): readonly AgentActivityItem[] {
  const [items, setItems] = useState<readonly AgentActivityItem[]>(() =>
    activityForAgent(agentMemberId),
  );
  useEffect(() => {
    const listener = () => setItems(activityForAgent(agentMemberId));
    listener();
    listeners.add(listener);
    return () => {
      listeners.delete(listener);
    };
  }, [agentMemberId]);
  return items;
}
