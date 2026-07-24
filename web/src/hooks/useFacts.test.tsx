import { act, cleanup, renderHook, waitFor } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { loadFacts, type FactsSnapshot } from "../lib/facts";
import { useFacts } from "./useFacts";

vi.mock("../lib/facts", async (loadOriginal) => {
  const original = await loadOriginal<typeof import("../lib/facts")>();
  return { ...original, loadFacts: vi.fn() };
});

const mockedLoadFacts = vi.mocked(loadFacts);

beforeEach(() => vi.clearAllMocks());
afterEach(cleanup);

describe("useFacts request lifecycle", () => {
  it("keeps the newest refresh when an older response arrives last", async () => {
    mockedLoadFacts.mockResolvedValueOnce(facts("initial"));
    const first = deferred<FactsSnapshot>();
    const second = deferred<FactsSnapshot>();
    mockedLoadFacts
      .mockReturnValueOnce(first.promise)
      .mockReturnValueOnce(second.promise);
    const hook = renderHook(() => useFacts(true));
    await waitFor(() => expect(hook.result.current.status).toBe("ready"));

    let firstRefresh!: Promise<void>;
    let secondRefresh!: Promise<void>;
    act(() => {
      firstRefresh = hook.result.current.refresh();
      secondRefresh = hook.result.current.refresh();
    });
    second.resolve(facts("newest"));
    await act(async () => secondRefresh);
    first.resolve(facts("older"));
    await act(async () => firstRefresh);

    expect(hook.result.current.data?.agents[0]?.profile?.displayName).toBe(
      "newest",
    );
  });

  it("ignores a response after facts are disabled", async () => {
    const pending = deferred<FactsSnapshot>();
    mockedLoadFacts.mockReturnValueOnce(pending.promise);
    const hook = renderHook(({ enabled }) => useFacts(enabled), {
      initialProps: { enabled: true },
    });
    await waitFor(() => expect(mockedLoadFacts).toHaveBeenCalledOnce());
    hook.rerender({ enabled: false });
    expect(hook.result.current.status).toBe("idle");

    pending.resolve(facts("late"));
    await act(async () => pending.promise);
    expect(hook.result.current.status).toBe("idle");
    expect(hook.result.current.data).toBeUndefined();
  });
});

function facts(name: string): FactsSnapshot {
  return {
    agents: [
      { handle: name, profile: { displayName: name } },
    ] as FactsSnapshot["agents"],
    computers: [],
    placements: [],
  };
}

function deferred<T>() {
  let resolve!: (value: T) => void;
  const promise = new Promise<T>((done) => {
    resolve = done;
  });
  return { promise, resolve };
}
