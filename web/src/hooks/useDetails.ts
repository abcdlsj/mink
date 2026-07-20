import { useCallback, useEffect, useMemo, useState } from "react";
import type { Agent } from "../gen/sumi/agent/v1/agent_pb";
import type { Computer } from "../gen/sumi/computer/v1/computer_pb";
import type { AgentPlacement } from "../gen/sumi/placement/v1/placement_pb";
import {
  factErrorMessage,
  getAgentDetail,
  getComputer,
  type AgentDetailSnapshot,
} from "../lib/facts";

type DetailState<T> = {
  status: "idle" | "loading" | "ready" | "error" | "stale";
  data?: T;
  error?: string;
};

export function useAgentDetail(
  agentId: string | undefined,
  agent: Agent | undefined,
  placement: AgentPlacement | undefined,
) {
  const fallback = useMemo(
    () => (agent ? { agent, placement } : undefined),
    [agent, placement],
  );
  return useDetail(agentId, fallback, getAgentDetail, "load Agent details");
}

export function useComputerDetail(
  computerId: string | undefined,
  computer: Computer | undefined,
) {
  return useDetail(computerId, computer, getComputer, "load Computer details");
}

function useDetail<T>(
  key: string | undefined,
  fallback: T | undefined,
  loader: (key: string) => Promise<T>,
  action: string,
) {
  const [revision, setRevision] = useState(0);
  const [state, setState] = useState<DetailState<T>>({ status: "idle" });

  useEffect(() => {
    if (!key) {
      setState({ status: "idle" });
      return;
    }
    let current = true;
    setState({ status: "loading", data: fallback });
    void loader(key)
      .then((data) => {
        if (current) setState({ status: "ready", data });
      })
      .catch((error) => {
        if (!current) return;
        setState({
          status: fallback ? "stale" : "error",
          data: fallback,
          error: factErrorMessage(error, action),
        });
      });
    return () => {
      current = false;
    };
  }, [action, fallback, key, loader, revision]);

  const reload = useCallback(() => setRevision((value) => value + 1), []);
  return { ...state, reload };
}

export type { AgentDetailSnapshot };
