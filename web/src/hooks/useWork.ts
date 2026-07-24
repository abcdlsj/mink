import { Code, ConnectError } from "@connectrpc/connect";
import { create } from "@bufbuild/protobuf";
import { useCallback, useEffect, useRef, useState } from "react";
import {
  AssignWorkRequestSchema,
  CreateWorkRequestSchema,
  GetWorkRequestSchema,
  ListWorksRequestSchema,
  RequestApprovalRequestSchema,
  ResolveApprovalRequestSchema,
  TransitionWorkRequestSchema,
} from "../gen/sumi/work/v1/work_pb";
import type {
  AssignWorkRequest,
  AssignWorkResponse,
  CreateWorkRequest,
  CreateWorkResponse,
  GetWorkResponse,
  RequestApprovalRequest,
  RequestApprovalResponse,
  ResolveApprovalRequest,
  ResolveApprovalResponse,
  TransitionWorkRequest,
  TransitionWorkResponse,
  Work,
} from "../gen/sumi/work/v1/work_pb";
import {
  assignWork,
  createWork,
  getWork,
  listWorks,
  requestWorkApproval,
  resolveWorkApproval,
  transitionWork,
} from "../lib/work";
import { PayloadRequestLifecycle } from "../lib/collaboration/requestLifecycle";
import {
  useRemoteSnapshot,
  type SnapshotBinding,
  type SnapshotFailure,
} from "./useRemoteSnapshot";

const pageSize = 50;

export type WorkSnapshot = {
  works: Work[];
  nextCursor: string;
  detail?: GetWorkResponse;
};

export type WorkActionState = {
  pending: boolean;
  error?: unknown;
};

type WorkFailure = SnapshotFailure & { code: Code };

type HumanCreateMutation = Omit<
  CreateWorkRequest,
  "$typeName" | "requestId" | "runId" | "runAttempt" | "runFence"
>;

type HumanAssignMutation = Omit<
  AssignWorkRequest,
  "$typeName" | "requestId" | "runId" | "runAttempt" | "runFence"
>;

type HumanTransitionMutation = Omit<
  TransitionWorkRequest,
  "$typeName" | "requestId" | "runId" | "runAttempt" | "runFence"
>;

type HumanApprovalMutation = Omit<
  RequestApprovalRequest,
  "$typeName" | "requestId" | "runId" | "runAttempt" | "runFence"
>;

type WorkMutation =
  | HumanCreateMutation
  | HumanAssignMutation
  | HumanTransitionMutation
  | HumanApprovalMutation
  | Omit<ResolveApprovalRequest, "$typeName" | "requestId">;

