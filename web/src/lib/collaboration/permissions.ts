import {
  Capability,
  PrincipalKind,
  ScopeKind,
} from "../../gen/sumi/grant/v1/grant_pb";
import { grantClient } from "../../api/clients";
import { collaborationErrorMessage } from "./errors";
import type { PermissionState, SpacePermissions } from "./types";

export async function loadSpacePermissions(
  humanId: string,
  spaceId: string,
  signal?: AbortSignal,
): Promise<SpacePermissions> {
  const [members, archive, send] = await Promise.all([
    checkPermission(
      humanId,
      Capability.SPACE_MEMBERS_MANAGE,
      ScopeKind.SPACE,
      spaceId,
      signal,
    ),
    checkPermission(
      humanId,
      Capability.SPACE_ARCHIVE,
      ScopeKind.SPACE,
      spaceId,
      signal,
    ),
    checkPermission(
      humanId,
      Capability.MESSAGE_SEND,
      ScopeKind.SPACE,
      spaceId,
      signal,
    ),
  ]);
  return { members, archive, send };
}

export async function checkPermission(
  humanId: string,
  capability: Capability,
  scopeKind: ScopeKind,
  scopeId: string,
  signal?: AbortSignal,
): Promise<PermissionState> {
  try {
    const response = await grantClient.checkPermission(
      {
        subject: { kind: PrincipalKind.HUMAN, id: humanId },
        capability,
        scope: { kind: scopeKind, id: scopeId },
      },
      { signal },
    );
    return { status: response.allowed ? "allowed" : "denied" };
  } catch (error) {
    if (signal?.aborted) throw error;
    return {
      status: "unknown",
      error: collaborationErrorMessage(error, "check permission"),
    };
  }
}
