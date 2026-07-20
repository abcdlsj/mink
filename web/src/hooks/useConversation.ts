import {
  useCallback,
  useEffect,
  useRef,
  useState,
  type Dispatch,
  type SetStateAction,
} from "react";
import type { Message, Principal } from "../gen/sumi/space/v1/space_pb";
import {
  addSpaceMember,
  collaborationErrorMessage,
  isInaccessibleCollaborationError,
  isUnauthenticatedCollaborationError,
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
  inaccessibleTargetId?: string;
  inaccessibleSpaceId?: string;
  authenticationInvalidated?: boolean;
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
    if (!humanId || !spaceId || latestSpaceId.current !== spaceId) return;
    const targetSpaceId = spaceId;
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
      if (
        latestSpaceId.current !== targetSpaceId ||
        conversationGeneration.current !== nextGeneration
      )
        return;
      setConversation({ status: "ready", data });
    } catch (error) {
      if (
        request.signal.aborted ||
        latestSpaceId.current !== targetSpaceId ||
        conversationGeneration.current !== nextGeneration
      )
        return;
      setConversation((current) => ({
        status:
          !isInaccessibleCollaborationError(error) &&
          current.data?.space.id === targetSpaceId
            ? "stale"
            : "error",
        data:
          !isInaccessibleCollaborationError(error) &&
          current.data?.space.id === targetSpaceId
            ? current.data
            : undefined,
        error: collaborationErrorMessage(error, "load conversation"),
        inaccessibleTargetId: isInaccessibleCollaborationError(error)
          ? targetSpaceId
          : undefined,
        authenticationInvalidated:
          isUnauthenticatedCollaborationError(error) || undefined,
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
    if (!threadRoot || latestThreadRootId.current !== threadRoot.id) return;
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
      if (
        latestThreadRootId.current !== rootId ||
        threadGeneration.current !== nextGeneration
      )
        return;
      setThread({ status: "ready", data });
    } catch (error) {
      if (
        request.signal.aborted ||
        latestThreadRootId.current !== rootId ||
        threadGeneration.current !== nextGeneration
      )
        return;
      const inaccessible = isInaccessibleCollaborationError(error);
      setThread((current) => ({
        status:
          !inaccessible && current.data?.root.id === rootId ? "stale" : "error",
        data:
          !inaccessible && current.data?.root.id === rootId
            ? current.data
            : undefined,
        error: collaborationErrorMessage(error, "load thread"),
        inaccessibleTargetId: inaccessible ? rootId : undefined,
        inaccessibleSpaceId: inaccessible ? threadRoot.spaceId : undefined,
        authenticationInvalidated:
          isUnauthenticatedCollaborationError(error) || undefined,
      }));
      if (inaccessible) {
        clearConversationForThread(
          setConversation,
          threadRoot.spaceId,
          error,
          "load thread",
        );
      }
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
    async (
      targetSpaceId: string,
      targetGeneration: number,
      mutation: () => Promise<void>,
    ) => {
      await mutation();
      if (
        latestSpaceId.current !== targetSpaceId ||
        conversationGeneration.current !== targetGeneration
      )
        return;
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
        const inaccessible = isInaccessibleCollaborationError(error);
        setConversation({
          status: inaccessible ? "error" : "stale",
          data: inaccessible ? undefined : snapshot,
          error: collaborationErrorMessage(error, "load more messages"),
          inaccessibleTargetId: inaccessible ? snapshot.space.id : undefined,
          authenticationInvalidated:
            isUnauthenticatedCollaborationError(error) || undefined,
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
        const inaccessible = isInaccessibleCollaborationError(error);
        setThread({
          status: inaccessible ? "error" : "stale",
          data: inaccessible ? undefined : snapshot,
          error: collaborationErrorMessage(error, "load more replies"),
          inaccessibleTargetId: inaccessible ? snapshot.root.id : undefined,
          inaccessibleSpaceId: inaccessible ? snapshot.root.spaceId : undefined,
          authenticationInvalidated:
            isUnauthenticatedCollaborationError(error) || undefined,
        });
        if (inaccessible) {
          clearConversationForThread(
            setConversation,
            snapshot.root.spaceId,
            error,
            "load more replies",
          );
        }
      }
    },
    sendMain: async (requestId: string, body: string) => {
      if (!spaceId) throw new Error("No Space selected");
      const targetSpaceId = spaceId;
      const targetGeneration = conversationGeneration.current;
      const message = await sendSpaceMessage({
        requestId,
        spaceId: targetSpaceId,
        body,
      });
      if (
        latestSpaceId.current !== targetSpaceId ||
        conversationGeneration.current !== targetGeneration
      )
        return;
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
      const targetGeneration = threadGeneration.current;
      const message = await sendThreadMessage({
        requestId,
        threadRootMessageId: targetRootId,
        body,
      });
      if (
        latestThreadRootId.current !== targetRootId ||
        threadGeneration.current !== targetGeneration
      )
        return;
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
      await mutateAndRefresh(spaceId, conversationGeneration.current, () =>
        addSpaceMember({ requestId, spaceId, member }),
      );
    },
    removeMember: async (requestId: string, member: Principal) => {
      if (!spaceId) throw new Error("No Space selected");
      await mutateAndRefresh(spaceId, conversationGeneration.current, () =>
        removeSpaceMember({ requestId, spaceId, member }),
      );
    },
    setArchived: async (requestId: string, archived: boolean) => {
      if (!spaceId) throw new Error("No Space selected");
      await mutateAndRefresh(spaceId, conversationGeneration.current, () =>
        setSpaceArchived({ requestId, spaceId, archived }),
      );
    },
  };
}

function clearConversationForThread(
  setConversation: Dispatch<
    SetStateAction<SnapshotState<ConversationSnapshot>>
  >,
  spaceId: string,
  error: unknown,
  action: string,
) {
  setConversation((current) =>
    current.data?.space.id === spaceId
      ? {
          status: "error",
          error: collaborationErrorMessage(error, action),
          inaccessibleTargetId: spaceId,
          authenticationInvalidated:
            isUnauthenticatedCollaborationError(error) || undefined,
        }
      : current,
  );
}
