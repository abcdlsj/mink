import { create } from "@bufbuild/protobuf";
import { Code, ConnectError } from "@connectrpc/connect";
import { useCallback } from "react";
import {
  ListHumanInboxItemsRequestSchema,
  type HumanInboxItem,
} from "../gen/sumi/inbox/v1/inbox_pb";
import { listHumanInboxItems } from "../lib/humanInbox";
import { useRemoteSnapshot, type SnapshotFailure } from "./useRemoteSnapshot";

export type HumanInboxSnapshot = { items: HumanInboxItem[] };

export function useHumanInbox(enabled = true) {
  const load = useCallback(async (signal: AbortSignal): Promise<HumanInboxSnapshot> => {
    const response = await listHumanInboxItems(
      create(ListHumanInboxItemsRequestSchema, { limit: 50 }),
      signal,
    );
    return { items: response.items };
  }, []);
  const classifyError = useCallback((error: unknown): SnapshotFailure => {
    const code = ConnectError.from(error).code;
    return {
      message: error instanceof Error ? error.message : "Could not load Human Inbox.",
      discard: code === Code.Unauthenticated || code === Code.PermissionDenied,
    };
  }, []);
  const snapshot = useRemoteSnapshot({ enabled, targetId: "human-inbox", load, classifyError });
  return {
    status: snapshot.state.status,
    data: snapshot.state.data,
    error: snapshot.state.failure?.message,
    refresh: () => snapshot.refresh("retrying"),
  };
}
