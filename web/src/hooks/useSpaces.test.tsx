import { act, cleanup, renderHook, waitFor } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { loadDirectory, type DirectorySnapshot } from "../lib/collaboration";
import { useSpaces } from "./useSpaces";

vi.mock("../lib/collaboration", () => ({
  loadDirectory: vi.fn(),
  createDM: vi.fn(),
  createGroup: vi.fn(),
  collaborationErrorMessage: (error: unknown) =>
    error instanceof Error ? error.message : "collaboration failed",
}));

const mockedLoadDirectory = vi.mocked(loadDirectory);

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
