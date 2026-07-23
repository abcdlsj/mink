import { create } from "@bufbuild/protobuf";
import { Code, ConnectError } from "@connectrpc/connect";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { workClient } from "../api/clients";
import {
  AssignWorkRequestSchema,
  CreateWorkRequestSchema,
  GetWorkRequestSchema,
  ListWorksRequestSchema,
  RequestApprovalRequestSchema,
  ResolveApprovalRequestSchema,
  TransitionWorkRequestSchema,
} from "../gen/sumi/work/v1/work_pb";
import {
  assignWork,
  createWork,
  getWork,
  listWorks,
  requestWorkApproval,
  resolveWorkApproval,
  transitionWork,
} from "./work";

vi.mock("../api/clients", () => ({
  workClient: {
    listWorks: vi.fn(),
    getWork: vi.fn(),
    createWork: vi.fn(),
    assignWork: vi.fn(),
    transitionWork: vi.fn(),
    requestApproval: vi.fn(),
    resolveApproval: vi.fn(),
  },
}));

beforeEach(() => vi.clearAllMocks());

describe("work transport", () => {
  it("forwards every generated request, opaque cursor, request ID, and signal unchanged", () => {
    const controller = new AbortController();
    const listRequest = create(ListWorksRequestSchema, {
      cursor: "opaque-server-cursor",
      limit: 37,
    });
    const getRequest = create(GetWorkRequestSchema, { workId: "work-1" });
    const createRequest = create(CreateWorkRequestSchema, {
      requestId: "create-request-id",
      goal: "ship it",
    });
    const assignRequest = create(AssignWorkRequestSchema, {
      requestId: "assign-request-id",
      workId: "work-1",
      agentId: "agent-1",
    });
    const transitionRequest = create(TransitionWorkRequestSchema, {
      requestId: "transition-request-id",
      workId: "work-1",
    });
    const requestApprovalRequest = create(RequestApprovalRequestSchema, {
      requestId: "approval-request-id",
      workId: "work-1",
      question: "approve?",
    });
    const resolveApprovalRequest = create(ResolveApprovalRequestSchema, {
      requestId: "resolve-request-id",
      approvalId: "approval-1",
    });

    listWorks(listRequest, controller.signal);
    getWork(getRequest, controller.signal);
    createWork(createRequest);
    assignWork(assignRequest);
    transitionWork(transitionRequest);
    requestWorkApproval(requestApprovalRequest);
    resolveWorkApproval(resolveApprovalRequest);

    expect(workClient.listWorks).toHaveBeenCalledWith(listRequest, {
      signal: controller.signal,
    });
    expect(workClient.getWork).toHaveBeenCalledWith(getRequest, {
      signal: controller.signal,
    });
    expect(workClient.createWork).toHaveBeenCalledWith(createRequest);
    expect(workClient.assignWork).toHaveBeenCalledWith(assignRequest);
    expect(workClient.transitionWork).toHaveBeenCalledWith(transitionRequest);
    expect(workClient.requestApproval).toHaveBeenCalledWith(
      requestApprovalRequest,
    );
    expect(workClient.resolveApproval).toHaveBeenCalledWith(
      resolveApprovalRequest,
    );
  });

  it.each([
    Code.Unauthenticated,
    Code.PermissionDenied,
    Code.NotFound,
    Code.Aborted,
    Code.Unavailable,
  ])("keeps %s typed errors unchanged", async (code) => {
    const error = new ConnectError("server fact", code);
    vi.mocked(workClient.transitionWork).mockRejectedValueOnce(error);
    const request = create(TransitionWorkRequestSchema, {
      requestId: "caller-request-id",
      workId: "work-1",
    });

    await expect(transitionWork(request)).rejects.toBe(error);
    expect(workClient.transitionWork).toHaveBeenCalledWith(request);
  });
});
