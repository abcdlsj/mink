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
  | "channel.leave"
  | "agent.create"
  | "inbox.ack"
  | "inbox.defer"
  | "run.yield";

export interface AgentActivityArgument {
  name: string;
  value: string;
}

export interface AgentActivityItem {
  eventId: string;
  agentMemberId: string;
  kind: AgentActivityKind;
  occurredAt: string;
  runId?: string;
  channelId?: string;
  scopeChannelId?: string;
  threadId?: string;
  messageId?: string;
  taskId?: string;
  itemId?: string;
  targetMemberId?: string;
  arguments: readonly AgentActivityArgument[];
  messagePreview?: string;
  messageTruncated: boolean;
}

export interface AgentActivityEvent {
  event_id: string;
  occurred_at: string;
  data: {
    member_id: string;
    kind: AgentActivityKind;
    run_id?: string;
    channel_id?: string;
    scope_channel_id?: string;
    thread_id?: string;
    message_id?: string;
    task_id?: string;
    item_id?: string;
    target_member_id?: string;
    arguments?: AgentActivityArgument[];
    message_preview?: string;
    message_truncated?: boolean;
  };
}

const MAX_ITEMS_PER_AGENT = 50;
const UUID_PATTERN = /\b[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}\b/gi;
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
  // The server can emit a fresh envelope when an idempotent request is retried.
  // Include semantic details so those retries collapse while two updates to the
  // same resource with different inputs remain visible as separate actions.
  if (item.arguments.length > 0 || item.messagePreview !== undefined) {
    return `${item.kind}:${resource}:${JSON.stringify([
      item.arguments,
      item.messagePreview,
      item.messageTruncated,
    ])}`;
  }
  return `${item.kind}:${resource}`;
}

function notify() {
  for (const listener of listeners) listener();
}

function semanticArguments(arguments_: AgentActivityArgument[] | undefined): AgentActivityArgument[] {
  return (arguments_ ?? [])
    .filter((argument) => argument.name !== "id" && !argument.name.endsWith("_id"))
    .map((argument) => ({
      name: argument.name,
      value: argument.value.replace(UUID_PATTERN, "[resource]"),
    }));
}

/** Records one `agent.activity` event. Replays and idempotent retries dedupe by resource plus
 * semantic details; legacy rows without details retain the resource-based fallback. */
export function recordAgentActivity(event: AgentActivityEvent): void {
  const data = event.data;
  const item: AgentActivityItem = {
    eventId: event.event_id,
    agentMemberId: data.member_id,
    kind: data.kind,
    occurredAt: event.occurred_at,
    runId: data.run_id,
    channelId: data.channel_id,
    scopeChannelId: data.scope_channel_id,
    threadId: data.thread_id,
    messageId: data.message_id,
    taskId: data.task_id,
    itemId: data.item_id,
    targetMemberId: data.target_member_id,
    arguments: semanticArguments(data.arguments),
    messagePreview: data.message_preview,
    messageTruncated: data.message_truncated ?? false,
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

/** Drops rows whose visibility scope changed after a Channel membership update. */
export function clearAgentActivityForChannel(channelId: string): void {
  let changed = false;
  for (const [agentId, items] of activityByAgent) {
    const visibleItems = items.filter(
      (item) => item.channelId !== channelId && item.scopeChannelId !== channelId,
    );
    if (visibleItems.length !== items.length) {
      activityByAgent.set(agentId, visibleItems);
      changed = true;
    }
  }
  if (changed) notify();
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
