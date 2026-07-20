import { act, cleanup, renderHook, waitFor } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import {
  SpaceKind,
  type Message,
  type Space,
} from "../gen/sumi/space/v1/space_pb";
import {
  loadConversation,
  loadThread,
  type ConversationSnapshot,
  type ThreadSnapshot,
} from "../lib/collaboration";
import { useConversation } from "./useConversation";

vi.mock("../lib/collaboration", () => ({
  loadConversation: vi.fn(),
  loadThread: vi.fn(),
  loadMoreConversationMessages: vi.fn(),
  loadMoreThreadMessages: vi.fn(),
  addSpaceMember: vi.fn(),
  removeSpaceMember: vi.fn(),
  setSpaceArchived: vi.fn(),
  sendSpaceMessage: vi.fn(),
  sendThreadMessage: vi.fn(),
  mergeMessages: vi.fn((current, incoming) => [...current, ...incoming]),
  collaborationErrorMessage: (error: unknown) =>
    error instanceof Error ? error.message : "collaboration failed",
}));

const mockedLoadConversation = vi.mocked(loadConversation);
const mockedLoadThread = vi.mocked(loadThread);

beforeEach(() => vi.clearAllMocks());
afterEach(cleanup);

describe("useConversation target generations", () => {
  it("ignores a late response from the previously selected Space", async () => {
    const pending = new Map<string, (snapshot: ConversationSnapshot) => void>();
    mockedLoadConversation.mockImplementation(
      (_humanId, spaceId) =>
        new Promise((resolve) => pending.set(spaceId, resolve)),
    );
    const hook = renderHook(
      ({ spaceId }) => useConversation("human", spaceId, undefined),
      { initialProps: { spaceId: "space-a" } },
    );
    await waitFor(() => expect(pending.has("space-a")).toBe(true));
    hook.rerender({ spaceId: "space-b" });
    await waitFor(() => expect(pending.has("space-b")).toBe(true));

    await act(async () => pending.get("space-b")!(conversation("space-b")));
    expect(hook.result.current.conversation.data?.space.id).toBe("space-b");
    await act(async () => pending.get("space-a")!(conversation("space-a")));
    expect(hook.result.current.conversation.data?.space.id).toBe("space-b");
  });

  it("ignores a late response from the previously opened Thread", async () => {
    mockedLoadConversation.mockResolvedValue(conversation("space-a"));
    const pending = new Map<string, (snapshot: ThreadSnapshot) => void>();
    mockedLoadThread.mockImplementation(
      (root) => new Promise((resolve) => pending.set(root.id, resolve)),
    );
    const rootA = message("root-a");
    const rootB = message("root-b");
    const hook = renderHook(
      ({ root }) => useConversation("human", "space-a", root),
      { initialProps: { root: rootA } },
    );
    await waitFor(() => expect(pending.has("root-a")).toBe(true));
    hook.rerender({ root: rootB });
    await waitFor(() => expect(pending.has("root-b")).toBe(true));

    await act(async () => pending.get("root-b")!(thread(rootB)));
    expect(hook.result.current.thread.data?.root.id).toBe("root-b");
    await act(async () => pending.get("root-a")!(thread(rootA)));
    expect(hook.result.current.thread.data?.root.id).toBe("root-b");
  });
});

function conversation(id: string): ConversationSnapshot {
  return {
    space: {
      id,
      organizationId: "org",
      kind: SpaceKind.GROUP,
      name: id,
    } as Space,
    memberships: [],
    messages: [],
    permissions: {
      members: { status: "allowed" },
      archive: { status: "allowed" },
      send: { status: "allowed" },
    },
    hasMore: false,
    nextAfterSequence: 0n,
  };
}

function message(id: string): Message {
  return {
    id,
    spaceId: "space-a",
    threadRootMessageId: "",
    targetSequence: 1n,
    body: id,
    requestId: `request-${id}`,
  } as Message;
}

function thread(root: Message): ThreadSnapshot {
  return { root, replies: [], hasMore: false, nextAfterSequence: 0n };
}
