import { useQueryClient } from "@tanstack/react-query";
import { useEffect } from "react";

interface SumiEvent {
  type: string;
  data: { channel_id?: string };
}

export function useSpaceEvents(spaceId?: string) {
  const queryClient = useQueryClient();

  useEffect(() => {
    if (!spaceId || typeof EventSource === "undefined") return;
    const source = new EventSource(`/api/v1/spaces/${encodeURIComponent(spaceId)}/events`);
    const invalidate = (event: MessageEvent<string>) => {
      const payload = JSON.parse(event.data) as SumiEvent;
      if (payload.type.startsWith("message.") && payload.data.channel_id) {
        void queryClient.invalidateQueries({ queryKey: ["messages", payload.data.channel_id] });
        void queryClient.invalidateQueries({ queryKey: ["thread", payload.data.channel_id] });
      }
      if (payload.type === "inbox.changed") {
        void queryClient.invalidateQueries({ queryKey: ["inbox", spaceId] });
      }
      if (payload.type.startsWith("channel.")) {
        void queryClient.invalidateQueries({ queryKey: ["channels", spaceId] });
        void queryClient.invalidateQueries({ queryKey: ["direct-messages", spaceId] });
      }
      if (payload.type === "member.updated") {
        void queryClient.invalidateQueries({ queryKey: ["members", spaceId] });
      }
      if (payload.type === "agent.status_changed" || payload.type === "agent.run_changed" || payload.type === "agent.activity_changed") {
        void queryClient.invalidateQueries({ queryKey: ["agents", spaceId] });
        void queryClient.invalidateQueries({ queryKey: ["agent"] });
      }
      if (payload.type.startsWith("approval.")) {
        void queryClient.invalidateQueries({ queryKey: ["approvals", spaceId] });
        void queryClient.invalidateQueries({ queryKey: ["inbox", spaceId] });
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
      "inbox.changed",
      "channel.created",
      "channel.updated",
      "member.updated",
      "agent.status_changed",
      "agent.run_changed",
      "agent.activity_changed",
      "approval.created",
      "approval.resolved",
      "task.created",
      "task.updated",
    ];
    for (const type of eventTypes) source.addEventListener(type, invalidate as EventListener);
    return () => source.close();
  }, [queryClient, spaceId]);
}
