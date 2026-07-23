import { create } from "@bufbuild/protobuf";
import { Code, ConnectError } from "@connectrpc/connect";
import { useCallback, useState } from "react";
import {
  ClaimInboxItemRequestSchema,
  CompleteInboxItemRequestSchema,
  InboxState,
  ListInboxItemsRequestSchema,
  type InboxItem,
} from "../gen/sumi/inbox/v1/inbox_pb";
import {
  claimInboxItem,
  completeInboxItem,
  listInboxItems,
  resolveInboxDestination,
  type InboxDestination,
} from "../lib/inbox";
import { useRemoteSnapshot, type SnapshotFailure } from "./useRemoteSnapshot";

export type InboxSnapshot = { items: InboxItem[] };

export function useInbox(enabled = true) {
  const [pendingItemId, setPendingItemId] = useState<string>();
  const [actionError, setActionError] = useState<string>();
  const load = useCallback(
    async (signal: AbortSignal): Promise<InboxSnapshot> => {
      const response = await listInboxItems(
        create(ListInboxItemsRequestSchema, { limit: 100 }),
        signal,
      );
      return { items: response.items };
    },
    [],
  );
  const classifyError = useCallback((error: unknown): SnapshotFailure => {
    const code = ConnectError.from(error).code;
    return {
      message:
        error instanceof Error
          ? error.message
          : "Could not load Message Inbox.",
      discard: code === Code.Unauthenticated || code === Code.PermissionDenied,
    };
  }, []);
  const snapshot = useRemoteSnapshot({
    enabled,
    targetId: "message-inbox",
    load,
    classifyError,
  });

  const mutate = useCallback(
    async (item: InboxItem, operation: "claim" | "complete") => {
      setPendingItemId(item.id);
      setActionError(undefined);
      try {
        const requestId = crypto.randomUUID();
        if (operation === "claim") {
          await claimInboxItem(
            create(ClaimInboxItemRequestSchema, {
              requestId,
              inboxItemId: item.id,
            }),
          );
        } else {
          await completeInboxItem(
            create(CompleteInboxItemRequestSchema, {
              requestId,
              inboxItemId: item.id,
            }),
          );
        }
        await snapshot.refresh("retrying");
      } catch (error) {
        setActionError(
          error instanceof Error
            ? error.message
            : `Could not ${operation} Inbox item.`,
        );
      } finally {
        setPendingItemId(undefined);
      }
    },
    [snapshot],
  );

  const open = useCallback(
    async (item: InboxItem): Promise<InboxDestination> => {
      setActionError(undefined);
      try {
        return await resolveInboxDestination(item);
      } catch (error) {
        const message =
          error instanceof Error ? error.message : "Could not open Inbox item.";
        setActionError(message);
        throw error;
      }
    },
    [],
  );

  return {
    status: snapshot.state.status,
    data: snapshot.state.data,
    error: snapshot.state.failure?.message,
    refresh: () => snapshot.refresh("retrying"),
    claim: (item: InboxItem) => mutate(item, "claim"),
    complete: (item: InboxItem) => mutate(item, "complete"),
    open,
    pendingItemId,
    actionError,
    canClaim: (item: InboxItem) => item.state === InboxState.UNREAD,
    canComplete: (item: InboxItem) => item.state === InboxState.CLAIMED,
  };
}
