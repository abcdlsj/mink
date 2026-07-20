import { useCallback, useEffect, useState } from "react";
import { factErrorMessage, loadFacts, type FactsSnapshot } from "../lib/facts";

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

const initialState: FactsState = { status: "idle" };

export function useFacts(enabled: boolean) {
  const [state, setState] = useState<FactsState>(initialState);

  const load = useCallback(async (pending: "loading" | "retrying") => {
    setState((current) => ({
      status: current.data ? "refreshing" : pending,
      data: current.data,
    }));
    try {
      const data = await loadFacts();
      setState({ status: "ready", data });
    } catch (error) {
      setState((current) => ({
        status: current.data ? "stale" : "error",
        data: current.data,
        error: factErrorMessage(error, "load facts"),
      }));
    }
  }, []);

  useEffect(() => {
    if (enabled && state.status === "idle") void load("loading");
  }, [enabled, load, state.status]);

  const mutate = useCallback(
    (change: (current: FactsSnapshot) => FactsSnapshot) => {
      setState((current) => {
        if (!current.data) return current;
        return { ...current, data: change(current.data) };
      });
    },
    [],
  );

  return {
    ...state,
    retry: () => load("retrying"),
    refresh: () => load("retrying"),
    mutate,
  };
}
