import { create } from "@bufbuild/protobuf";
import { Code, ConnectError } from "@connectrpc/connect";
import { act, cleanup, renderHook, waitFor } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import {
  WorkAttentionItemSchema,
  ListWorkAttentionItemsResponseSchema,
} from "../gen/sumi/inbox/v1/inbox_pb";
import * as inbox from "../lib/workAttention";
import { useWorkAttention } from "./useWorkAttention";

afterEach(() => {
  cleanup();
  vi.restoreAllMocks();
});

describe("useWorkAttention", () => {
  it("loads only server metadata, clears revoked access, and keeps offline data stale", async () => {
    const item = create(WorkAttentionItemSchema, {
      workId: "work",
      spaceId: "space",
      agentId: "agent",
      kind: "agent_exception",
      status: "claimed",
      reasonCode: "held_draft",
    });
    vi.spyOn(inbox, "listWorkAttentionItems")
      .mockResolvedValueOnce(
        create(ListWorkAttentionItemsResponseSchema, { items: [item] }),
      )
      .mockRejectedValueOnce(new ConnectError("revoked", Code.PermissionDenied))
      .mockResolvedValueOnce(
        create(ListWorkAttentionItemsResponseSchema, { items: [item] }),
      )
      .mockRejectedValueOnce(new ConnectError("offline", Code.Unavailable));
    const hook = renderHook(() => useWorkAttention());

    await waitFor(() => expect(hook.result.current.status).toBe("ready"));
    expect(hook.result.current.data?.items).toEqual([item]);
    await act(async () => hook.result.current.refresh());
    expect(hook.result.current.status).toBe("error");
    expect(hook.result.current.data).toBeUndefined();
    await act(async () => hook.result.current.refresh());
    await act(async () => hook.result.current.refresh());
    expect(hook.result.current.status).toBe("stale");
    expect(hook.result.current.data?.items).toEqual([item]);
  });

  it("aborts and ignores the old request when disabled", async () => {
    let signal: AbortSignal | undefined;
    vi.spyOn(inbox, "listWorkAttentionItems").mockImplementation(
      (_, requestSignal) => {
        signal = requestSignal;
        return new Promise(() => {});
      },
    );
    const hook = renderHook(({ enabled }) => useWorkAttention(enabled), {
      initialProps: { enabled: true },
    });
    await waitFor(() => expect(signal).toBeDefined());
    hook.rerender({ enabled: false });
    expect(signal?.aborted).toBe(true);
    expect(hook.result.current.status).toBe("idle");
  });
});
