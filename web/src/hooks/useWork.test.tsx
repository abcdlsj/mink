import { create } from "@bufbuild/protobuf";
import { Code, ConnectError } from "@connectrpc/connect";
import { act, cleanup, renderHook, waitFor } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import {
  AssignWorkResponseSchema,
  CreateWorkResponseSchema,
  GetWorkResponseSchema,
  ListWorksResponseSchema,
  RequestApprovalResponseSchema,
  ResolveApprovalResponseSchema,
  TransitionWorkResponseSchema,
  WorkSchema,
} from "../gen/sumi/work/v1/work_pb";
import * as work from "../lib/work";
import { useWork } from "./useWork";

afterEach(() => {
  cleanup();
  vi.restoreAllMocks();
});

describe("useWork", () => {
  it("loads, appends opaque cursor pages in server order, and resets pagination on refresh", async () => {
    const list = vi
      .spyOn(work, "listWorks")
      .mockResolvedValueOnce(page("first", "opaque-next"))
      .mockResolvedValueOnce(page("second", ""))
      .mockResolvedValueOnce(page("refreshed", "fresh-next"));
    const detail = create(GetWorkResponseSchema);
    const get = vi.spyOn(work, "getWork").mockResolvedValue(detail);
    const hook = renderHook(() => useWork("work-1"));

    expect(hook.result.current.status).toBe("loading");
    await waitFor(() => expect(hook.result.current.status).toBe("ready"));
    expect(hook.result.current.data?.detail).toBe(detail);
    expect(get).toHaveBeenCalledWith(
      expect.objectContaining({ workId: "work-1" }),
      expect.any(AbortSignal),
    );
    await act(async () => hook.result.current.loadMore());
    expect(hook.result.current.data?.works.map((item) => item.id)).toEqual([
      "first",
      "second",
    ]);
    expect(list.mock.calls[1]?.[0].cursor).toBe("opaque-next");
    expect(list.mock.calls[1]?.[1]).toBeInstanceOf(AbortSignal);

    await act(async () => hook.result.current.refresh());
    expect(list.mock.calls[2]?.[0].cursor).toBe("");
    expect(hook.result.current.data?.works.map((item) => item.id)).toEqual([
      "refreshed",
    ]);
  });

  it("aborts old snapshot and page requests and ignores their late results", async () => {
    const first = deferred<ReturnType<typeof page>>();
    const second = deferred<ReturnType<typeof page>>();
    const latePage = deferred<ReturnType<typeof page>>();
    const signals: AbortSignal[] = [];
    const list = vi
      .spyOn(work, "listWorks")
      .mockImplementationOnce((_, signal) => {
        signals.push(signal!);
        return first.promise;
      })
      .mockImplementationOnce((_, signal) => {
        signals.push(signal!);
        return second.promise;
      })
      .mockImplementationOnce((_, signal) => {
        signals.push(signal!);
        return latePage.promise;
      });
    vi.spyOn(work, "getWork").mockResolvedValue({
      $typeName: "sumi.work.v1.GetWorkResponse",
    });
    const hook = renderHook(({ id }) => useWork(id), {
      initialProps: { id: "work-a" },
    });
    await waitFor(() => expect(signals).toHaveLength(1));

    hook.rerender({ id: "work-b" });
    await waitFor(() => expect(signals).toHaveLength(2));
    expect(signals[0]?.aborted).toBe(true);
    second.resolve(page("current", "page-next"));
    await waitFor(() => expect(hook.result.current.status).toBe("ready"));

    let pagePromise!: Promise<void>;
    act(() => {
      pagePromise = hook.result.current.loadMore();
    });
    await waitFor(() => expect(signals).toHaveLength(3));
    let refreshPromise!: Promise<void>;
    vi.spyOn(work, "listWorks").mockResolvedValueOnce(page("fresh", ""));
    act(() => {
      refreshPromise = hook.result.current.refresh();
    });
    expect(signals[2]?.aborted).toBe(true);
    latePage.resolve(page("late-page", ""));
    first.resolve(page("late-target", ""));
    await act(async () => {
      await Promise.all([pagePromise, refreshPromise]);
    });
    expect(hook.result.current.data?.works[0]?.id).toBe("fresh");

    const activeSignal = list.mock.calls.at(-1)?.[1];
    hook.unmount();
    expect(activeSignal?.aborted).toBe(true);
  });

  it("exposes idle, refreshing, inaccessible error, and unavailable stale states", async () => {
    const refresh = deferred<ReturnType<typeof page>>();
    const list = vi
      .spyOn(work, "listWorks")
      .mockResolvedValueOnce(page("current", ""))
      .mockReturnValueOnce(refresh.promise)
      .mockRejectedValueOnce(new ConnectError("denied", Code.PermissionDenied))
      .mockResolvedValueOnce(page("current", ""))
      .mockRejectedValueOnce(new ConnectError("offline", Code.Unavailable));
    const hook = renderHook(({ enabled }) => useWork(undefined, enabled), {
      initialProps: { enabled: true },
    });
    await waitFor(() => expect(hook.result.current.status).toBe("ready"));

    let refreshing!: Promise<void>;
    act(() => {
      refreshing = hook.result.current.refresh();
    });
    expect(hook.result.current.status).toBe("refreshing");
    refresh.resolve(page("new", ""));
    await act(async () => refreshing);
    await act(async () => hook.result.current.refresh());
    expect(hook.result.current.status).toBe("error");
    expect(hook.result.current.data).toBeUndefined();

    await act(async () => hook.result.current.refresh());
    await act(async () => hook.result.current.refresh());
    expect(hook.result.current.status).toBe("stale");
    expect(hook.result.current.data?.works[0]?.id).toBe("current");
    hook.rerender({ enabled: false });
    expect(hook.result.current.status).toBe("idle");
    expect(list).toHaveBeenCalledTimes(5);
  });

  it("keeps request IDs across same-payload failures and rotates an edited payload", async () => {
    vi.spyOn(work, "listWorks").mockResolvedValue(page("server", ""));
    const failure = new ConnectError("conflict", Code.AlreadyExists);
    const response = create(CreateWorkResponseSchema);
    const createWork = vi
      .spyOn(work, "createWork")
      .mockRejectedValueOnce(failure)
      .mockRejectedValueOnce(failure)
      .mockResolvedValueOnce(response);
    const hook = renderHook(() => useWork(undefined));
    await waitFor(() => expect(hook.result.current.status).toBe("ready"));

    await expectCreateFailure(hook.result.current.create, "one", failure);
    const firstId = createWork.mock.calls[0]?.[0].requestId;
    await expectCreateFailure(hook.result.current.create, "two", failure);
    const editedId = createWork.mock.calls[1]?.[0].requestId;
    expect(editedId).not.toBe(firstId);
    let result!: unknown;
    await act(async () => {
      result = await hook.result.current.create(createPayload("two"));
    });
    expect(result).toBe(response);
    expect(createWork.mock.calls[2]?.[0].requestId).toBe(editedId);
    expect(hook.result.current.action).toEqual({ pending: false });
    expect(hook.result.current.data?.works[0]?.id).toBe("server");
  });

  it("does not let a late mutation failure pollute a newly selected Work", async () => {
    const list = vi
      .spyOn(work, "listWorks")
      .mockResolvedValue(page("canonical", ""));
    vi.spyOn(work, "getWork").mockResolvedValue(create(GetWorkResponseSchema));
    const lateResponse = create(CreateWorkResponseSchema);
    const pending = deferred<typeof lateResponse>();
    vi.spyOn(work, "createWork").mockReturnValue(pending.promise);
    const hook = renderHook(({ id }) => useWork(id), {
      initialProps: { id: "work-a" },
    });
    await waitFor(() => expect(hook.result.current.status).toBe("ready"));
    let completion!: Promise<unknown>;
    act(() => {
      completion = hook.result.current.create(createPayload("old"));
    });
    expect(hook.result.current.action.pending).toBe(true);

    hook.rerender({ id: "work-b" });
    await waitFor(() => expect(hook.result.current.status).toBe("ready"));
    const calls = list.mock.calls.length;
    const failure = new ConnectError("late", Code.Aborted);
    pending.reject(failure);
    await act(async () => {
      await expect(completion).rejects.toBe(failure);
    });
    expect(hook.result.current.action).toEqual({ pending: false });
    expect(list).toHaveBeenCalledTimes(calls);
  });

  it("refreshes after every typed mutation failure and success without optimistic facts", async () => {
    const failure = new ConnectError("state changed", Code.FailedPrecondition);
    const list = vi
      .spyOn(work, "listWorks")
      .mockResolvedValue(page("canonical", ""));
    const assignResponse = create(AssignWorkResponseSchema);
    const transitionResponse = create(TransitionWorkResponseSchema);
    const requestResponse = create(RequestApprovalResponseSchema);
    const resolveResponse = create(ResolveApprovalResponseSchema);
    const assign = vi
      .spyOn(work, "assignWork")
      .mockRejectedValueOnce(failure)
      .mockResolvedValueOnce(assignResponse);
    const transition = vi
      .spyOn(work, "transitionWork")
      .mockRejectedValueOnce(failure)
      .mockResolvedValueOnce(transitionResponse);
    const request = vi
      .spyOn(work, "requestWorkApproval")
      .mockRejectedValueOnce(failure)
      .mockResolvedValueOnce(requestResponse);
    const resolve = vi
      .spyOn(work, "resolveWorkApproval")
      .mockRejectedValueOnce(failure)
      .mockResolvedValueOnce(resolveResponse);
    const hook = renderHook(() => useWork(undefined));
    await waitFor(() => expect(hook.result.current.status).toBe("ready"));

    const actions = [
      {
        invoke: () =>
          hook.result.current.assign({
            workId: "work",
            role: 0,
            agentId: "agent",
          }),
        calls: assign.mock.calls,
        response: assignResponse,
      },
      {
        invoke: () =>
          hook.result.current.transition({
            workId: "work",
            toState: 0,
            reason: "reason",
            result: "",
            criterionResults: [],
          }),
        calls: transition.mock.calls,
        response: transitionResponse,
      },
      {
        invoke: () =>
          hook.result.current.requestApproval({
            workId: "work",
            question: "approve?",
          }),
        calls: request.mock.calls,
        response: requestResponse,
      },
      {
        invoke: () =>
          hook.result.current.resolveApproval({
            approvalId: "approval",
            decision: 0,
            note: "note",
          }),
        calls: resolve.mock.calls,
        response: resolveResponse,
      },
    ];
    for (const action of actions) {
      const refreshes = list.mock.calls.length;
      await act(async () => {
        await expect(action.invoke()).rejects.toBe(failure);
      });
      expect(hook.result.current.action).toEqual({
        pending: false,
        error: failure,
      });
      expect(hook.result.current.data?.works[0]?.id).toBe("canonical");
      expect(list).toHaveBeenCalledTimes(refreshes + 1);
      let response!: unknown;
      await act(async () => {
        response = await action.invoke();
      });
      expect(response).toBe(action.response);
      expect(action.calls[1]?.[0].requestId).toBe(
        action.calls[0]?.[0].requestId,
      );
      expect(hook.result.current.action).toEqual({ pending: false });
      expect(list).toHaveBeenCalledTimes(refreshes + 2);
    }
  });
});

function page(id: string, nextCursor: string) {
  return create(ListWorksResponseSchema, {
    works: [create(WorkSchema, { id })],
    nextCursor,
  });
}

function createPayload(goal: string) {
  return {
    parentWorkId: "",
    sourceMessageId: "",
    sourceSpaceId: "",
    sourceTargetSequence: 0n,
    goal,
    constraints: [],
    acceptanceCriteria: [],
  };
}

async function expectCreateFailure(
  createWork: ReturnType<typeof useWork>["create"],
  goal: string,
  failure: ConnectError,
) {
  let completion!: Promise<unknown>;
  act(() => {
    completion = createWork(createPayload(goal));
  });
  expect(completion).toBeDefined();
  await act(async () => {
    await expect(completion).rejects.toBe(failure);
  });
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
