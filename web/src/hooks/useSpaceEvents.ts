import { useQueryClient } from "@tanstack/react-query";
import { useEffect, useRef } from "react";

import { recordAgentActivity, type AgentActivityEvent } from "../agentActivity";

interface SumiEvent {
  type: string;
  event_id?: string;
  occurred_at?: string;
  data: {
    channel_id?: string;
    member_id?: string;
    kind?: string;
    run_id?: string;
    thread_id?: string;
    message_id?: string;
    task_id?: string;
    item_id?: string;
    target_member_id?: string;
  };
}

export function useSpaceEvents(
  spaceId?: string,
  onMessage?: (event: { type: string; channelId: string }) => void,
) {
  const queryClient = useQueryClient();
  const onMessageRef = useRef(onMessage);
  useEffect(() => {
    onMessageRef.current = onMessage;
  });

  useEffect(() => {
    if (!spaceId || typeof EventSource === "undefined") return;
    // A fresh connection replays the storage retention window (historical
    // message.created etc.). Only events arriving after the connection opened
    // are live; replay must not be treated as new unread activity.
    const connectedAt = Date.now();
    const source = new EventSource(`/api/v1/spaces/${encodeURIComponent(spaceId)}/events`);
    const invalidate = (event: MessageEvent<string>) => {
      const payload = JSON.parse(event.data) as SumiEvent;
      const isReplay =
        payload.occurred_at !== undefined &&
        new Date(payload.occurred_at).getTime() < connectedAt;
      if (
        payload.type === "agent.activity" &&
        payload.event_id &&
        payload.occurred_at &&
        payload.data.member_id &&
        payload.data.kind
      ) {
        recordAgentActivity(payload as AgentActivityEvent);
      }
      if (isReplay) return;
      if (payload.type.startsWith("message.") && payload.data.channel_id) {
        if (payload.type === "message.created" && payload.data.channel_id) {
          onMessageRef.current?.({ type: payload.type, channelId: payload.data.channel_id });
        }
        void queryClient.invalidateQueries({ queryKey: ["messages", payload.data.channel_id] });
        void queryClient.invalidateQueries({ queryKey: ["thread", payload.data.channel_id] });
      }
      if (payload.type === "inbox.changed") {
        void queryClient.invalidateQueries({ queryKey: ["inbox", spaceId] });
      }
      if (payload.type === "thread.updated") {
        void queryClient.invalidateQueries({ queryKey: ["thread"] });
        if (payload.data.channel_id) {
          void queryClient.invalidateQueries({ queryKey: ["messages", payload.data.channel_id] });
        }
      }
      if (payload.type.startsWith("channel.")) {
        void queryClient.invalidateQueries({ queryKey: ["channels", spaceId] });
        void queryClient.invalidateQueries({ queryKey: ["direct-messages", spaceId] });
      }
      if (payload.type === "member.changed") {
        void queryClient.invalidateQueries({ queryKey: ["members", spaceId] });
        if (payload.data.channel_id) {
          void queryClient.invalidateQueries({ queryKey: ["channel-members", payload.data.channel_id] });
        } else {
          // Space-level membership changes have no Channel scope. Keep the
          // fallback for any open Channel projections that may contain it.
          void queryClient.invalidateQueries({ queryKey: ["channel-members"] });
        }
      }
      if (payload.type === "agent.changed" || payload.type === "agent.updated" || payload.type === "run.changed") {
        void queryClient.invalidateQueries({ queryKey: ["agents", spaceId] });
        void queryClient.invalidateQueries({ queryKey: ["agent"] });
      }
      if (payload.type === "computer.changed") {
        void queryClient.invalidateQueries({ queryKey: ["computers", spaceId] });
      }
      if (payload.type.startsWith("task.")) {
        void queryClient.invalidateQueries({ queryKey: ["tasks", spaceId] });
        void queryClient.invalidateQueries({ queryKey: ["messages"] });
        void queryClient.invalidateQueries({ queryKey: ["thread"] });
      }
    };
    const eventTypes = [
      "message.created",
      "message.updated",
      "message.deleted",
      "thread.updated",
      "inbox.changed",
      "channel.created",
      "channel.updated",
      "member.changed",
      "agent.changed",
      "agent.created",
      "agent.updated",
      "agent.activity",
      "computer.changed",
      "run.changed",
      "task.created",
      "task.updated",
      "task.linked",
      "task.unlinked",
      "task.finished",
    ];
    for (const type of eventTypes) source.addEventListener(type, invalidate as EventListener);
    return () => source.close();
  }, [queryClient, spaceId]);
}
