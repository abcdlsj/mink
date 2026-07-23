import { create } from "@bufbuild/protobuf";
import { Code, ConnectError } from "@connectrpc/connect";
import { act, cleanup, renderHook, waitFor } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import {
  ClaimInboxItemResponseSchema,
  InboxItemSchema,
  InboxState,
  ListInboxItemsResponseSchema,
} from "../gen/sumi/inbox/v1/inbox_pb";
import * as inbox from "../lib/inbox";
import { useInbox } from "./useInbox";

afterEach(() => {
  cleanup();
  vi.restoreAllMocks();
});

describe("useInbox", () => {
  it("loads Human message attention, claims it, and clears revoked access", async () => {
    const item = create(InboxItemSchema, {
      id: crypto.randomUUID(),
      state: InboxState.UNREAD,
    });
    vi.spyOn(inbox, "listInboxItems")
      .mockResolvedValueOnce(
        create(ListInboxItemsResponseSchema, { items: [item] }),
      )
      .mockResolvedValueOnce(create(ListInboxItemsResponseSchema))
      .mockRejectedValueOnce(
        new ConnectError("revoked", Code.PermissionDenied),
      );
    vi.spyOn(inbox, "claimInboxItem").mockResolvedValueOnce(
      create(ClaimInboxItemResponseSchema, { item }),
    );
    const hook = renderHook(() => useInbox());

    await waitFor(() => expect(hook.result.current.status).toBe("ready"));
    expect(hook.result.current.data?.items).toEqual([item]);
    await act(async () => hook.result.current.claim(item));
    expect(hook.result.current.data?.items).toEqual([]);
    await act(async () => hook.result.current.refresh());
    expect(hook.result.current.status).toBe("error");
    expect(hook.result.current.data).toBeUndefined();
  });

  it("aborts and ignores the old request when disabled", async () => {
    let signal: AbortSignal | undefined;
    vi.spyOn(inbox, "listInboxItems").mockImplementation((_, requestSignal) => {
      signal = requestSignal;
      return new Promise(() => {});
    });
    const hook = renderHook(({ enabled }) => useInbox(enabled), {
      initialProps: { enabled: true },
    });
    await waitFor(() => expect(signal).toBeDefined());
    hook.rerender({ enabled: false });
    expect(signal?.aborted).toBe(true);
    expect(hook.result.current.status).toBe("idle");
  });
});
