import { useCallback } from "react";
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
import {
  useRemoteSnapshot,
  type RemoteSnapshotState,
  type SnapshotBinding,
  type SnapshotFailure,
  type SnapshotStateSetter,
} from "./useRemoteSnapshot";

type SnapshotState<T> = {
  status: "idle" | "loading" | "ready" | "refreshing" | "error" | "stale";
  data?: T;
  error?: string;
  inaccessibleTargetId?: string;
  inaccessibleSpaceId?: string;
  authenticationInvalidated?: boolean;
};

type ConversationFailure = SnapshotFailure & {
  inaccessibleTargetId?: string;
  inaccessibleSpaceId?: string;
  authenticationInvalidated: boolean;
};

export function useConversation(
  humanId: string | undefined,
  spaceId: string | undefined,
  threadRoot: Message | undefined,
) {
  const loadMain = useCallback(
    (signal: AbortSignal) => {
      if (!humanId || !spaceId) throw new Error("No Space selected");
      return loadConversation(humanId, spaceId, { signal });
    },
    [humanId, spaceId],
  );
  const classifyMainError = useCallback(
    (error: unknown) => classifyFailure(error, "load conversation", spaceId),
    [spaceId],
  );
  const main = useRemoteSnapshot({
    enabled: Boolean(humanId && spaceId),
    targetId: spaceId,
    load: loadMain,
    classifyError: classifyMainError,
  });

  const loadSelectedThread = useCallback(
    (signal: AbortSignal) => {
      if (!threadRoot) throw new Error("No Thread selected");
      return loadThread(threadRoot, { signal });
    },
    [threadRoot],
  );
  const classifyThreadError = useCallback(
    (error: unknown) =>
      classifyFailure(
        error,
        "load thread",
        threadRoot?.id,
        threadRoot?.spaceId,
      ),
    [threadRoot],
  );
  const clearMainOnThreadFailure = useCallback(
    (error: unknown, failure: ConversationFailure) => {
      if (failure.discard && threadRoot) {
        clearConversationForThread(
          main.setState,
          threadRoot.spaceId,
          error,
          "load thread",
        );
      }
    },
    [main.setState, threadRoot],
  );
  const selectedThread = useRemoteSnapshot({
    enabled: Boolean(threadRoot),
    targetId: threadRoot?.id,
    load: loadSelectedThread,
    classifyError: classifyThreadError,
    onFailure: clearMainOnThreadFailure,
  });

  const mutateAndRefresh = useCallback(
    async (binding: SnapshotBinding, mutation: () => Promise<void>) => {
      await mutation();
      if (!main.isCurrent(binding)) return;
      await main.refresh();
    },
    [main],
  );

  const conversation = flattenState(main.state);
  const thread = flattenState(selectedThread.state);

  return {
    conversation,
    thread,
    refresh: main.refresh,
    refreshThread: selectedThread.refresh,
    loadMore: async () => {
      const snapshot = main.state.data;
      const binding = main.capture();
      if (!snapshot || !binding) return;
      try {
        const data = await loadMoreConversationMessages(snapshot);
        if (!main.isCurrent(binding)) return;
        main.setState({
          status: "ready",
          data,
          targetId: binding.targetId,
        });
      } catch (error) {
        if (!main.isCurrent(binding)) return;
        const failure = classifyFailure(
          error,
          "load more messages",
          snapshot.space.id,
        );
        main.setState({
          status: failure.discard ? "error" : "stale",
          data: failure.discard ? undefined : snapshot,
          targetId: binding.targetId,
          failure,
        });
      }
    },
    loadMoreThread: async () => {
      const snapshot = selectedThread.state.data;
      const binding = selectedThread.capture();
      if (!snapshot || !binding) return;
      try {
        const data = await loadMoreThreadMessages(snapshot);
        if (!selectedThread.isCurrent(binding)) return;
        selectedThread.setState({
          status: "ready",
          data,
          targetId: binding.targetId,
        });
      } catch (error) {
        if (!selectedThread.isCurrent(binding)) return;
        const failure = classifyFailure(
          error,
          "load more replies",
          snapshot.root.id,
          snapshot.root.spaceId,
        );
        selectedThread.setState({
          status: failure.discard ? "error" : "stale",
          data: failure.discard ? undefined : snapshot,
          targetId: binding.targetId,
          failure,
        });
        if (failure.discard) {
          clearConversationForThread(
            main.setState,
            snapshot.root.spaceId,
            error,
            "load more replies",
          );
        }
      }
    },
    sendMain: async (requestId: string, body: string) => {
      const binding = main.capture();
      if (!spaceId || !binding) throw new Error("No Space selected");
      const message = await sendSpaceMessage({
        requestId,
        spaceId: binding.targetId,
        body,
      });
      if (!main.isCurrent(binding)) return;
      main.setState((current) =>
        current.data?.space.id === binding.targetId
          ? {
              status: "ready",
              data: {
                ...current.data,
                messages: mergeMessages(current.data.messages, [message]),
              },
              targetId: binding.targetId,
            }
          : current,
      );
      await main.refresh();
    },
    sendReply: async (requestId: string, body: string) => {
      const binding = selectedThread.capture();
      if (!threadRoot || !binding) throw new Error("No Thread selected");
      const message = await sendThreadMessage({
        requestId,
        threadRootMessageId: binding.targetId,
        body,
      });
      if (!selectedThread.isCurrent(binding)) return;
      selectedThread.setState((current) =>
        current.data?.root.id === binding.targetId
          ? {
              status: "ready",
              data: {
                ...current.data,
                replies: mergeMessages(current.data.replies, [message]),
              },
              targetId: binding.targetId,
            }
          : current,
      );
      await selectedThread.refresh();
    },
    addMember: async (requestId: string, member: Principal) => {
      const binding = main.capture();
      if (!spaceId || !binding) throw new Error("No Space selected");
      await mutateAndRefresh(binding, () =>
        addSpaceMember({ requestId, spaceId: binding.targetId, member }),
      );
    },
    removeMember: async (requestId: string, member: Principal) => {
      const binding = main.capture();
      if (!spaceId || !binding) throw new Error("No Space selected");
      await mutateAndRefresh(binding, () =>
        removeSpaceMember({ requestId, spaceId: binding.targetId, member }),
      );
    },
    setArchived: async (requestId: string, archived: boolean) => {
      const binding = main.capture();
      if (!spaceId || !binding) throw new Error("No Space selected");
      await mutateAndRefresh(binding, () =>
        setSpaceArchived({
          requestId,
          spaceId: binding.targetId,
          archived,
        }),
      );
    },
  };
}

