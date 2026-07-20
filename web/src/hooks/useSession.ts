import { useCallback, useEffect, useState } from "react";
import { getSession, logoutSession, type BrowserHuman } from "../lib/session";

type SessionState =
  | { status: "loading" | "retrying"; human: undefined }
  | { status: "authenticated"; human: BrowserHuman }
  | { status: "unauthenticated" | "error"; human: undefined }
  | { status: "logging-out"; human: BrowserHuman };

export function useSession() {
  const [state, setState] = useState<SessionState>({
    status: "loading",
    human: undefined,
  });

  const load = useCallback(async (pending: "loading" | "retrying") => {
    setState({ status: pending, human: undefined });
    try {
      const human = await getSession();
      setState(
        human
          ? { status: "authenticated", human }
          : { status: "unauthenticated", human: undefined },
      );
    } catch {
      setState({ status: "error", human: undefined });
    }
  }, []);

  useEffect(() => {
    void load("loading");
  }, [load]);

  const logout = useCallback(async () => {
    setState((current) =>
      current.human ? { status: "logging-out", human: current.human } : current,
    );
    try {
      await logoutSession();
      setState({ status: "unauthenticated", human: undefined });
    } catch {
      setState({ status: "error", human: undefined });
    }
  }, []);

  return {
    ...state,
    retry: () => load("retrying"),
    logout,
  };
}
