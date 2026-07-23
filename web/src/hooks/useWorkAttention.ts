import { create } from "@bufbuild/protobuf";
import { Code, ConnectError } from "@connectrpc/connect";
import { useCallback } from "react";
import {
  ListWorkAttentionItemsRequestSchema,
  type WorkAttentionItem,
} from "../gen/sumi/inbox/v1/inbox_pb";
import { listWorkAttentionItems } from "../lib/workAttention";
import { useRemoteSnapshot, type SnapshotFailure } from "./useRemoteSnapshot";

export type WorkAttentionSnapshot = { items: WorkAttentionItem[] };

export function useWorkAttention(enabled = true) {
  const load = useCallback(
    async (signal: AbortSignal): Promise<WorkAttentionSnapshot> => {
      const response = await listWorkAttentionItems(
        create(ListWorkAttentionItemsRequestSchema, { limit: 50 }),
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
          : "Could not load Work Attention.",
      discard: code === Code.Unauthenticated || code === Code.PermissionDenied,
    };
  }, []);
  const snapshot = useRemoteSnapshot({
    enabled,
    targetId: "work-attention",
    load,
    classifyError,
  });
  return {
    status: snapshot.state.status,
    data: snapshot.state.data,
    error: snapshot.state.failure?.message,
    refresh: () => snapshot.refresh("retrying"),
  };
}
