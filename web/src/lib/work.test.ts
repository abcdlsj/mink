import { create } from "@bufbuild/protobuf";
import { Code, ConnectError } from "@connectrpc/connect";
import { afterEach, describe, expect, it, vi } from "vitest";
import { workClient } from "../api/clients";
import {
  AssignWorkRequestSchema,
  AssignWorkResponseSchema,
  CreateWorkRequestSchema,
  CreateWorkResponseSchema,
  GetWorkRequestSchema,
  GetWorkResponseSchema,
  ListWorksRequestSchema,
  ListWorksResponseSchema,
  RequestApprovalRequestSchema,
  RequestApprovalResponseSchema,
  ResolveApprovalRequestSchema,
  ResolveApprovalResponseSchema,
  TransitionWorkRequestSchema,
  TransitionWorkResponseSchema,
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

afterEach(() => vi.restoreAllMocks());

describe("work transport", () => {
  it("keeps all seven generated RPC request, response, and error identities", async () => {
    const controller = new AbortController();

    const listRequest = create(ListWorksRequestSchema, {
      cursor: "opaque-server-cursor",
      limit: 37,
    });
    const listResponse = create(ListWorksResponseSchema, {
      nextCursor: "next-opaque-cursor",
    });
    const listResult = Promise.resolve(listResponse);
    const listSpy = vi
      .spyOn(workClient, "listWorks")
      .mockReturnValueOnce(listResult);
    const listed = listWorks(listRequest, controller.signal);
    expect(listed).toBe(listResult);
    await expect(listed).resolves.toBe(listResponse);
    expect(listSpy).toHaveBeenCalledWith(listRequest, {
      signal: controller.signal,
    });
    const listError = new ConnectError("list works", Code.Unavailable);
    const listFailure = Promise.reject(listError);
    listSpy.mockReturnValueOnce(listFailure);
    const listedFailure = listWorks(listRequest, controller.signal);
    expect(listedFailure).toBe(listFailure);
    await expect(listedFailure).rejects.toBe(listError);

    const getRequest = create(GetWorkRequestSchema, { workId: "work-get" });
    const getResponse = create(GetWorkResponseSchema);
    const getResult = Promise.resolve(getResponse);
    const getSpy = vi
      .spyOn(workClient, "getWork")
      .mockReturnValueOnce(getResult);
    const gotten = getWork(getRequest, controller.signal);
    expect(gotten).toBe(getResult);
    await expect(gotten).resolves.toBe(getResponse);
    expect(getSpy).toHaveBeenCalledWith(getRequest, {
      signal: controller.signal,
    });
    const getError = new ConnectError("get work", Code.NotFound);
    const getFailure = Promise.reject(getError);
    getSpy.mockReturnValueOnce(getFailure);
    const gottenFailure = getWork(getRequest, controller.signal);
    expect(gottenFailure).toBe(getFailure);
    await expect(gottenFailure).rejects.toBe(getError);

    const createRequest = create(CreateWorkRequestSchema, {
      requestId: "create-request-id",
      goal: "ship it",
    });
    const createResponse = create(CreateWorkResponseSchema);
    const createResult = Promise.resolve(createResponse);
    const createSpy = vi
      .spyOn(workClient, "createWork")
      .mockReturnValueOnce(createResult);
    const created = createWork(createRequest);
    expect(created).toBe(createResult);
    await expect(created).resolves.toBe(createResponse);
    expect(createSpy).toHaveBeenCalledWith(createRequest);
    const createError = new ConnectError("create work", Code.Unauthenticated);
    const createFailure = Promise.reject(createError);
    createSpy.mockReturnValueOnce(createFailure);
    const createdFailure = createWork(createRequest);
    expect(createdFailure).toBe(createFailure);
    await expect(createdFailure).rejects.toBe(createError);

    const assignRequest = create(AssignWorkRequestSchema, {
      requestId: "assign-request-id",
      workId: "work-assign",
      agentId: "agent-assign",
    });
    const assignResponse = create(AssignWorkResponseSchema);
    const assignResult = Promise.resolve(assignResponse);
    const assignSpy = vi
      .spyOn(workClient, "assignWork")
      .mockReturnValueOnce(assignResult);
    const assigned = assignWork(assignRequest);
    expect(assigned).toBe(assignResult);
    await expect(assigned).resolves.toBe(assignResponse);
    expect(assignSpy).toHaveBeenCalledWith(assignRequest);
    const assignError = new ConnectError("assign work", Code.PermissionDenied);
    const assignFailure = Promise.reject(assignError);
    assignSpy.mockReturnValueOnce(assignFailure);
    const assignedFailure = assignWork(assignRequest);
    expect(assignedFailure).toBe(assignFailure);
    await expect(assignedFailure).rejects.toBe(assignError);

    const transitionRequest = create(TransitionWorkRequestSchema, {
      requestId: "transition-request-id",
      workId: "work-transition",
    });
    const transitionResponse = create(TransitionWorkResponseSchema);
    const transitionResult = Promise.resolve(transitionResponse);
    const transitionSpy = vi
      .spyOn(workClient, "transitionWork")
      .mockReturnValueOnce(transitionResult);
    const transitioned = transitionWork(transitionRequest);
    expect(transitioned).toBe(transitionResult);
    await expect(transitioned).resolves.toBe(transitionResponse);
    expect(transitionSpy).toHaveBeenCalledWith(transitionRequest);
    const transitionError = new ConnectError("transition work", Code.Aborted);
    const transitionFailure = Promise.reject(transitionError);
    transitionSpy.mockReturnValueOnce(transitionFailure);
    const transitionedFailure = transitionWork(transitionRequest);
    expect(transitionedFailure).toBe(transitionFailure);
    await expect(transitionedFailure).rejects.toBe(transitionError);

    const approvalRequest = create(RequestApprovalRequestSchema, {
      requestId: "approval-request-id",
      workId: "work-approval",
      question: "approve?",
    });
    const approvalResponse = create(RequestApprovalResponseSchema);
    const approvalResult = Promise.resolve(approvalResponse);
    const approvalSpy = vi
      .spyOn(workClient, "requestApproval")
      .mockReturnValueOnce(approvalResult);
    const requestedApproval = requestWorkApproval(approvalRequest);
    expect(requestedApproval).toBe(approvalResult);
    await expect(requestedApproval).resolves.toBe(approvalResponse);
    expect(approvalSpy).toHaveBeenCalledWith(approvalRequest);
    const approvalError = new ConnectError(
      "request approval",
      Code.FailedPrecondition,
    );
    const approvalFailure = Promise.reject(approvalError);
    approvalSpy.mockReturnValueOnce(approvalFailure);
    const requestedApprovalFailure = requestWorkApproval(approvalRequest);
    expect(requestedApprovalFailure).toBe(approvalFailure);
    await expect(requestedApprovalFailure).rejects.toBe(approvalError);

    const resolveRequest = create(ResolveApprovalRequestSchema, {
      requestId: "resolve-request-id",
      approvalId: "approval-resolve",
    });
    const resolveResponse = create(ResolveApprovalResponseSchema);
    const resolveResult = Promise.resolve(resolveResponse);
    const resolveSpy = vi
      .spyOn(workClient, "resolveApproval")
      .mockReturnValueOnce(resolveResult);
    const resolved = resolveWorkApproval(resolveRequest);
    expect(resolved).toBe(resolveResult);
    await expect(resolved).resolves.toBe(resolveResponse);
    expect(resolveSpy).toHaveBeenCalledWith(resolveRequest);
    const resolveError = new ConnectError("resolve approval", Code.Internal);
    const resolveFailure = Promise.reject(resolveError);
    resolveSpy.mockReturnValueOnce(resolveFailure);
    const resolvedFailure = resolveWorkApproval(resolveRequest);
    expect(resolvedFailure).toBe(resolveFailure);
    await expect(resolvedFailure).rejects.toBe(resolveError);
  });
});
