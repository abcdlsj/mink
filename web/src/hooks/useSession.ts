import { useCallback, useEffect, useState } from "react";
import {
  getLocalSetupRequired,
  getSession,
  LocalAuthError,
  loginLocalAccount,
  logoutSession,
  setupLocalAccount,
  type BrowserHuman,
  type LocalLoginInput,
  type LocalSetupInput,
} from "../lib/session";

type SessionState =
  | { status: "loading" | "retrying"; human: undefined }
  | { status: "authenticated"; human: BrowserHuman }
  | {
      status: "unauthenticated" | "authenticating";
      human: undefined;
      setupRequired: boolean;
      authError?: string;
    }
  | { status: "error"; human: undefined }
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
      if (human) {
        setState({ status: "authenticated", human });
        return;
      }
      const setupRequired = await getLocalSetupRequired();
      setState({
        status: "unauthenticated",
        human: undefined,
        setupRequired,
      });
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
      const setupRequired = await getLocalSetupRequired();
      setState({
        status: "unauthenticated",
        human: undefined,
        setupRequired,
      });
    } catch {
      setState({ status: "error", human: undefined });
    }
  }, []);

  const authenticate = useCallback(
    async (setupRequired: boolean, action: () => Promise<BrowserHuman>) => {
      setState({
        status: "authenticating",
        human: undefined,
        setupRequired,
      });
      try {
        const human = await action();
        setState({ status: "authenticated", human });
      } catch (error) {
        const setupComplete =
          error instanceof LocalAuthError && error.code === "setup_complete";
        setState({
          status: "unauthenticated",
          human: undefined,
          setupRequired: setupComplete ? false : setupRequired,
          authError:
            error instanceof Error
              ? error.message
              : "Local authentication is unavailable.",
        });
      }
    },
    [],
  );

  const login = useCallback(
    (input: LocalLoginInput) =>
      authenticate(false, () => loginLocalAccount(input)),
    [authenticate],
  );

  const setup = useCallback(
    (input: LocalSetupInput) =>
      authenticate(true, () => setupLocalAccount(input)),
    [authenticate],
  );

  return {
    ...state,
    retry: () => load("retrying"),
    logout,
    login,
    setup,
    clearAuthError: () =>
      setState((current) =>
        current.status === "unauthenticated"
          ? { ...current, authError: undefined }
          : current,
      ),
  };
}
