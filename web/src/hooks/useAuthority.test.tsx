import { create } from "@bufbuild/protobuf";
import { Code, ConnectError } from "@connectrpc/connect";
import { act, cleanup, renderHook, waitFor } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import {
  CheckPermissionRequestSchema,
  CheckPermissionResponseSchema,
  GrantSchema,
  IssueGrantResponseSchema,
  ListGrantsResponseSchema,
  RevokeGrantResponseSchema,
} from "../gen/sumi/grant/v1/grant_pb";
import {
  CreateHumanResponseSchema,
  GetOrganizationResponseSchema,
  HumanSchema,
  ListHumansResponseSchema,
  OrganizationSchema,
  SetHumanStatusResponseSchema,
} from "../gen/sumi/organization/v1/organization_pb";
import * as authority from "../lib/authority";
import { useAuthority } from "./useAuthority";

afterEach(() => {
  cleanup();
  vi.restoreAllMocks();
});

describe("useAuthority", () => {
  it("loads organization, humans, grants, and the requested permission as one snapshot", async () => {
    mockSnapshot("org-a");
    const permission = create(CheckPermissionResponseSchema, { allowed: true });
    const check = vi
      .spyOn(authority, "checkPermission")
      .mockResolvedValue(permission);
    const request = create(CheckPermissionRequestSchema, { capability: 0 });
    const hook = renderHook(() => useAuthority(request));

    expect(hook.result.current.status).toBe("loading");
    await waitFor(() => expect(hook.result.current.status).toBe("ready"));
    expect(hook.result.current.data?.organization?.id).toBe("org-a");
    expect(hook.result.current.data?.humans[0]?.id).toBe("human-org-a");
    expect(hook.result.current.data?.grants[0]?.id).toBe("grant-org-a");
    expect(hook.result.current.data?.permission).toBe(permission);
    expect(check).toHaveBeenCalledWith(request, expect.any(AbortSignal));
  });

  it("aborts a replaced snapshot and the active request on unmount without late pollution", async () => {
    const oldOrganization = deferred<ReturnType<typeof organization>>();
    const signals: AbortSignal[] = [];
    vi.spyOn(authority, "getOrganization")
      .mockImplementationOnce((signal) => {
        signals.push(signal!);
        return oldOrganization.promise;
      })
      .mockImplementationOnce((signal) => {
        signals.push(signal!);
        return Promise.resolve(organization("current"));
      });
    vi.spyOn(authority, "listHumans").mockResolvedValue(humans("current"));
    vi.spyOn(authority, "listGrants").mockResolvedValue(grants("current"));
    vi.spyOn(authority, "checkPermission").mockResolvedValue(
      create(CheckPermissionResponseSchema),
    );
    const first = create(CheckPermissionRequestSchema, { capability: 0 });
    const second = create(CheckPermissionRequestSchema, { capability: 1 });
    const hook = renderHook(({ request }) => useAuthority(request), {
      initialProps: { request: first },
    });
    await waitFor(() => expect(signals).toHaveLength(1));

    hook.rerender({ request: second });
    await waitFor(() => expect(hook.result.current.status).toBe("ready"));
    expect(signals[0]?.aborted).toBe(true);
    oldOrganization.resolve(organization("late"));
    await act(async () => oldOrganization.promise);
    expect(hook.result.current.data?.organization?.id).toBe("current");

    hook.unmount();
    expect(signals[1]?.aborted).toBe(true);
  });

  it("exposes refreshing, inaccessible error, unavailable stale, and idle states", async () => {
    const next = deferred<ReturnType<typeof organization>>();
    const getOrganization = vi
      .spyOn(authority, "getOrganization")
      .mockResolvedValueOnce(organization("current"))
      .mockReturnValueOnce(next.promise)
      .mockRejectedValueOnce(new ConnectError("denied", Code.PermissionDenied))
      .mockResolvedValueOnce(organization("current"))
      .mockRejectedValueOnce(new ConnectError("offline", Code.Unavailable));
    vi.spyOn(authority, "listHumans").mockResolvedValue(humans("current"));
    vi.spyOn(authority, "listGrants").mockResolvedValue(grants("current"));
    const hook = renderHook(({ enabled }) => useAuthority(undefined, enabled), {
      initialProps: { enabled: true },
    });
    await waitFor(() => expect(hook.result.current.status).toBe("ready"));

    let refreshPromise!: Promise<void>;
    act(() => {
      refreshPromise = hook.result.current.refresh();
    });
    expect(hook.result.current.status).toBe("refreshing");
    next.resolve(organization("new"));
    await act(async () => refreshPromise);
    await act(async () => hook.result.current.refresh());
    expect(hook.result.current.status).toBe("error");
    expect(hook.result.current.data).toBeUndefined();
    await act(async () => hook.result.current.refresh());
    await act(async () => hook.result.current.refresh());
    expect(hook.result.current.status).toBe("stale");
    expect(hook.result.current.data?.organization?.id).toBe("current");
    hook.rerender({ enabled: false });
    expect(hook.result.current.status).toBe("idle");
    expect(getOrganization).toHaveBeenCalledTimes(5);
  });

  it("refreshes every mutation after typed failure and success without optimistic facts", async () => {
    const getOrganization = mockSnapshot("canonical");
    const failure = new ConnectError("last owner", Code.FailedPrecondition);
    const createResponse = create(CreateHumanResponseSchema);
    const statusResponse = create(SetHumanStatusResponseSchema);
    const issueResponse = create(IssueGrantResponseSchema);
    const revokeResponse = create(RevokeGrantResponseSchema);
    const createHuman = vi
      .spyOn(authority, "createHuman")
      .mockRejectedValueOnce(failure)
      .mockResolvedValueOnce(createResponse);
    const setHumanStatus = vi
      .spyOn(authority, "setHumanStatus")
      .mockRejectedValueOnce(failure)
      .mockResolvedValueOnce(statusResponse);
    const issueGrant = vi
      .spyOn(authority, "issueGrant")
      .mockRejectedValueOnce(failure)
      .mockResolvedValueOnce(issueResponse);
    const revokeGrant = vi
      .spyOn(authority, "revokeGrant")
      .mockRejectedValueOnce(failure)
      .mockResolvedValueOnce(revokeResponse);
    const hook = renderHook(() => useAuthority());
    await waitFor(() => expect(hook.result.current.status).toBe("ready"));

    const actions = [
      {
        invoke: () =>
          hook.result.current.createHuman({
            name: "human",
            role: 0,
            credential: "credential",
          }),
        calls: createHuman.mock.calls,
        response: createResponse,
      },
      {
        invoke: () =>
          hook.result.current.setHumanStatus({ humanId: "human", status: 0 }),
        calls: setHumanStatus.mock.calls,
        response: statusResponse,
      },
      {
        invoke: () =>
          hook.result.current.issueGrant({
            subject: undefined,
            capability: 0,
            scope: undefined,
            parentGrantId: "",
            expiresAt: undefined,
          }),
        calls: issueGrant.mock.calls,
        response: issueResponse,
      },
      {
        invoke: () => hook.result.current.revokeGrant({ grantId: "grant" }),
        calls: revokeGrant.mock.calls,
        response: revokeResponse,
      },
    ];
    for (const action of actions) {
      const refreshes = getOrganization.mock.calls.length;
      let failed!: Promise<unknown>;
      act(() => {
        failed = action.invoke();
      });
      expect(hook.result.current.action.pending).toBe(true);
      expect(hook.result.current.data?.organization?.id).toBe("canonical");
      await act(async () => {
        await expect(failed).rejects.toBe(failure);
      });
      expect(hook.result.current.action).toEqual({
        pending: false,
        error: failure,
      });
      expect(getOrganization).toHaveBeenCalledTimes(refreshes + 1);
      let response!: unknown;
      await act(async () => {
        response = await action.invoke();
      });
      expect(response).toBe(action.response);
      expect(action.calls[1]?.[0].requestId).toBe(
        action.calls[0]?.[0].requestId,
      );
      expect(hook.result.current.action).toEqual({ pending: false });
      expect(getOrganization).toHaveBeenCalledTimes(refreshes + 2);
    }
  });

  it("reuses a failed request ID and rotates it when a Create Human payload changes", async () => {
    mockSnapshot("canonical");
    const failure = new ConnectError("conflict", Code.AlreadyExists);
    const createHuman = vi
      .spyOn(authority, "createHuman")
      .mockRejectedValue(failure);
    const hook = renderHook(() => useAuthority());
    await waitFor(() => expect(hook.result.current.status).toBe("ready"));
    const create = (name: string) =>
      hook.result.current.createHuman({ name, role: 0, credential: "secret" });

    await act(async () => {
      await expect(create("one")).rejects.toBe(failure);
      await expect(create("one")).rejects.toBe(failure);
      await expect(create("two")).rejects.toBe(failure);
    });
    expect(createHuman.mock.calls[1]?.[0].requestId).toBe(
      createHuman.mock.calls[0]?.[0].requestId,
    );
    expect(createHuman.mock.calls[2]?.[0].requestId).not.toBe(
      createHuman.mock.calls[1]?.[0].requestId,
    );
  });

  it("ignores a late mutation error after the permission target changes", async () => {
    const getOrganization = mockSnapshot("canonical");
    vi.spyOn(authority, "checkPermission").mockResolvedValue(
      create(CheckPermissionResponseSchema),
    );
    const lateResponse = create(CreateHumanResponseSchema);
    const pending = deferred<typeof lateResponse>();
    vi.spyOn(authority, "createHuman").mockReturnValue(pending.promise);
    const first = create(CheckPermissionRequestSchema, { capability: 0 });
    const second = create(CheckPermissionRequestSchema, { capability: 1 });
    const hook = renderHook(({ request }) => useAuthority(request), {
      initialProps: { request: first },
    });
    await waitFor(() => expect(hook.result.current.status).toBe("ready"));
    let completion!: Promise<unknown>;
    act(() => {
      completion = hook.result.current.createHuman({
        name: "old",
        role: 0,
        credential: "secret",
      });
    });
    hook.rerender({ request: second });
    await waitFor(() => expect(hook.result.current.status).toBe("ready"));
    const calls = getOrganization.mock.calls.length;
    const failure = new ConnectError("late", Code.Unavailable);
    pending.reject(failure);
    await act(async () => {
      await expect(completion).rejects.toBe(failure);
    });
    expect(hook.result.current.action).toEqual({ pending: false });
    expect(getOrganization).toHaveBeenCalledTimes(calls);
  });
});

function mockSnapshot(id: string) {
  const getOrganization = vi
    .spyOn(authority, "getOrganization")
    .mockResolvedValue(organization(id));
  vi.spyOn(authority, "listHumans").mockResolvedValue(humans(id));
  vi.spyOn(authority, "listGrants").mockResolvedValue(grants(id));
  return getOrganization;
}

function organization(id: string) {
  return create(GetOrganizationResponseSchema, {
    organization: create(OrganizationSchema, { id }),
  });
}

function humans(id: string) {
  return create(ListHumansResponseSchema, {
    humans: [create(HumanSchema, { id: `human-${id}` })],
  });
}

function grants(id: string) {
  return create(ListGrantsResponseSchema, {
    grants: [create(GrantSchema, { id: `grant-${id}` })],
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
