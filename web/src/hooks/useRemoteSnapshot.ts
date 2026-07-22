import {
  useCallback,
  useEffect,
  useRef,
  useState,
  type Dispatch,
  type SetStateAction,
} from "react";

export type SnapshotStatus =
  | "idle"
  | "loading"
  | "retrying"
  | "ready"
  | "refreshing"
  | "error"
  | "stale";

export type SnapshotFailure = {
  message: string;
  discard: boolean;
};

export type RemoteSnapshotState<T, F extends SnapshotFailure> = {
  status: SnapshotStatus;
  data?: T;
  targetId?: string;
  failure?: F;
};

export type SnapshotBinding = {
  targetId: string;
  generation: number;
};

type RemoteSnapshotOptions<T, F extends SnapshotFailure> = {
  enabled: boolean;
  targetId: string | undefined;
  load: (signal: AbortSignal) => Promise<T>;
  classifyError: (error: unknown) => F;
  onFailure?: (error: unknown, failure: F) => void;
};

export function useRemoteSnapshot<T, F extends SnapshotFailure>({
  enabled,
  targetId,
  load,
  classifyError,
  onFailure,
}: RemoteSnapshotOptions<T, F>) {
  const [state, setState] = useState<RemoteSnapshotState<T, F>>({
    status: "idle",
  });
  const generation = useRef(0);
  const controller = useRef<AbortController | undefined>(undefined);
  const latestTargetId = useRef(targetId);
  const latestEnabled = useRef(enabled);
  latestTargetId.current = targetId;
  latestEnabled.current = enabled;

  const isCurrent = useCallback((binding: SnapshotBinding) => {
    return (
      latestEnabled.current &&
      latestTargetId.current === binding.targetId &&
      generation.current === binding.generation
    );
  }, []);

  const capture = useCallback((): SnapshotBinding | undefined => {
    if (!latestEnabled.current || !latestTargetId.current) return undefined;
    return {
      targetId: latestTargetId.current,
      generation: generation.current,
    };
  }, []);

  const refresh = useCallback(
    async (pending: "loading" | "retrying" = "retrying") => {
      if (!enabled || !targetId || latestTargetId.current !== targetId) return;
      const binding = { targetId, generation: ++generation.current };
      controller.current?.abort();
      const request = new AbortController();
      controller.current = request;
      setState((current) => {
        const retain = current.targetId === targetId ? current.data : undefined;
        return {
          status: retain ? "refreshing" : pending,
          data: retain,
          targetId,
        };
      });
      try {
        const data = await load(request.signal);
        if (!isCurrent(binding)) return;
        setState({ status: "ready", data, targetId });
      } catch (error) {
        if (request.signal.aborted || !isCurrent(binding)) return;
        const failure = classifyError(error);
        onFailure?.(error, failure);
        setState((current) => {
          const retain =
            !failure.discard && current.targetId === targetId
              ? current.data
              : undefined;
          return {
            status: retain ? "stale" : "error",
            data: retain,
            targetId,
            failure,
          };
        });
      }
    },
    [classifyError, enabled, isCurrent, load, onFailure, targetId],
  );

  useEffect(() => {
    if (!enabled || !targetId) {
      controller.current?.abort();
      generation.current += 1;
      setState({ status: "idle" });
      return;
    }
    void refresh("loading");
    return () => controller.current?.abort();
  }, [enabled, refresh, targetId]);

  return { state, setState, refresh, capture, isCurrent };
}

export type SnapshotStateSetter<T, F extends SnapshotFailure> = Dispatch<
  SetStateAction<RemoteSnapshotState<T, F>>
>;
