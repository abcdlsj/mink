import type {
  AssignWorkRequest,
  CreateWorkRequest,
  GetWorkRequest,
  ListWorksRequest,
  RequestApprovalRequest,
  ResolveApprovalRequest,
  TransitionWorkRequest,
} from "../gen/sumi/work/v1/work_pb";
import { workClient } from "../api/clients";

export const listWorks = (request: ListWorksRequest, signal?: AbortSignal) =>
  workClient.listWorks(request, { signal });
export const getWork = (request: GetWorkRequest, signal?: AbortSignal) =>
  workClient.getWork(request, { signal });
export const createWork = (request: CreateWorkRequest) =>
  workClient.createWork(request);
export const assignWork = (request: AssignWorkRequest) =>
  workClient.assignWork(request);
export const transitionWork = (request: TransitionWorkRequest) =>
  workClient.transitionWork(request);
export const requestWorkApproval = (request: RequestApprovalRequest) =>
  workClient.requestApproval(request);
export const resolveWorkApproval = (request: ResolveApprovalRequest) =>
  workClient.resolveApproval(request);
