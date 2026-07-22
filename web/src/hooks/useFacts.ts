import { useCallback } from "react";
import { factErrorMessage, loadFacts, type FactsSnapshot } from "../lib/facts";
import { useRemoteSnapshot, type SnapshotFailure } from "./useRemoteSnapshot";

export type FactsState = {
  status:
    | "idle"
    | "loading"
    | "retrying"
    | "ready"
    | "refreshing"
    | "error"
    | "stale";
  data?: FactsSnapshot;
  error?: string;
};

export function useFacts(enabled: boolean) {
  const load = useCallback((signal: AbortSignal) => loadFacts({ signal }), []);
  const classifyError = useCallback(
    (error: unknown): SnapshotFailure => ({
      message: factErrorMessage(error, "load facts"),
      discard: false,
    }),
    [],
  );
  const snapshot = useRemoteSnapshot({
    enabled,
    targetId: enabled ? "management-facts" : undefined,
    load,
    classifyError,
  });

  const mutate = useCallback(
    (change: (current: FactsSnapshot) => FactsSnapshot) => {
      snapshot.setState((current) => {
        if (!current.data) return current;
        return { ...current, data: change(current.data) };
      });
    },
    [snapshot.setState],
  );

  return {
    status: snapshot.state.status,
    data: snapshot.state.data,
    error: snapshot.state.failure?.message,
    retry: () => snapshot.refresh("retrying"),
    refresh: () => snapshot.refresh("retrying"),
    mutate,
  } satisfies FactsState & {
    retry: () => Promise<void>;
    refresh: () => Promise<void>;
    mutate: (change: (current: FactsSnapshot) => FactsSnapshot) => void;
  };
}
