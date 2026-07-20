import { useCallback, useEffect, useState } from "react";
import type { GetBootstrapResponse } from "../gen/sumi/system/v1/system_pb";
import { getBootstrap } from "../lib/bootstrap";

type BootstrapState =
  | { status: "loading"; value: undefined }
  | { status: "retrying"; value: undefined }
  | { status: "ready"; value: GetBootstrapResponse }
  | { status: "offline"; value: undefined };

export function useBootstrap() {
  const [state, setState] = useState<BootstrapState>({
    status: "loading",
    value: undefined,
  });

  const load = useCallback(async (pending: "loading" | "retrying") => {
    setState({ status: pending, value: undefined });
    try {
      const value = await getBootstrap();
      setState({ status: "ready", value });
    } catch {
      setState({ status: "offline", value: undefined });
    }
  }, []);

  useEffect(() => {
    void load("loading");
  }, [load]);

  return { ...state, retry: () => load("retrying") };
}
