import {
  useCallback,
  useEffect,
  useRef,
  useState,
  type Dispatch,
  type SetStateAction,
} from "react";
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

export function useSpaces(humanId: string | undefined, enabled: boolean) {
  const [state, setState] = useState<SpacesState>({ status: "idle" });
  const generation = useRef(0);
  const controller = useRef<AbortController | undefined>(undefined);
  const latestHumanId = useRef(humanId);
  const latestEnabled = useRef(enabled);
  latestHumanId.current = humanId;
  latestEnabled.current = enabled;

  const load = useCallback(
    async (pending: "loading" | "retrying") => {
      if (
        !humanId ||
        latestHumanId.current !== humanId ||
        !latestEnabled.current
      )
        return;
      const currentGeneration = ++generation.current;
      controller.current?.abort();
      const request = new AbortController();
      controller.current = request;
      setState((current) => ({
        status:
          current.principalId === humanId && current.data
            ? "refreshing"
            : pending,
        data: current.principalId === humanId ? current.data : undefined,
        principalId: humanId,
      }));
      try {
        const data = await loadDirectory(humanId, { signal: request.signal });
        if (generation.current !== currentGeneration) return;
        setState({ status: "ready", data, principalId: humanId });
      } catch (error) {
        if (request.signal.aborted || generation.current !== currentGeneration)
          return;
        setState((current) => ({
          status:
            !isInaccessibleCollaborationError(error) &&
            current.principalId === humanId &&
            current.data
              ? "stale"
              : "error",
          data:
            !isInaccessibleCollaborationError(error) &&
            current.principalId === humanId
              ? current.data
              : undefined,
          error: collaborationErrorMessage(error, "load collaboration facts"),
          principalId: humanId,
          accessInvalidated:
            isInaccessibleCollaborationError(error) || undefined,
          authenticationInvalidated:
            isUnauthenticatedCollaborationError(error) || undefined,
        }));
      }
    },
    [humanId],
  );

  useEffect(() => {
    if (!enabled || !humanId) {
      controller.current?.abort();
      generation.current += 1;
      setState({ status: "idle" });
      return;
    }
    void load("loading");
    return () => controller.current?.abort();
  }, [enabled, humanId, load]);

  const createDirectMessage = useCallback(
    async (requestId: string, peer: Principal): Promise<Space> => {
      if (!humanId) throw staleMutationError();
      const targetHumanId = humanId;
      const targetGeneration = generation.current;
      const space = await createDM({ requestId, peer });
      requireCurrentMutation(
        targetHumanId,
        targetGeneration,
        latestHumanId.current,
        latestEnabled.current,
        generation.current,
      );
      retainCreatedSpace(setState, targetHumanId, space);
      const refresh = load("retrying");
      const refreshGeneration = generation.current;
      await refresh;
      requireCurrentMutation(
        targetHumanId,
        refreshGeneration,
        latestHumanId.current,
        latestEnabled.current,
        generation.current,
      );
      return space;
    },
    [humanId, load],
  );

  const createGroupSpace = useCallback(
    async (requestId: string, name: string): Promise<Space> => {
      if (!humanId) throw staleMutationError();
      const targetHumanId = humanId;
      const targetGeneration = generation.current;
      const space = await createGroup({ requestId, name });
      requireCurrentMutation(
        targetHumanId,
        targetGeneration,
        latestHumanId.current,
        latestEnabled.current,
        generation.current,
      );
      retainCreatedSpace(setState, targetHumanId, space);
      const refresh = load("retrying");
      const refreshGeneration = generation.current;
      await refresh;
      requireCurrentMutation(
        targetHumanId,
        refreshGeneration,
        latestHumanId.current,
        latestEnabled.current,
        generation.current,
      );
      return space;
    },
    [humanId, load],
  );

  return {
    ...state,
    retry: () => load("retrying"),
    refresh: () => load("retrying"),
    createDirectMessage,
    createGroupSpace,
  };
}

function retainCreatedSpace(
  setState: Dispatch<SetStateAction<SpacesState>>,
  humanId: string,
  space: Space,
) {
  setState((current) => {
    if (!current.data || current.principalId !== humanId) return current;
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

function requireCurrentMutation(
  targetHumanId: string,
  targetGeneration: number,
  currentHumanId: string | undefined,
  enabled: boolean,
  currentGeneration: number,
) {
  if (
    !enabled ||
    currentHumanId !== targetHumanId ||
    currentGeneration !== targetGeneration
  ) {
    throw staleMutationError();
  }
}

function staleMutationError() {
  const error = new Error(
    "The authenticated Human changed before the mutation completed",
  );
  error.name = "AbortError";
  return error;
}
