import { create } from "@bufbuild/protobuf";
import { Code, ConnectError } from "@connectrpc/connect";
import { afterEach, describe, expect, it, vi } from "vitest";
import { grantClient, organizationClient } from "../api/clients";
import {
  CheckPermissionRequestSchema,
  CheckPermissionResponseSchema,
  GetGrantRequestSchema,
  GetGrantResponseSchema,
  IssueGrantRequestSchema,
  IssueGrantResponseSchema,
  ListGrantsResponseSchema,
  RevokeGrantRequestSchema,
  RevokeGrantResponseSchema,
} from "../gen/sumi/grant/v1/grant_pb";
import {
  CreateHumanRequestSchema,
  CreateHumanResponseSchema,
  GetHumanRequestSchema,
  GetHumanResponseSchema,
  GetOrganizationResponseSchema,
  ListHumansResponseSchema,
  SetHumanStatusRequestSchema,
  SetHumanStatusResponseSchema,
} from "../gen/sumi/organization/v1/organization_pb";
import {
  checkPermission,
  createHuman,
  getGrant,
  getHuman,
  getOrganization,
  issueGrant,
  listGrants,
  listHumans,
  revokeGrant,
  setHumanStatus,
} from "./authority";

afterEach(() => vi.restoreAllMocks());

