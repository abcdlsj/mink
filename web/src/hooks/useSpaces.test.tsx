import { act, cleanup, renderHook, waitFor } from "@testing-library/react";
import { Code, ConnectError } from "@connectrpc/connect";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import {
  PrincipalKind,
  SpaceKind,
  type Principal,
  type Space,
} from "../gen/sumi/space/v1/space_pb";
import {
  createDM,
  createGroup,
  loadDirectory,
  type DirectorySnapshot,
} from "../lib/collaboration";
import { useSpaces } from "./useSpaces";

vi.mock("../lib/collaboration", () => ({
  loadDirectory: vi.fn(),
  createDM: vi.fn(),
  createGroup: vi.fn(),
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

const mockedLoadDirectory = vi.mocked(loadDirectory);
const mockedCreateDM = vi.mocked(createDM);
const mockedCreateGroup = vi.mocked(createGroup);

beforeEach(() => vi.clearAllMocks());
afterEach(cleanup);

describe("useSpaces request boundaries", () => {
  it("does not let a late prior-Human response overwrite the current directory", async () => {
    const pending = new Map<string, (snapshot: DirectorySnapshot) => void>();
    mockedLoadDirectory.mockImplementation(
      (humanId) =>
        new Promise((resolve) => {
          pending.set(humanId, resolve);
        }),
    );
    const hook = renderHook(({ humanId }) => useSpaces(humanId, true), {
      initialProps: { humanId: "human-a" },
    });
    await waitFor(() => expect(pending.has("human-a")).toBe(true));
    hook.rerender({ humanId: "human-b" });
    await waitFor(() => expect(pending.has("human-b")).toBe(true));

    await act(async () => pending.get("human-b")!(directory("B")));
    expect(hook.result.current.data?.organization.name).toBe("B");
    await act(async () => pending.get("human-a")!(directory("A")));
    expect(hook.result.current.data?.organization.name).toBe("B");
  });

  it("keeps the prior directory and marks it stale when Refresh fails", async () => {
    mockedLoadDirectory
      .mockResolvedValueOnce(directory("Current"))
      .mockRejectedValueOnce(new Error("refresh offline"));
    const hook = renderHook(() => useSpaces("human-a", true));
    await waitFor(() => expect(hook.result.current.status).toBe("ready"));

    await act(async () => hook.result.current.refresh());
    expect(hook.result.current.status).toBe("stale");
    expect(hook.result.current.data?.organization.name).toBe("Current");
    expect(hook.result.current.error).toBe("refresh offline");
  });

  for (const code of [
    Code.Unauthenticated,
    Code.PermissionDenied,
    Code.NotFound,
  ]) {
    it(`clears directory facts when Refresh fails with ${Code[code]}`, async () => {
      mockedLoadDirectory
        .mockResolvedValueOnce(directory("Current"))
        .mockRejectedValueOnce(new ConnectError("access lost", code));
      const hook = renderHook(() => useSpaces("human-a", true));
      await waitFor(() => expect(hook.result.current.status).toBe("ready"));
      await act(async () => hook.result.current.refresh());
      expect(hook.result.current.status).toBe("error");
      expect(hook.result.current.data).toBeUndefined();
      expect(hook.result.current.accessInvalidated).toBe(true);
      expect(hook.result.current.authenticationInvalidated).toBe(
        code === Code.Unauthenticated ? true : undefined,
      );
    });
  }

  it("keeps directory facts stale when Refresh is Unavailable", async () => {
    mockedLoadDirectory
      .mockResolvedValueOnce(directory("Current"))
      .mockRejectedValueOnce(
        new ConnectError("temporarily unavailable", Code.Unavailable),
      );
    const hook = renderHook(() => useSpaces("human-a", true));
    await waitFor(() => expect(hook.result.current.status).toBe("ready"));
    await act(async () => hook.result.current.refresh());
    expect(hook.result.current.status).toBe("stale");
    expect(hook.result.current.data?.organization.name).toBe("Current");
    expect(hook.result.current.accessInvalidated).toBeUndefined();
  });

  for (const kind of ["dm", "group"] as const) {
    it(`does not merge a late Create ${kind} result into a new Human directory`, async () => {
      mockedLoadDirectory.mockImplementation((humanId) =>
        Promise.resolve(directory(humanId)),
      );
      const pending = deferred<Space>();
      if (kind === "dm") mockedCreateDM.mockReturnValueOnce(pending.promise);
      else mockedCreateGroup.mockReturnValueOnce(pending.promise);
      const hook = renderHook(({ humanId }) => useSpaces(humanId, true), {
        initialProps: { humanId: "human-a" },
      });
      await waitFor(() =>
        expect(hook.result.current.data?.organization.name).toBe("human-a"),
      );
      let completion!: Promise<Space>;
      act(() => {
        completion =
          kind === "dm"
            ? hook.result.current.createDirectMessage("request-dm", principal())
            : hook.result.current.createGroupSpace("request-group", "Group");
      });
      hook.rerender({ humanId: "human-b" });
      await waitFor(() =>
        expect(hook.result.current.data?.organization.name).toBe("human-b"),
      );
      const loadCount = mockedLoadDirectory.mock.calls.length;
      pending.resolve(space("old-human-space"));
      let error: unknown;
      await act(async () => {
        try {
          await completion;
        } catch (cause) {
          error = cause;
        }
      });
      expect(error).toMatchObject({ name: "AbortError" });
      expect(hook.result.current.data?.organization.name).toBe("human-b");
      expect(hook.result.current.data?.spaces).toHaveLength(0);
      expect(mockedLoadDirectory).toHaveBeenCalledTimes(loadCount);
    });
  }
});

function directory(name: string): DirectorySnapshot {
  return {
    organization: {
      id: `org-${name}`,
      name,
      bootstrapHumanId: "human-a",
    } as DirectorySnapshot["organization"],
    humans: [],
    agents: [],
    spaces: [],
    createSpace: { status: "allowed" },
  };
}

function space(id: string): Space {
  return {
    id,
    organizationId: "org",
    kind: SpaceKind.GROUP,
    name: id,
  } as Space;
}

function principal(): Principal {
  return { kind: PrincipalKind.AGENT, id: "agent" } as Principal;
}

function deferred<T>() {
  let resolve!: (value: T) => void;
  const promise = new Promise<T>((done) => {
    resolve = done;
  });
  return { promise, resolve };
}