export function useWork(workId: string | undefined, enabled = true) {
  const targetId = workId ?? "work-index";
  const load = useCallback(
    async (signal: AbortSignal): Promise<WorkSnapshot> => {
      const [listed, detail] = await Promise.all([
        listWorks(
          create(ListWorksRequestSchema, { cursor: "", limit: pageSize }),
          signal,
        ),
        workId
          ? getWork(create(GetWorkRequestSchema, { workId }), signal)
          : Promise.resolve(undefined),
      ]);
      return { works: listed.works, nextCursor: listed.nextCursor, detail };
    },
    [workId],
  );
  const classifyError = useCallback((error: unknown): WorkFailure => {
    const code = ConnectError.from(error).code;
    return {
      message: error instanceof Error ? error.message : "Could not load Work.",
      discard:
        code === Code.Unauthenticated ||
        code === Code.PermissionDenied ||
        code === Code.NotFound,
      code,
    };
  }, []);
  const snapshot = useRemoteSnapshot({
    enabled,
    targetId,
    load,
    classifyError,
  });
  const [action, setAction] = useState<WorkActionState>({ pending: false });
  const createLifecycle = useRef(
    new PayloadRequestLifecycle<WorkMutation>(fingerprint),
  );
  const assignLifecycle = useRef(
    new PayloadRequestLifecycle<WorkMutation>(fingerprint),
  );
  const transitionLifecycle = useRef(
    new PayloadRequestLifecycle<WorkMutation>(fingerprint),
  );
  const requestApprovalLifecycle = useRef(
    new PayloadRequestLifecycle<WorkMutation>(fingerprint),
  );
  const resolveApprovalLifecycle = useRef(
    new PayloadRequestLifecycle<WorkMutation>(fingerprint),
  );
  const actionGeneration = useRef(0);
  const pageGeneration = useRef(0);
  const pageController = useRef<AbortController | undefined>(undefined);

  useEffect(() => {
    actionGeneration.current += 1;
    pageGeneration.current += 1;
    pageController.current?.abort();
    setAction({ pending: false });
    return () => {
      actionGeneration.current += 1;
      pageGeneration.current += 1;
      pageController.current?.abort();
    };
  }, [enabled, targetId]);

  const refresh = useCallback(() => {
    pageGeneration.current += 1;
    pageController.current?.abort();
    return snapshot.refresh("retrying");
  }, [snapshot]);

  const loadMore = useCallback(async () => {
    const current = snapshot.state.data;
    const binding = snapshot.capture();
    if (!current?.nextCursor || !binding) return;
    const generation = ++pageGeneration.current;
    pageController.current?.abort();
    const controller = new AbortController();
    pageController.current = controller;
    try {
      const page = await listWorks(
        create(ListWorksRequestSchema, {
          cursor: current.nextCursor,
          limit: pageSize,
        }),
        controller.signal,
      );
      if (
        controller.signal.aborted ||
        pageGeneration.current !== generation ||
        !snapshot.isCurrent(binding)
      )
        return;
      snapshot.setState({
        status: "ready",
        targetId: binding.targetId,
        data: {
          ...current,
          works: [...current.works, ...page.works],
          nextCursor: page.nextCursor,
        },
      });
    } catch (error) {
      if (
        controller.signal.aborted ||
        pageGeneration.current !== generation ||
        !snapshot.isCurrent(binding)
      )
        return;
      const failure = classifyError(error);
      snapshot.setState({
        status: failure.discard ? "error" : "stale",
        targetId: binding.targetId,
        data: failure.discard ? undefined : current,
        failure,
      });
    }
  }, [classifyError, snapshot]);

  const mutate = useCallback(
    async <T>(
      binding: SnapshotBinding,
      lifecycle: PayloadRequestLifecycle<WorkMutation>,
      payload: WorkMutation,
      invoke: (requestId: string) => Promise<T>,
    ): Promise<T> => {
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
        if (actionGeneration.current === generation) {
          setAction((current) => ({ ...current, pending: false }));
        }
      }
    },
    [refresh, snapshot],
  );

  const requireBinding = useCallback(() => {
    const binding = snapshot.capture();
    if (!binding) throw new Error("Work snapshot is not active");
    return binding;
  }, [snapshot]);

  return {
    status: flattenStatus(snapshot.state.status),
    data: snapshot.state.data,
    error: snapshot.state.failure?.message,
    action,
    refresh,
    loadMore,
    create: (payload: HumanCreateMutation) =>
      mutate(requireBinding(), createLifecycle.current, payload, (requestId) =>
        createWork(create(CreateWorkRequestSchema, { ...payload, requestId })),
      ) as Promise<CreateWorkResponse>,
    assign: (payload: HumanAssignMutation) =>
      mutate(requireBinding(), assignLifecycle.current, payload, (requestId) =>
        assignWork(create(AssignWorkRequestSchema, { ...payload, requestId })),
      ) as Promise<AssignWorkResponse>,
    transition: (payload: HumanTransitionMutation) =>
      mutate(
        requireBinding(),
        transitionLifecycle.current,
        payload,
        (requestId) =>
          transitionWork(
            create(TransitionWorkRequestSchema, { ...payload, requestId }),
          ),
      ) as Promise<TransitionWorkResponse>,
    requestApproval: (payload: HumanApprovalMutation) =>
      mutate(
        requireBinding(),
        requestApprovalLifecycle.current,
        payload,
        (requestId) =>
          requestWorkApproval(
            create(RequestApprovalRequestSchema, { ...payload, requestId }),
          ),
      ) as Promise<RequestApprovalResponse>,
    resolveApproval: (
      payload: Omit<ResolveApprovalRequest, "$typeName" | "requestId">,
    ) =>
      mutate(
        requireBinding(),
        resolveApprovalLifecycle.current,
        payload,
        (requestId) =>
          resolveWorkApproval(
            create(ResolveApprovalRequestSchema, { ...payload, requestId }),
          ),
      ) as Promise<ResolveApprovalResponse>,
  };
}

function flattenStatus(
  status: ReturnType<typeof useRemoteSnapshot>["state"]["status"],
) {
  return status === "retrying" ? "loading" : status;
}

function fingerprint(payload: WorkMutation) {
  return JSON.stringify(payload, (_, value) =>
    typeof value === "bigint" ? value.toString() : value,
  );
}
