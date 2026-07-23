import { create } from "@bufbuild/protobuf";
import { Code, ConnectError } from "@connectrpc/connect";
import { useCallback, useEffect, useRef, useState } from "react";
import {
  CheckPermissionRequestSchema,
  IssueGrantRequestSchema,
  RevokeGrantRequestSchema,
} from "../gen/sumi/grant/v1/grant_pb";
import type {
  CheckPermissionRequest,
  CheckPermissionResponse,
  IssueGrantRequest,
  IssueGrantResponse,
  ListGrantsResponse,
  RevokeGrantRequest,
  RevokeGrantResponse,
} from "../gen/sumi/grant/v1/grant_pb";
import {
  CreateHumanRequestSchema,
  SetHumanStatusRequestSchema,
} from "../gen/sumi/organization/v1/organization_pb";
import type {
  CreateHumanRequest,
  CreateHumanResponse,
  GetOrganizationResponse,
  ListHumansResponse,
  SetHumanStatusRequest,
  SetHumanStatusResponse,
} from "../gen/sumi/organization/v1/organization_pb";
import { PayloadRequestLifecycle } from "../lib/collaboration/requestLifecycle";
import {
  checkPermission,
  createHuman,
  getOrganization,
  issueGrant,
  listGrants,
  listHumans,
  revokeGrant,
  setHumanStatus,
} from "../lib/authority";
import { useRemoteSnapshot, type SnapshotFailure } from "./useRemoteSnapshot";

type Failure = SnapshotFailure;
type Mutation =
  | Omit<CreateHumanRequest, "$typeName" | "requestId">
  | Omit<SetHumanStatusRequest, "$typeName" | "requestId">
  | Omit<IssueGrantRequest, "$typeName" | "requestId">
  | Omit<RevokeGrantRequest, "$typeName" | "requestId">;
export type AuthoritySnapshot = {
  organization?: GetOrganizationResponse["organization"];
  humans: ListHumansResponse["humans"];
  grants: ListGrantsResponse["grants"];
  permission?: CheckPermissionResponse;
};

export function useAuthority(
  permissionRequest?: CheckPermissionRequest,
  enabled = true,
) {
  const targetId = `authority:${JSON.stringify(permissionRequest ?? {})}`;
  const load = useCallback(
    async (signal: AbortSignal): Promise<AuthoritySnapshot> => {
      const [organization, humans, grants, permission] = await Promise.all([
        getOrganization(signal),
        listHumans(signal),
        listGrants(signal),
        permissionRequest
          ? checkPermission(permissionRequest, signal)
          : Promise.resolve(undefined),
      ]);
      return {
        organization: organization.organization,
        humans: humans.humans,
        grants: grants.grants,
        permission,
      };
    },
    [permissionRequest],
  );
  const classifyError = useCallback((error: unknown): Failure => {
    const code = ConnectError.from(error).code;
    return {
      message:
        error instanceof Error
          ? error.message
          : "Could not load authority facts.",
      discard:
        code === Code.Unauthenticated ||
        code === Code.PermissionDenied ||
        code === Code.NotFound,
    };
  }, []);
  const snapshot = useRemoteSnapshot({
    enabled,
    targetId,
    load,
    classifyError,
  });
  const [action, setAction] = useState<{ pending: boolean; error?: unknown }>({
    pending: false,
  });
  const actionGeneration = useRef(0);
  const createLifecycle = useRef(
    new PayloadRequestLifecycle<Mutation>(fingerprint),
  );
  const statusLifecycle = useRef(
    new PayloadRequestLifecycle<Mutation>(fingerprint),
  );
  const issueLifecycle = useRef(
    new PayloadRequestLifecycle<Mutation>(fingerprint),
  );
  const revokeLifecycle = useRef(
    new PayloadRequestLifecycle<Mutation>(fingerprint),
  );
  useEffect(() => {
    actionGeneration.current += 1;
    setAction({ pending: false });
    return () => {
      actionGeneration.current += 1;
    };
  }, [enabled, targetId]);
  const refresh = useCallback(() => snapshot.refresh("retrying"), [snapshot]);
  const mutate = useCallback(
    async <T>(
      lifecycle: PayloadRequestLifecycle<Mutation>,
      payload: Mutation,
      invoke: (requestId: string) => Promise<T>,
    ) => {
      const binding = snapshot.capture();
      if (!binding) throw new Error("Authority snapshot is not active");
      const generation = ++actionGeneration.current;
      setAction({ pending: true });
      try {
        const response = await invoke(lifecycle.sync(payload));
        lifecycle.complete();
        if (
          actionGeneration.current === generation &&
          snapshot.isCurrent(binding)
        )
          await refresh();
        return response;
      } catch (error) {
        if (
          actionGeneration.current === generation &&
          snapshot.isCurrent(binding)
        ) {
          setAction({ pending: false, error });
          await refresh();
        }
        throw error;
      } finally {
        if (actionGeneration.current === generation)
          setAction((current) => ({ ...current, pending: false }));
      }
    },
    [refresh, snapshot],
  );
  return {
    status:
      snapshot.state.status === "retrying" ? "loading" : snapshot.state.status,
    data: snapshot.state.data,
    error: snapshot.state.failure?.message,
    action,
    refresh,
    check: (request: CheckPermissionRequest) => checkPermission(request),
    createHuman: (
      payload: Omit<CreateHumanRequest, "$typeName" | "requestId">,
    ) =>
      mutate(createLifecycle.current, payload, (requestId) =>
        createHuman(
          create(CreateHumanRequestSchema, { ...payload, requestId }),
        ),
      ) as Promise<CreateHumanResponse>,
    setHumanStatus: (
      payload: Omit<SetHumanStatusRequest, "$typeName" | "requestId">,
    ) =>
      mutate(statusLifecycle.current, payload, (requestId) =>
        setHumanStatus(
          create(SetHumanStatusRequestSchema, { ...payload, requestId }),
        ),
      ) as Promise<SetHumanStatusResponse>,
    issueGrant: (payload: Omit<IssueGrantRequest, "$typeName" | "requestId">) =>
      mutate(issueLifecycle.current, payload, (requestId) =>
        issueGrant(create(IssueGrantRequestSchema, { ...payload, requestId })),
      ) as Promise<IssueGrantResponse>,
    revokeGrant: (
      payload: Omit<RevokeGrantRequest, "$typeName" | "requestId">,
    ) =>
      mutate(revokeLifecycle.current, payload, (requestId) =>
        revokeGrant(
          create(RevokeGrantRequestSchema, { ...payload, requestId }),
        ),
      ) as Promise<RevokeGrantResponse>,
  };
}

function fingerprint(payload: Mutation) {
  return JSON.stringify(payload, (_, value) =>
    typeof value === "bigint" ? value.toString() : value,
  );
}
