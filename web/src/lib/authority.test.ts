import { create } from "@bufbuild/protobuf";
import { Code, ConnectError } from "@connectrpc/connect";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { grantClient, organizationClient } from "../api/clients";
import {
  CheckPermissionRequestSchema,
  GetGrantRequestSchema,
  IssueGrantRequestSchema,
  RevokeGrantRequestSchema,
} from "../gen/sumi/grant/v1/grant_pb";
import {
  CreateHumanRequestSchema,
  GetHumanRequestSchema,
  SetHumanStatusRequestSchema,
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

vi.mock("../api/clients", () => ({
  grantClient: {
    issueGrant: vi.fn(),
    revokeGrant: vi.fn(),
    getGrant: vi.fn(),
    listGrants: vi.fn(),
    checkPermission: vi.fn(),
  },
  organizationClient: {
    getOrganization: vi.fn(),
    listHumans: vi.fn(),
    createHuman: vi.fn(),
    getHuman: vi.fn(),
    setHumanStatus: vi.fn(),
  },
}));

beforeEach(() => vi.clearAllMocks());

describe("authority transport", () => {
  it("reuses generated organization and grant clients without adding pagination or request IDs", () => {
    const controller = new AbortController();
    const createHumanRequest = create(CreateHumanRequestSchema, {
      requestId: "create-human-request-id",
      name: "Ada",
    });
    const getHumanRequest = create(GetHumanRequestSchema, {
      humanId: "human-1",
    });
    const setStatusRequest = create(SetHumanStatusRequestSchema, {
      requestId: "status-request-id",
      humanId: "human-1",
    });
    const issueGrantRequest = create(IssueGrantRequestSchema, {
      requestId: "issue-grant-request-id",
    });
    const revokeGrantRequest = create(RevokeGrantRequestSchema, {
      requestId: "revoke-grant-request-id",
      grantId: "grant-1",
    });
    const getGrantRequest = create(GetGrantRequestSchema, {
      grantId: "grant-1",
    });
    const permissionRequest = create(CheckPermissionRequestSchema);

    getOrganization(controller.signal);
    listHumans(controller.signal);
    createHuman(createHumanRequest);
    getHuman(getHumanRequest, controller.signal);
    setHumanStatus(setStatusRequest);
    issueGrant(issueGrantRequest);
    revokeGrant(revokeGrantRequest);
    getGrant(getGrantRequest, controller.signal);
    listGrants(controller.signal);
    checkPermission(permissionRequest, controller.signal);

    expect(organizationClient.getOrganization).toHaveBeenCalledWith(
      {},
      {
        signal: controller.signal,
      },
    );
    expect(organizationClient.listHumans).toHaveBeenCalledWith(
      {},
      {
        signal: controller.signal,
      },
    );
    expect(organizationClient.createHuman).toHaveBeenCalledWith(
      createHumanRequest,
    );
    expect(organizationClient.getHuman).toHaveBeenCalledWith(getHumanRequest, {
      signal: controller.signal,
    });
    expect(organizationClient.setHumanStatus).toHaveBeenCalledWith(
      setStatusRequest,
    );
    expect(grantClient.issueGrant).toHaveBeenCalledWith(issueGrantRequest);
    expect(grantClient.revokeGrant).toHaveBeenCalledWith(revokeGrantRequest);
    expect(grantClient.getGrant).toHaveBeenCalledWith(getGrantRequest, {
      signal: controller.signal,
    });
    expect(grantClient.listGrants).toHaveBeenCalledWith(
      {},
      {
        signal: controller.signal,
      },
    );
    expect(grantClient.checkPermission).toHaveBeenCalledWith(
      permissionRequest,
      {
        signal: controller.signal,
      },
    );
  });

  it.each([
    Code.Unauthenticated,
    Code.PermissionDenied,
    Code.NotFound,
    Code.FailedPrecondition,
    Code.Unavailable,
  ])("keeps %s authority errors unchanged", async (code) => {
    const error = new ConnectError("current server authority", code);
    vi.mocked(organizationClient.setHumanStatus).mockRejectedValueOnce(error);
    const request = create(SetHumanStatusRequestSchema, {
      requestId: "caller-request-id",
      humanId: "human-1",
    });

    await expect(setHumanStatus(request)).rejects.toBe(error);
    expect(organizationClient.setHumanStatus).toHaveBeenCalledWith(request);
  });
});
