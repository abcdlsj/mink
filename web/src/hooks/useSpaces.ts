import { useCallback } from "react";
import {
  collaborationErrorMessage,
  createDM,
  createGroup,
  isInaccessibleCollaborationError,
  isUnauthenticatedCollaborationError,
  loadDirectory,
  type DirectorySnapshot,
} from "../lib/collaboration";
import type { Principal, Space } from "../gen/sumi/space/v1/space_pb";
import {
  useRemoteSnapshot,
  type SnapshotBinding,
  type SnapshotFailure,
  type SnapshotStateSetter,
} from "./useRemoteSnapshot";

export type SpacesState = {
  status:
    | "idle"
    | "loading"
    | "retrying"
    | "ready"
    | "refreshing"
    | "error"
    | "stale";
  data?: DirectorySnapshot;
  error?: string;
  principalId?: string;
  accessInvalidated?: boolean;
  authenticationInvalidated?: boolean;
};

type SpacesFailure = SnapshotFailure & {
  accessInvalidated: boolean;
  authenticationInvalidated: boolean;
};

export function useSpaces(humanId: string | undefined, enabled: boolean) {
  const load = useCallback(
    (signal: AbortSignal) => {
      if (!humanId) throw staleMutationError();
      return loadDirectory(humanId, { signal });
    },
    [humanId],
  );
  const classifyError = useCallback(
    (error: unknown): SpacesFailure => ({
      message: collaborationErrorMessage(error, "load collaboration facts"),
      discard: isInaccessibleCollaborationError(error),
      accessInvalidated: isInaccessibleCollaborationError(error),
      authenticationInvalidated: isUnauthenticatedCollaborationError(error),
    }),
    [],
  );
  const snapshot = useRemoteSnapshot({
    enabled: enabled && Boolean(humanId),
    targetId: humanId,
    load,
    classifyError,
  });

  const createDirectMessage = useCallback(
    async (requestId: string, peer: Principal): Promise<Space> => {
      const binding = requireBinding(snapshot.capture());
      const space = await createDM({ requestId, peer });
      requireCurrentMutation(snapshot.isCurrent(binding));
      retainCreatedSpace(snapshot.setState, binding.targetId, space);
      await refreshCurrentSnapshot(snapshot, binding.targetId);
      return space;
    },
    [snapshot],
  );

  const createGroupSpace = useCallback(
    async (requestId: string, name: string): Promise<Space> => {
      const binding = requireBinding(snapshot.capture());
      const space = await createGroup({ requestId, name });
      requireCurrentMutation(snapshot.isCurrent(binding));
      retainCreatedSpace(snapshot.setState, binding.targetId, space);
      await refreshCurrentSnapshot(snapshot, binding.targetId);
      return space;
    },
    [snapshot],
  );

  return {
    status: snapshot.state.status,
    data: snapshot.state.data,
    error: snapshot.state.failure?.message,
    principalId: snapshot.state.targetId,
    accessInvalidated: snapshot.state.failure?.accessInvalidated || undefined,
    authenticationInvalidated:
      snapshot.state.failure?.authenticationInvalidated || undefined,
    retry: () => snapshot.refresh("retrying"),
    refresh: () => snapshot.refresh("retrying"),
    createDirectMessage,
    createGroupSpace,
  } satisfies SpacesState & {
    retry: () => Promise<void>;
    refresh: () => Promise<void>;
    createDirectMessage: (requestId: string, peer: Principal) => Promise<Space>;
    createGroupSpace: (requestId: string, name: string) => Promise<Space>;
  };
}

async function refreshCurrentSnapshot(
  snapshot: {
    refresh: (pending: "loading" | "retrying") => Promise<void>;
    capture: () => SnapshotBinding | undefined;
    isCurrent: (binding: SnapshotBinding) => boolean;
  },
  targetId: string,
) {
  const refresh = snapshot.refresh("retrying");
  const binding = requireBinding(snapshot.capture());
  await refresh;
  requireCurrentMutation(
    binding.targetId === targetId && snapshot.isCurrent(binding),
  );
}

function retainCreatedSpace(
  setState: SnapshotStateSetter<DirectorySnapshot, SpacesFailure>,
  humanId: string,
  space: Space,
) {
  setState((current) => {
    if (!current.data || current.targetId !== humanId) return current;
    return {
      ...current,
      data: {
        ...current.data,
        spaces: [
          ...current.data.spaces.filter((existing) => existing.id !== space.id),
          space,
        ],
      },
    };
  });
}

function requireBinding(binding: SnapshotBinding | undefined): SnapshotBinding {
  if (!binding) throw staleMutationError();
  return binding;
}

function requireCurrentMutation(current: boolean) {
  if (!current) throw staleMutationError();
}

function staleMutationError() {
  const error = new Error(
    "The authenticated Human changed before the mutation completed",
  );
  error.name = "AbortError";
  return error;
}
