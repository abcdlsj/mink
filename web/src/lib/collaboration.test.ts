import { describe, expect, it, vi } from "vitest";
import type { Message } from "../gen/sumi/space/v1/space_pb";
import {
  MESSAGE_PAGE_BATCH,
  MESSAGE_PAGE_LIMIT,
  PayloadRequestLifecycle,
  loadMessagePages,
  mergeMessages,
} from "./collaboration";

describe("PayloadRequestLifecycle", () => {
  it("reuses an ID for an unchanged Create Group retry and rotates after editing", () => {
    const ids = ["group-1", "group-2", "group-3"];
    const lifecycle = new PayloadRequestLifecycle<{ name: string }>(
      JSON.stringify,
      () => ids.shift()!,
    );

    const first = lifecycle.sync({ name: "Release" });
    expect(lifecycle.sync({ name: "Release" })).toBe(first);
    const edited = lifecycle.sync({ name: "Release room" });
    expect(edited).not.toBe(first);
    expect(lifecycle.sync({ name: "Release room" })).toBe(edited);
  });

  it("rotates IDs when add/remove/archive payload or target changes", () => {
    let counter = 0;
    const lifecycle = new PayloadRequestLifecycle<{
      action: string;
      space: string;
      member?: string;
    }>(JSON.stringify, () => `mutation-${++counter}`);

    const add = lifecycle.sync({
      action: "add",
      space: "space-a",
      member: "human-a",
    });
    expect(
      lifecycle.sync({ action: "add", space: "space-a", member: "human-a" }),
    ).toBe(add);
    const changedMember = lifecycle.sync({
      action: "add",
      space: "space-a",
      member: "agent-b",
    });
    expect(changedMember).not.toBe(add);
    const changedAction = lifecycle.sync({
      action: "remove",
      space: "space-a",
      member: "agent-b",
    });
    expect(changedAction).not.toBe(changedMember);
    const changedTarget = lifecycle.sync({
      action: "archive",
      space: "space-b",
    });
    expect(changedTarget).not.toBe(changedAction);
    expect(lifecycle.sync({ action: "archive", space: "space-b" })).toBe(
      changedTarget,
    );
  });
});

describe("bounded message pagination", () => {
  it("stops after five 200-message pages and exposes Load more", async () => {
    const fetchPage = vi.fn(async (after: bigint, limit: number) =>
      Array.from({ length: limit }, (_, index) =>
        message(Number(after) + index + 1),
      ),
    );

    const result = await loadMessagePages(fetchPage);

    expect(fetchPage).toHaveBeenCalledTimes(MESSAGE_PAGE_BATCH);
    expect(fetchPage).toHaveBeenNthCalledWith(1, 0n, MESSAGE_PAGE_LIMIT);
    expect(result.messages).toHaveLength(
      MESSAGE_PAGE_BATCH * MESSAGE_PAGE_LIMIT,
    );
    expect(result.hasMore).toBe(true);
    expect(result.nextAfterSequence).toBe(1000n);
  });

  it("deduplicates by ID, sorts by sequence, and rejects sequence collisions", () => {
    expect(
      mergeMessages([message(2), message(1)], [message(2), message(3)]).map(
        (item) => item.id,
      ),
    ).toEqual(["message-1", "message-2", "message-3"]);
    expect(() =>
      mergeMessages([message(1)], [message(1, "different-message")]),
    ).toThrow(/share sequence/);
  });
});

function message(sequence: number, id = `message-${sequence}`): Message {
  return {
    id,
    spaceId: "space-a",
    threadRootMessageId: "",
    targetSequence: BigInt(sequence),
    body: `Message ${sequence}`,
    requestId: `request-${sequence}`,
  } as Message;
}