function classifyFailure(
  error: unknown,
  action: string,
  targetId: string | undefined,
  spaceId?: string,
): ConversationFailure {
  const inaccessible = isInaccessibleCollaborationError(error);
  return {
    message: collaborationErrorMessage(error, action),
    discard: inaccessible,
    inaccessibleTargetId: inaccessible ? targetId : undefined,
    inaccessibleSpaceId: inaccessible ? spaceId : undefined,
    authenticationInvalidated: isUnauthenticatedCollaborationError(error),
  };
}

function flattenState<T>(
  state: RemoteSnapshotState<T, ConversationFailure>,
): SnapshotState<T> {
  return {
    status: state.status === "retrying" ? "loading" : state.status,
    data: state.data,
    error: state.failure?.message,
    inaccessibleTargetId: state.failure?.inaccessibleTargetId,
    inaccessibleSpaceId: state.failure?.inaccessibleSpaceId,
    authenticationInvalidated:
      state.failure?.authenticationInvalidated || undefined,
  };
}

function clearConversationForThread(
  setConversation: SnapshotStateSetter<
    ConversationSnapshot,
    ConversationFailure
  >,
  spaceId: string,
  error: unknown,
  action: string,
) {
  setConversation((current) =>
    current.data?.space.id === spaceId
      ? {
          status: "error",
          targetId: spaceId,
          failure: classifyFailure(error, action, spaceId),
        }
      : current,
  );
}
