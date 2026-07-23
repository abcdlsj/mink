import type {
  CreateHumanRequest,
  GetHumanRequest,
  SetHumanStatusRequest,
} from "../gen/sumi/organization/v1/organization_pb";
import type {
  CheckPermissionRequest,
  GetGrantRequest,
  IssueGrantRequest,
  RevokeGrantRequest,
} from "../gen/sumi/grant/v1/grant_pb";
import { grantClient, organizationClient } from "../api/clients";

export const getOrganization = (signal?: AbortSignal) =>
  organizationClient.getOrganization({}, { signal });
export const listHumans = (signal?: AbortSignal) =>
  organizationClient.listHumans({}, { signal });
export const createHuman = (request: CreateHumanRequest) =>
  organizationClient.createHuman(request);
export const getHuman = (request: GetHumanRequest, signal?: AbortSignal) =>
  organizationClient.getHuman(request, { signal });
export const setHumanStatus = (request: SetHumanStatusRequest) =>
  organizationClient.setHumanStatus(request);
export const issueGrant = (request: IssueGrantRequest) =>
  grantClient.issueGrant(request);
export const revokeGrant = (request: RevokeGrantRequest) =>
  grantClient.revokeGrant(request);
export const getGrant = (request: GetGrantRequest, signal?: AbortSignal) =>
  grantClient.getGrant(request, { signal });
export const listGrants = (signal?: AbortSignal) =>
  grantClient.listGrants({}, { signal });
export const checkPermission = (
  request: CheckPermissionRequest,
  signal?: AbortSignal,
) => grantClient.checkPermission(request, { signal });
