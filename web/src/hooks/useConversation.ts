import { useCallback, useEffect, useRef, useState } from "react";
import type { Message, Principal } from "../gen/sumi/space/v1/space_pb";
import {
  addSpaceMember,
  collaborationErrorMessage,
  loadConversation,
  loadMoreConversationMessages,
  loadMoreThreadMessages,
  loadThread,
  mergeMessages,
  removeSpaceMember,
  sendSpaceMessage,
  sendThreadMessage,
  setSpaceArchived,
  type ConversationSnapshot,
  type ThreadSnapshot,
} from "../lib/collaboration";

type SnapshotState<T> = {
  status: "idle" | "loading" | "ready" | "refreshing" | "error" | "stale";
  data?: T;
  error?: string;
};

export function useConversation(
  humanId: string | undefined,
  spaceId: string | undefined,
  threadRoot: Message | undefined,
) {
  const [conversation, setConversation] = useState<
    SnapshotState<ConversationSnapshot>
  >({ status: "idle" });
  const [thread, setThread] = useState<SnapshotState<ThreadSnapshot>>({
    status: "idle",
  });
  const conversationGeneration = useRef(0);
  const threadGeneration = useRef(0);
  const latestSpaceId = useRef(spaceId);
  const latestThreadRootId = useRef(threadRoot?.id);
  latestSpaceId.current = spaceId;
  latestThreadRootId.current = threadRoot?.id;
  const conversationController = useRef<AbortController | undefined>(undefined);
  const threadController = useRef<AbortController | undefined>(undefined);

  const refresh = useCallback(async () => {
    if (!humanId || !spaceId) return;
    const nextGeneration = ++conversationGeneration.current;
    conversationController.current?.abort();
    const request = new AbortController();
    conversationController.current = request;
    setConversation((current) => ({
      status: current.data?.space.id === spaceId ? "refreshing" : "loading",
      data: current.data?.space.id === spaceId ? current.data : undefined,
    }));
    try {
      const data = await loadConversation(humanId, spaceId, {
        signal: request.signal,
      });
      if (conversationGeneration.current !== nextGeneration) return;
      setConversation({ status: "ready", data });
    } catch (error) {
      if (
        request.signal.aborted ||
        conversationGeneration.current !== nextGeneration
      )
        return;
      setConversation((current) => ({
        status: current.data?.space.id === spaceId ? "stale" : "error",
        data: current.data?.space.id === spaceId ? current.data : undefined,
        error: collaborationErrorMessage(error, "load conversation"),
      }));
    }
  }, [humanId, spaceId]);

  useEffect(() => {
    if (!humanId || !spaceId) {
      conversationController.current?.abort();
      conversationGeneration.current += 1;
      setConversation({ status: "idle" });
      return;
    }
    void refresh();
    return () => conversationController.current?.abort();
  }, [humanId, refresh, spaceId]);

  const refreshThread = useCallback(async () => {
    if (!threadRoot) return;
    const rootId = threadRoot.id;
    const nextGeneration = ++threadGeneration.current;
    threadController.current?.abort();
    const request = new AbortController();
    threadController.current = request;
    setThread((current) => ({
      status: current.data?.root.id === rootId ? "refreshing" : "loading",
      data: current.data?.root.id === rootId ? current.data : undefined,
    }));
    try {
      const data = await loadThread(threadRoot, { signal: request.signal });
      if (threadGeneration.current !== nextGeneration) return;
      setThread({ status: "ready", data });
    } catch (error) {
      if (request.signal.aborted || threadGeneration.current !== nextGeneration)
        return;
      setThread((current) => ({
        status: current.data?.root.id === rootId ? "stale" : "error",
        data: current.data?.root.id === rootId ? current.data : undefined,
        error: collaborationErrorMessage(error, "load thread"),
      }));
    }
  }, [threadRoot]);

  useEffect(() => {
    if (!threadRoot) {
      threadController.current?.abort();
      threadGeneration.current += 1;
      setThread({ status: "idle" });
      return;
    }
    void refreshThread();
    return () => threadController.current?.abort();
  }, [refreshThread, threadRoot]);

  const mutateAndRefresh = useCallback(
    async (mutation: () => Promise<void>) => {
      await mutation();
      await refresh();
    },
    [refresh],
  );

  return {
    conversation,
    thread,
    refresh,
    refreshThread,
    loadMore: async () => {
      if (!conversation.data) return;
      const snapshot = conversation.data;
      const targetGeneration = conversationGeneration.current;
      try {
        const data = await loadMoreConversationMessages(snapshot);
        if (
          latestSpaceId.current !== snapshot.space.id ||
          conversationGeneration.current !== targetGeneration
        )
          return;
        setConversation({ status: "ready", data });
      } catch (error) {
        if (
          latestSpaceId.current !== snapshot.space.id ||
          conversationGeneration.current !== targetGeneration
        )
          return;
        setConversation({
          status: "stale",
          data: snapshot,
          error: collaborationErrorMessage(error, "load more messages"),
        });
      }
    },
    loadMoreThread: async () => {
      if (!thread.data) return;
      const snapshot = thread.data;
      const targetGeneration = threadGeneration.current;
      try {
        const data = await loadMoreThreadMessages(snapshot);
        if (
          latestThreadRootId.current !== snapshot.root.id ||
          threadGeneration.current !== targetGeneration
        )
          return;
        setThread({ status: "ready", data });
      } catch (error) {
        if (
          latestThreadRootId.current !== snapshot.root.id ||
          threadGeneration.current !== targetGeneration
        )
          return;
        setThread({
          status: "stale",
          data: snapshot,
          error: collaborationErrorMessage(error, "load more replies"),
        });
      }
    },
    sendMain: async (requestId: string, body: string) => {
      if (!spaceId) throw new Error("No Space selected");
      const targetSpaceId = spaceId;
      const message = await sendSpaceMessage({
        requestId,
        spaceId: targetSpaceId,
        body,
      });
      if (latestSpaceId.current !== targetSpaceId) return;
      setConversation((current) =>
        current.data?.space.id === targetSpaceId
          ? {
              status: "ready",
              data: {
                ...current.data,
                messages: mergeMessages(current.data.messages, [message]),
              },
            }
          : current,
      );
      await refresh();
    },
    sendReply: async (requestId: string, body: string) => {
      if (!threadRoot) throw new Error("No Thread selected");
      const targetRootId = threadRoot.id;
      const message = await sendThreadMessage({
        requestId,
        threadRootMessageId: targetRootId,
        body,
      });
      if (latestThreadRootId.current !== targetRootId) return;
      setThread((current) =>
        current.data?.root.id === targetRootId
          ? {
              status: "ready",
              data: {
                ...current.data,
                replies: mergeMessages(current.data.replies, [message]),
              },
            }
          : current,
      );
      await refreshThread();
    },
    addMember: async (requestId: string, member: Principal) => {
      if (!spaceId) throw new Error("No Space selected");
      await mutateAndRefresh(() =>
        addSpaceMember({ requestId, spaceId, member }),
      );
    },
    removeMember: async (requestId: string, member: Principal) => {
      if (!spaceId) throw new Error("No Space selected");
      await mutateAndRefresh(() =>
        removeSpaceMember({ requestId, spaceId, member }),
      );
    },
    setArchived: async (requestId: string, archived: boolean) => {
      if (!spaceId) throw new Error("No Space selected");
      await mutateAndRefresh(() =>
        setSpaceArchived({ requestId, spaceId, archived }),
      );
    },
  };
}