describe("authority transport", () => {
  it("keeps all ten generated RPC request, response, and error identities", async () => {
    const controller = new AbortController();

    const organizationResponse = create(GetOrganizationResponseSchema);
    const organizationResult = Promise.resolve(organizationResponse);
    const organizationSpy = vi
      .spyOn(organizationClient, "getOrganization")
      .mockReturnValueOnce(organizationResult);
    const organization = getOrganization(controller.signal);
    expect(organization).toBe(organizationResult);
    await expect(organization).resolves.toBe(organizationResponse);
    expect(organizationSpy).toHaveBeenCalledWith(
      {},
      { signal: controller.signal },
    );
    const organizationError = new ConnectError(
      "get organization",
      Code.Unavailable,
    );
    const organizationFailure = Promise.reject(organizationError);
    organizationSpy.mockReturnValueOnce(organizationFailure);
    const organizationFailed = getOrganization(controller.signal);
    expect(organizationFailed).toBe(organizationFailure);
    await expect(organizationFailed).rejects.toBe(organizationError);

    const humansResponse = create(ListHumansResponseSchema);
    const humansResult = Promise.resolve(humansResponse);
    const humansSpy = vi
      .spyOn(organizationClient, "listHumans")
      .mockReturnValueOnce(humansResult);
    const humans = listHumans(controller.signal);
    expect(humans).toBe(humansResult);
    await expect(humans).resolves.toBe(humansResponse);
    expect(humansSpy).toHaveBeenCalledWith({}, { signal: controller.signal });
    const humansError = new ConnectError("list humans", Code.NotFound);
    const humansFailure = Promise.reject(humansError);
    humansSpy.mockReturnValueOnce(humansFailure);
    const humansFailed = listHumans(controller.signal);
    expect(humansFailed).toBe(humansFailure);
    await expect(humansFailed).rejects.toBe(humansError);

    const createHumanRequest = create(CreateHumanRequestSchema, {
      requestId: "create-human-request-id",
      name: "Ada",
    });
    const createHumanResponse = create(CreateHumanResponseSchema);
    const createHumanResult = Promise.resolve(createHumanResponse);
    const createHumanSpy = vi
      .spyOn(organizationClient, "createHuman")
      .mockReturnValueOnce(createHumanResult);
    const createdHuman = createHuman(createHumanRequest);
    expect(createdHuman).toBe(createHumanResult);
    await expect(createdHuman).resolves.toBe(createHumanResponse);
    expect(createHumanSpy).toHaveBeenCalledWith(createHumanRequest);
    const createHumanError = new ConnectError(
      "create human",
      Code.Unauthenticated,
    );
    const createHumanFailure = Promise.reject(createHumanError);
    createHumanSpy.mockReturnValueOnce(createHumanFailure);
    const createdHumanFailed = createHuman(createHumanRequest);
    expect(createdHumanFailed).toBe(createHumanFailure);
    await expect(createdHumanFailed).rejects.toBe(createHumanError);

    const getHumanRequest = create(GetHumanRequestSchema, {
      humanId: "human-get",
    });
    const getHumanResponse = create(GetHumanResponseSchema);
    const getHumanResult = Promise.resolve(getHumanResponse);
    const getHumanSpy = vi
      .spyOn(organizationClient, "getHuman")
      .mockReturnValueOnce(getHumanResult);
    const gottenHuman = getHuman(getHumanRequest, controller.signal);
    expect(gottenHuman).toBe(getHumanResult);
    await expect(gottenHuman).resolves.toBe(getHumanResponse);
    expect(getHumanSpy).toHaveBeenCalledWith(getHumanRequest, {
      signal: controller.signal,
    });
    const getHumanError = new ConnectError("get human", Code.PermissionDenied);
    const getHumanFailure = Promise.reject(getHumanError);
    getHumanSpy.mockReturnValueOnce(getHumanFailure);
    const gottenHumanFailed = getHuman(getHumanRequest, controller.signal);
    expect(gottenHumanFailed).toBe(getHumanFailure);
    await expect(gottenHumanFailed).rejects.toBe(getHumanError);

    const setStatusRequest = create(SetHumanStatusRequestSchema, {
      requestId: "status-request-id",
      humanId: "human-status",
    });
    const setStatusResponse = create(SetHumanStatusResponseSchema);
    const setStatusResult = Promise.resolve(setStatusResponse);
    const setStatusSpy = vi
      .spyOn(organizationClient, "setHumanStatus")
      .mockReturnValueOnce(setStatusResult);
    const setStatus = setHumanStatus(setStatusRequest);
    expect(setStatus).toBe(setStatusResult);
    await expect(setStatus).resolves.toBe(setStatusResponse);
    expect(setStatusSpy).toHaveBeenCalledWith(setStatusRequest);
    const setStatusError = new ConnectError(
      "set human status",
      Code.FailedPrecondition,
    );
    const setStatusFailure = Promise.reject(setStatusError);
    setStatusSpy.mockReturnValueOnce(setStatusFailure);
    const setStatusFailed = setHumanStatus(setStatusRequest);
    expect(setStatusFailed).toBe(setStatusFailure);
    await expect(setStatusFailed).rejects.toBe(setStatusError);

    const issueRequest = create(IssueGrantRequestSchema, {
      requestId: "issue-request-id",
    });
    const issueResponse = create(IssueGrantResponseSchema);
    const issueResult = Promise.resolve(issueResponse);
    const issueSpy = vi
      .spyOn(grantClient, "issueGrant")
      .mockReturnValueOnce(issueResult);
    const issued = issueGrant(issueRequest);
    expect(issued).toBe(issueResult);
    await expect(issued).resolves.toBe(issueResponse);
    expect(issueSpy).toHaveBeenCalledWith(issueRequest);
    const issueError = new ConnectError("issue grant", Code.Aborted);
    const issueFailure = Promise.reject(issueError);
    issueSpy.mockReturnValueOnce(issueFailure);
    const issuedFailed = issueGrant(issueRequest);
    expect(issuedFailed).toBe(issueFailure);
    await expect(issuedFailed).rejects.toBe(issueError);

    const revokeRequest = create(RevokeGrantRequestSchema, {
      requestId: "revoke-request-id",
      grantId: "grant-revoke",
    });
    const revokeResponse = create(RevokeGrantResponseSchema);
    const revokeResult = Promise.resolve(revokeResponse);
    const revokeSpy = vi
      .spyOn(grantClient, "revokeGrant")
      .mockReturnValueOnce(revokeResult);
    const revoked = revokeGrant(revokeRequest);
    expect(revoked).toBe(revokeResult);
    await expect(revoked).resolves.toBe(revokeResponse);
    expect(revokeSpy).toHaveBeenCalledWith(revokeRequest);
    const revokeError = new ConnectError("revoke grant", Code.Internal);
    const revokeFailure = Promise.reject(revokeError);
    revokeSpy.mockReturnValueOnce(revokeFailure);
    const revokedFailed = revokeGrant(revokeRequest);
    expect(revokedFailed).toBe(revokeFailure);
    await expect(revokedFailed).rejects.toBe(revokeError);

    const getGrantRequest = create(GetGrantRequestSchema, {
      grantId: "grant-get",
    });
    const getGrantResponse = create(GetGrantResponseSchema);
    const getGrantResult = Promise.resolve(getGrantResponse);
    const getGrantSpy = vi
      .spyOn(grantClient, "getGrant")
      .mockReturnValueOnce(getGrantResult);
    const gottenGrant = getGrant(getGrantRequest, controller.signal);
    expect(gottenGrant).toBe(getGrantResult);
    await expect(gottenGrant).resolves.toBe(getGrantResponse);
    expect(getGrantSpy).toHaveBeenCalledWith(getGrantRequest, {
      signal: controller.signal,
    });
    const getGrantError = new ConnectError("get grant", Code.PermissionDenied);
    const getGrantFailure = Promise.reject(getGrantError);
    getGrantSpy.mockReturnValueOnce(getGrantFailure);
    const gottenGrantFailed = getGrant(getGrantRequest, controller.signal);
    expect(gottenGrantFailed).toBe(getGrantFailure);
    await expect(gottenGrantFailed).rejects.toBe(getGrantError);

    const listGrantsResponse = create(ListGrantsResponseSchema);
    const listGrantsResult = Promise.resolve(listGrantsResponse);
    const listGrantsSpy = vi
      .spyOn(grantClient, "listGrants")
      .mockReturnValueOnce(listGrantsResult);
    const listedGrants = listGrants(controller.signal);
    expect(listedGrants).toBe(listGrantsResult);
    await expect(listedGrants).resolves.toBe(listGrantsResponse);
    expect(listGrantsSpy).toHaveBeenCalledWith(
      {},
      { signal: controller.signal },
    );
    const listGrantsError = new ConnectError("list grants", Code.NotFound);
    const listGrantsFailure = Promise.reject(listGrantsError);
    listGrantsSpy.mockReturnValueOnce(listGrantsFailure);
    const listedGrantsFailed = listGrants(controller.signal);
    expect(listedGrantsFailed).toBe(listGrantsFailure);
    await expect(listedGrantsFailed).rejects.toBe(listGrantsError);

    const permissionRequest = create(CheckPermissionRequestSchema);
    const permissionResponse = create(CheckPermissionResponseSchema, {
      allowed: true,
    });
    const permissionResult = Promise.resolve(permissionResponse);
    const permissionSpy = vi
      .spyOn(grantClient, "checkPermission")
      .mockReturnValueOnce(permissionResult);
    const permission = checkPermission(permissionRequest, controller.signal);
    expect(permission).toBe(permissionResult);
    await expect(permission).resolves.toBe(permissionResponse);
    expect(permissionSpy).toHaveBeenCalledWith(permissionRequest, {
      signal: controller.signal,
    });
    const permissionError = new ConnectError(
      "check permission",
      Code.Unavailable,
    );
    const permissionFailure = Promise.reject(permissionError);
    permissionSpy.mockReturnValueOnce(permissionFailure);
    const permissionFailed = checkPermission(
      permissionRequest,
      controller.signal,
    );
    expect(permissionFailed).toBe(permissionFailure);
    await expect(permissionFailed).rejects.toBe(permissionError);
  });
});
