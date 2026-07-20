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
};

export function useSpaces(humanId: string | undefined, enabled: boolean) {
  const [state, setState] = useState<SpacesState>({ status: "idle" });
  const generation = useRef(0);
  const controller = useRef<AbortController | undefined>(undefined);

  const load = useCallback(
    async (pending: "loading" | "retrying") => {
      if (!humanId) return;
      const currentGeneration = ++generation.current;
      controller.current?.abort();
      const request = new AbortController();
      controller.current = request;
      setState((current) => ({
        status: current.data ? "refreshing" : pending,
        data: current.data,
      }));
      try {
        const data = await loadDirectory(humanId, { signal: request.signal });
        if (generation.current !== currentGeneration) return;
        setState({ status: "ready", data });
      } catch (error) {
        if (request.signal.aborted || generation.current !== currentGeneration)
          return;
        setState((current) => ({
          status: current.data ? "stale" : "error",
          data: current.data,
          error: collaborationErrorMessage(error, "load collaboration facts"),
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
      const space = await createDM({ requestId, peer });
      retainCreatedSpace(setState, space);
      await load("retrying");
      return space;
    },
    [load],
  );

  const createGroupSpace = useCallback(
    async (requestId: string, name: string): Promise<Space> => {
      const space = await createGroup({ requestId, name });
      retainCreatedSpace(setState, space);
      await load("retrying");
      return space;
    },
    [load],
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
  space: Space,
) {
  setState((current) => {
    if (!current.data) return current;
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
