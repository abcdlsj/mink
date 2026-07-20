import { act, cleanup, renderHook, waitFor } from "@testing-library/react";
import { Code, ConnectError } from "@connectrpc/connect";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import {
  PrincipalKind,
  SpaceKind,
  type Message,
  type Principal,
  type Space,
} from "../gen/sumi/space/v1/space_pb";
import {
  addSpaceMember,
  loadConversation,
  loadMoreConversationMessages,
  loadMoreThreadMessages,
  loadThread,
  removeSpaceMember,
  sendSpaceMessage,
  sendThreadMessage,
  setSpaceArchived,
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
  isInaccessibleCollaborationError: (error: unknown) => {
    const code = ConnectError.from(error).code;
    return (
      code === Code.Unauthenticated ||
      code === Code.PermissionDenied ||
      code === Code.NotFound
    );
  },
  isUnauthenticatedCollaborationError: (error: unknown) =>
    ConnectError.from(error).code === Code.Unauthenticated,
}));

const mockedLoadConversation = vi.mocked(loadConversation);
const mockedLoadThread = vi.mocked(loadThread);
const mockedLoadMoreConversation = vi.mocked(loadMoreConversationMessages);
const mockedLoadMoreThread = vi.mocked(loadMoreThreadMessages);
const mockedAddMember = vi.mocked(addSpaceMember);
const mockedRemoveMember = vi.mocked(removeSpaceMember);
const mockedSetArchived = vi.mocked(setSpaceArchived);
const mockedSendMain = vi.mocked(sendSpaceMessage);
const mockedSendReply = vi.mocked(sendThreadMessage);

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

  for (const code of [
    Code.Unauthenticated,
    Code.PermissionDenied,
    Code.NotFound,
  ]) {
    it(`clears an inaccessible Space snapshot on ${Code[code]}`, async () => {
      mockedLoadConversation
        .mockResolvedValueOnce(conversation("space-a"))
        .mockRejectedValueOnce(new ConnectError("access lost", code));
      const hook = renderHook(() =>
        useConversation("human", "space-a", undefined),
      );
      await waitFor(() =>
        expect(hook.result.current.conversation.status).toBe("ready"),
      );

      await act(async () => hook.result.current.refresh());
      expect(hook.result.current.conversation.status).toBe("error");
      expect(hook.result.current.conversation.data).toBeUndefined();
      expect(hook.result.current.conversation.inaccessibleTargetId).toBe(
        "space-a",
      );
      expect(hook.result.current.conversation.authenticationInvalidated).toBe(
        code === Code.Unauthenticated ? true : undefined,
      );
    });
  }

  it("keeps a Space snapshot stale on transient Unavailable refresh", async () => {
    mockedLoadConversation
      .mockResolvedValueOnce(conversation("space-a"))
      .mockRejectedValueOnce(
        new ConnectError("temporarily unavailable", Code.Unavailable),
      );
    const hook = renderHook(() =>
      useConversation("human", "space-a", undefined),
    );
    await waitFor(() =>
      expect(hook.result.current.conversation.status).toBe("ready"),
    );
    await act(async () => hook.result.current.refresh());
    expect(hook.result.current.conversation.status).toBe("stale");
    expect(hook.result.current.conversation.data?.space.id).toBe("space-a");
    expect(
      hook.result.current.conversation.inaccessibleTargetId,
    ).toBeUndefined();
  });

  for (const code of [
    Code.Unauthenticated,
    Code.PermissionDenied,
    Code.NotFound,
  ]) {
    it(`clears main messages when Load more fails with ${Code[code]}`, async () => {
      mockedLoadConversation.mockResolvedValue(conversation("space-a"));
      const pending = deferred<ConversationSnapshot>();
      mockedLoadMoreConversation.mockReturnValueOnce(pending.promise);
      const hook = renderHook(() =>
        useConversation("human", "space-a", undefined),
      );
      await waitFor(() =>
        expect(hook.result.current.conversation.status).toBe("ready"),
      );
      let completion!: Promise<void>;
      act(() => {
        completion = hook.result.current.loadMore();
      });
      pending.reject(new ConnectError("access lost", code));
      await act(async () => completion);
      expect(hook.result.current.conversation.status).toBe("error");
      expect(hook.result.current.conversation.data).toBeUndefined();
      expect(hook.result.current.conversation.inaccessibleTargetId).toBe(
        "space-a",
      );
    });
  }

  it("keeps main messages stale when Load more is Unavailable", async () => {
    mockedLoadConversation.mockResolvedValue(conversation("space-a"));
    mockedLoadMoreConversation.mockRejectedValueOnce(
      new ConnectError("temporarily unavailable", Code.Unavailable),
    );
    const hook = renderHook(() =>
      useConversation("human", "space-a", undefined),
    );
    await waitFor(() =>
      expect(hook.result.current.conversation.status).toBe("ready"),
    );
    await act(async () => hook.result.current.loadMore());
    expect(hook.result.current.conversation.status).toBe("stale");
    expect(hook.result.current.conversation.data?.space.id).toBe("space-a");
  });

  for (const code of [
    Code.Unauthenticated,
    Code.PermissionDenied,
    Code.NotFound,
  ]) {
    it(`clears the Space and Thread when Thread refresh fails with ${Code[code]}`, async () => {
      const root = message("root-a");
      mockedLoadConversation.mockResolvedValue(conversation("space-a"));
      mockedLoadThread
        .mockResolvedValueOnce(thread(root))
        .mockRejectedValueOnce(new ConnectError("access lost", code));
      const hook = renderHook(() => useConversation("human", "space-a", root));
      await waitFor(() =>
        expect(hook.result.current.thread.status).toBe("ready"),
      );
      await act(async () => hook.result.current.refreshThread());
      expect(hook.result.current.thread.data).toBeUndefined();
      expect(hook.result.current.thread.inaccessibleTargetId).toBe("root-a");
      expect(hook.result.current.thread.inaccessibleSpaceId).toBe("space-a");
      expect(hook.result.current.conversation.data).toBeUndefined();
      expect(hook.result.current.conversation.inaccessibleTargetId).toBe(
        "space-a",
      );
    });
  }

  it("keeps Space and Thread snapshots stale when Thread refresh is Unavailable", async () => {
    const root = message("root-a");
    mockedLoadConversation.mockResolvedValue(conversation("space-a"));
    mockedLoadThread
      .mockResolvedValueOnce(thread(root))
      .mockRejectedValueOnce(
        new ConnectError("temporarily unavailable", Code.Unavailable),
      );
    const hook = renderHook(() => useConversation("human", "space-a", root));
    await waitFor(() =>
      expect(hook.result.current.thread.status).toBe("ready"),
    );
    await act(async () => hook.result.current.refreshThread());
    expect(hook.result.current.thread.status).toBe("stale");
    expect(hook.result.current.thread.data?.root.id).toBe("root-a");
    expect(hook.result.current.conversation.data?.space.id).toBe("space-a");
  });

  for (const code of [
    Code.Unauthenticated,
    Code.PermissionDenied,
    Code.NotFound,
  ]) {
    it(`clears Thread replies when Load more fails with ${Code[code]}`, async () => {
      const root = message("root-a");
      mockedLoadConversation.mockResolvedValue(conversation("space-a"));
      mockedLoadThread.mockResolvedValue(thread(root));
      const pending = deferred<ThreadSnapshot>();
      mockedLoadMoreThread.mockReturnValueOnce(pending.promise);
      const hook = renderHook(() => useConversation("human", "space-a", root));
      await waitFor(() =>
        expect(hook.result.current.thread.status).toBe("ready"),
      );
      let completion!: Promise<void>;
      act(() => {
        completion = hook.result.current.loadMoreThread();
      });
      pending.reject(new ConnectError("access lost", code));
      await act(async () => completion);
      expect(hook.result.current.thread.status).toBe("error");
      expect(hook.result.current.thread.data).toBeUndefined();
      expect(hook.result.current.thread.inaccessibleTargetId).toBe("root-a");
      expect(hook.result.current.thread.inaccessibleSpaceId).toBe("space-a");
      expect(hook.result.current.conversation.data).toBeUndefined();
      expect(hook.result.current.conversation.inaccessibleTargetId).toBe(
        "space-a",
      );
    });
  }

  it("keeps Thread replies stale when Load more is Unavailable", async () => {
    const root = message("root-a");
    mockedLoadConversation.mockResolvedValue(conversation("space-a"));
    mockedLoadThread.mockResolvedValue(thread(root));
    mockedLoadMoreThread.mockRejectedValueOnce(
      new ConnectError("temporarily unavailable", Code.Unavailable),
    );
    const hook = renderHook(() => useConversation("human", "space-a", root));
    await waitFor(() =>
      expect(hook.result.current.thread.status).toBe("ready"),
    );
    await act(async () => hook.result.current.loadMoreThread());
    expect(hook.result.current.thread.status).toBe("stale");
    expect(hook.result.current.thread.data?.root.id).toBe("root-a");
  });

  for (const action of ["add", "remove", "archive"] as const) {
    it(`does not let a switch-during-${action} completion refresh the old Space`, async () => {
      mockedLoadConversation.mockImplementation((_humanId, spaceId) =>
        Promise.resolve(conversation(spaceId)),
      );
      const pending = deferred<void>();
      if (action === "add")
        mockedAddMember.mockReturnValueOnce(pending.promise);
      if (action === "remove")
        mockedRemoveMember.mockReturnValueOnce(pending.promise);
      if (action === "archive")
        mockedSetArchived.mockReturnValueOnce(pending.promise);
      const hook = renderHook(
        ({ spaceId }) => useConversation("human", spaceId, undefined),
        { initialProps: { spaceId: "space-a" } },
      );
      await waitFor(() =>
        expect(hook.result.current.conversation.data?.space.id).toBe("space-a"),
      );

      let completion!: Promise<void>;
      act(() => {
        completion =
          action === "add"
            ? hook.result.current.addMember("request-add", principal())
            : action === "remove"
              ? hook.result.current.removeMember("request-remove", principal())
              : hook.result.current.setArchived("request-archive", true);
      });
      hook.rerender({ spaceId: "space-b" });
      await waitFor(() =>
        expect(hook.result.current.conversation.data?.space.id).toBe("space-b"),
      );
      const loadCount = mockedLoadConversation.mock.calls.length;
      pending.resolve();
      await act(async () => completion);

      expect(hook.result.current.conversation.data?.space.id).toBe("space-b");
      expect(mockedLoadConversation).toHaveBeenCalledTimes(loadCount);
    });
  }

  it("does not let a switch-during-main-send refresh the old Space", async () => {
    mockedLoadConversation.mockImplementation((_humanId, spaceId) =>
      Promise.resolve(conversation(spaceId)),
    );
    const pending = deferred<Message>();
    mockedSendMain.mockReturnValueOnce(pending.promise);
    const hook = renderHook(
      ({ spaceId }) => useConversation("human", spaceId, undefined),
      { initialProps: { spaceId: "space-a" } },
    );
    await waitFor(() =>
      expect(hook.result.current.conversation.data?.space.id).toBe("space-a"),
    );
    let completion!: Promise<void>;
    act(() => {
      completion = hook.result.current.sendMain("request-send", "body");
    });
    hook.rerender({ spaceId: "space-b" });
    await waitFor(() =>
      expect(hook.result.current.conversation.data?.space.id).toBe("space-b"),
    );
    const loadCount = mockedLoadConversation.mock.calls.length;
    pending.resolve(message("old-space-message"));
    await act(async () => completion);
    expect(hook.result.current.conversation.data?.space.id).toBe("space-b");
    expect(mockedLoadConversation).toHaveBeenCalledTimes(loadCount);
  });

  it("does not let a switch-during-reply refresh the old Thread", async () => {
    mockedLoadConversation.mockResolvedValue(conversation("space-a"));
    mockedLoadThread.mockImplementation((root) =>
      Promise.resolve(thread(root)),
    );
    const pending = deferred<Message>();
    mockedSendReply.mockReturnValueOnce(pending.promise);
    const rootA = message("root-a");
    const rootB = message("root-b");
    const hook = renderHook(
      ({ root }) => useConversation("human", "space-a", root),
      { initialProps: { root: rootA } },
    );
    await waitFor(() =>
      expect(hook.result.current.thread.data?.root.id).toBe("root-a"),
    );
    let completion!: Promise<void>;
    act(() => {
      completion = hook.result.current.sendReply("request-reply", "reply");
    });
    hook.rerender({ root: rootB });
    await waitFor(() =>
      expect(hook.result.current.thread.data?.root.id).toBe("root-b"),
    );
    const loadCount = mockedLoadThread.mock.calls.length;
    pending.resolve(message("old-thread-reply"));
    await act(async () => completion);
    expect(hook.result.current.thread.data?.root.id).toBe("root-b");
    expect(mockedLoadThread).toHaveBeenCalledTimes(loadCount);
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

function principal(): Principal {
  return { kind: PrincipalKind.AGENT, id: "agent" } as Principal;
}

function deferred<T>() {
  let resolve!: (value: T) => void;
  let reject!: (reason: unknown) => void;
  const promise = new Promise<T>((done, fail) => {
    resolve = done;
    reject = fail;
  });
  return { promise, resolve, reject };
}
