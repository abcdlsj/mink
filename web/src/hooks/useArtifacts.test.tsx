import { create } from "@bufbuild/protobuf";
import { Code, ConnectError } from "@connectrpc/connect";
import { act, cleanup, renderHook, waitFor } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import {
  ArtifactSchema,
  ArtifactViewSchema,
  FetchArtifactResponseSchema,
  GetArtifactResponseSchema,
  GrantArtifactResponseSchema,
  ListArtifactsResponseSchema,
  RevokeArtifactGrantResponseSchema,
  type FetchArtifactResponse,
} from "../gen/sumi/artifact/v1/artifact_pb";
import * as artifacts from "../lib/artifact";
import { useArtifacts } from "./useArtifacts";

afterEach(() => {
  cleanup();
  vi.restoreAllMocks();
});

describe("useArtifacts", () => {
  it("appends keyset pages in server order and resets the keyset on refresh", async () => {
    const list = vi
      .spyOn(artifacts, "listArtifacts")
      .mockResolvedValueOnce(page("first", "artifact-next"))
      .mockResolvedValueOnce(page("second", ""))
      .mockResolvedValueOnce(page("refreshed", "fresh-next"));
    const detail = create(GetArtifactResponseSchema);
    const get = vi.spyOn(artifacts, "getArtifact").mockResolvedValue(detail);
    const hook = renderHook(() => useArtifacts("work-1", "artifact-1"));

    expect(hook.result.current.status).toBe("loading");
    await waitFor(() => expect(hook.result.current.status).toBe("ready"));
    expect(hook.result.current.data?.artifact).toBe(detail);
    expect(get).toHaveBeenCalledWith(
      expect.objectContaining({ artifactId: "artifact-1" }),
      expect.any(AbortSignal),
    );
    await act(async () => hook.result.current.loadMore());
    expect(ids(hook.result.current.data?.views)).toEqual(["first", "second"]);
    expect(list.mock.calls[1]?.[0].afterArtifactId).toBe("artifact-next");
    expect(list.mock.calls[1]?.[1]).toBeInstanceOf(AbortSignal);

    await act(async () => hook.result.current.refresh());
    expect(list.mock.calls[2]?.[0].afterArtifactId).toBe("");
    expect(ids(hook.result.current.data?.views)).toEqual(["refreshed"]);
  });

  it("clears inaccessible snapshots, keeps unavailable facts stale, and exposes idle", async () => {
    const refreshing = deferred<ReturnType<typeof page>>();
    vi.spyOn(artifacts, "listArtifacts")
      .mockResolvedValueOnce(page("current", ""))
      .mockReturnValueOnce(refreshing.promise)
      .mockRejectedValueOnce(new ConnectError("missing", Code.NotFound))
      .mockResolvedValueOnce(page("current", ""))
      .mockRejectedValueOnce(new ConnectError("offline", Code.Unavailable));
    const hook = renderHook(
      ({ enabled }) => useArtifacts("work-1", undefined, enabled),
      { initialProps: { enabled: true } },
    );
    await waitFor(() => expect(hook.result.current.status).toBe("ready"));

    let refreshPromise!: Promise<void>;
    act(() => {
      refreshPromise = hook.result.current.refresh();
    });
    expect(hook.result.current.status).toBe("refreshing");
    refreshing.resolve(page("new", ""));
    await act(async () => refreshPromise);
    await act(async () => hook.result.current.refresh());
    expect(hook.result.current.status).toBe("error");
    expect(hook.result.current.data).toBeUndefined();
    await act(async () => hook.result.current.refresh());
    await act(async () => hook.result.current.refresh());
    expect(hook.result.current.status).toBe("stale");
    expect(ids(hook.result.current.data?.views)).toEqual(["current"]);
    hook.rerender({ enabled: false });
    expect(hook.result.current.status).toBe("idle");
  });

  it("forwards a typed stream without buffering and records its terminal error", async () => {
    vi.spyOn(artifacts, "listArtifacts").mockResolvedValue(page("one", ""));
    const frame = create(FetchArtifactResponseSchema, {
      payload: { case: "chunk", value: new Uint8Array([1]) },
    });
    const failure = new ConnectError("restricted", Code.PermissionDenied);
    let pulls = 0;
    vi.spyOn(artifacts, "fetchArtifact").mockReturnValue({
      async *[Symbol.asyncIterator]() {
        pulls += 1;
        yield frame;
        throw failure;
      },
    });
    const hook = renderHook(() => useArtifacts("work-1"));
    await waitFor(() => expect(hook.result.current.status).toBe("ready"));

    let handle!: ReturnType<typeof hook.result.current.startFetch>;
    act(() => {
      handle = hook.result.current.startFetch("artifact-1", 1n);
    });
    expect(pulls).toBe(0);
    const iterator = handle.stream![Symbol.asyncIterator]();
    await expect(iterator.next()).resolves.toEqual({
      done: false,
      value: frame,
    });
    await expect(iterator.next()).rejects.toBe(failure);
    await waitFor(() => expect(hook.result.current.fetch.status).toBe("error"));
    expect(hook.result.current.fetch.error).toBe(failure);
  });

  it("aborts on cancel or selection change and ignores late frames and errors", async () => {
    vi.spyOn(artifacts, "listArtifacts").mockResolvedValue(page("one", ""));
    const lateFrame = deferred<IteratorResult<FetchArtifactResponse>>();
    const lateError = deferred<IteratorResult<FetchArtifactResponse>>();
    const signals: AbortSignal[] = [];
    vi.spyOn(artifacts, "fetchArtifact")
      .mockImplementationOnce((_, signal) => {
        signals.push(signal!);
        return sourceFrom(lateFrame.promise);
      })
      .mockImplementationOnce((_, signal) => {
        signals.push(signal!);
        return sourceFrom(lateError.promise);
      });
    const hook = renderHook(({ workId }) => useArtifacts(workId), {
      initialProps: { workId: "work-a" },
    });
    await waitFor(() => expect(hook.result.current.status).toBe("ready"));

    let cancelled!: ReturnType<typeof hook.result.current.startFetch>;
    act(() => {
      cancelled = hook.result.current.startFetch("artifact-a", 1n);
    });
    const cancelledNext = cancelled.stream![Symbol.asyncIterator]().next();
    act(() => cancelled.cancel());
    expect(signals[0]?.aborted).toBe(true);
    expect(hook.result.current.fetch.status).toBe("cancelled");
    lateFrame.resolve({
      done: false,
      value: create(FetchArtifactResponseSchema),
    });
    await expect(cancelledNext).resolves.toEqual({
      done: true,
      value: undefined,
    });
    expect(hook.result.current.fetch.status).toBe("cancelled");

    let oldHandle!: ReturnType<typeof hook.result.current.startFetch>;
    act(() => {
      oldHandle = hook.result.current.startFetch("artifact-a", 2n);
    });
    const oldNext = oldHandle.stream![Symbol.asyncIterator]().next();
    hook.rerender({ workId: "work-b" });
    await waitFor(() => expect(hook.result.current.status).toBe("ready"));
    expect(signals[1]?.aborted).toBe(true);
    expect(hook.result.current.fetch.status).toBe("idle");
    const error = new ConnectError("late", Code.Unavailable);
    lateError.reject(error);
    await expect(oldNext).rejects.toBe(error);
    expect(hook.result.current.fetch.status).toBe("idle");
  });

  it("refreshes Grant and Revoke after typed failure and success without optimistic facts", async () => {
    const list = vi
      .spyOn(artifacts, "listArtifacts")
      .mockResolvedValue(page("canonical", ""));
    const failure = new ConnectError("conflict", Code.FailedPrecondition);
    const grantResponse = create(GrantArtifactResponseSchema);
    const revokeResponse = create(RevokeArtifactGrantResponseSchema);
    const grant = vi
      .spyOn(artifacts, "grantArtifact")
      .mockRejectedValueOnce(failure)
      .mockResolvedValueOnce(grantResponse);
    const revoke = vi
      .spyOn(artifacts, "revokeArtifactGrant")
      .mockRejectedValueOnce(failure)
      .mockResolvedValueOnce(revokeResponse);
    const hook = renderHook(() => useArtifacts("work-1"));
    await waitFor(() => expect(hook.result.current.status).toBe("ready"));

    const actions = [
      {
        invoke: () =>
          hook.result.current.grant({
            artifactId: "artifact",
            target: undefined,
            capability: 0,
          }),
        calls: grant.mock.calls,
        response: grantResponse,
      },
      {
        invoke: () => hook.result.current.revoke({ grantId: "grant" }),
        calls: revoke.mock.calls,
        response: revokeResponse,
      },
    ];
    for (const action of actions) {
      const refreshes = list.mock.calls.length;
      let failed!: Promise<unknown>;
      act(() => {
        failed = action.invoke();
      });
      expect(hook.result.current.action.pending).toBe(true);
      expect(ids(hook.result.current.data?.views)).toEqual(["canonical"]);
      await act(async () => {
        await expect(failed).rejects.toBe(failure);
      });
      expect(hook.result.current.action).toEqual({
        pending: false,
        error: failure,
      });
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

  it("rotates a Grant request ID when a failed payload is edited", async () => {
    vi.spyOn(artifacts, "listArtifacts").mockResolvedValue(page("one", ""));
    const failure = new ConnectError("conflict", Code.AlreadyExists);
    const grant = vi
      .spyOn(artifacts, "grantArtifact")
      .mockRejectedValue(failure);
    const hook = renderHook(() => useArtifacts("work-1"));
    await waitFor(() => expect(hook.result.current.status).toBe("ready"));
    await act(async () => {
      await expect(
        hook.result.current.grant({
          artifactId: "one",
          target: undefined,
          capability: 0,
        }),
      ).rejects.toBe(failure);
      await expect(
        hook.result.current.grant({
          artifactId: "two",
          target: undefined,
          capability: 0,
        }),
      ).rejects.toBe(failure);
    });
    expect(grant.mock.calls[1]?.[0].requestId).not.toBe(
      grant.mock.calls[0]?.[0].requestId,
    );
  });

  it("ignores a late mutation error after the owning Work changes", async () => {
    const list = vi
      .spyOn(artifacts, "listArtifacts")
      .mockImplementation((request) =>
        Promise.resolve(page(request.owningWorkId, "")),
      );
    const lateResponse = create(GrantArtifactResponseSchema);
    const pending = deferred<typeof lateResponse>();
    vi.spyOn(artifacts, "grantArtifact").mockReturnValue(pending.promise);
    const hook = renderHook(({ workId }) => useArtifacts(workId), {
      initialProps: { workId: "work-a" },
    });
    await waitFor(() => expect(hook.result.current.status).toBe("ready"));
    let completion!: Promise<unknown>;
    act(() => {
      completion = hook.result.current.grant({
        artifactId: "artifact",
        target: undefined,
        capability: 0,
      });
    });
    hook.rerender({ workId: "work-b" });
    await waitFor(() =>
      expect(ids(hook.result.current.data?.views)).toEqual(["work-b"]),
    );
    const calls = list.mock.calls.length;
    const failure = new ConnectError("late", Code.Unavailable);
    pending.reject(failure);
    await act(async () => {
      await expect(completion).rejects.toBe(failure);
    });
    expect(hook.result.current.action).toEqual({ pending: false });
    expect(list).toHaveBeenCalledTimes(calls);
  });
});

function page(id: string, nextArtifactId: string) {
  return create(ListArtifactsResponseSchema, {
    views: [
      create(ArtifactViewSchema, {
        artifact: create(ArtifactSchema, { id }),
      }),
    ],
    nextArtifactId,
  });
}

function ids(views: ReturnType<typeof page>["views"] | undefined) {
  return views?.map((view) => view.artifact?.id);
}

function sourceFrom(
  next: Promise<IteratorResult<FetchArtifactResponse>>,
): AsyncIterable<FetchArtifactResponse> {
  return {
    [Symbol.asyncIterator]() {
      return { next: () => next };
    },
  };
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
