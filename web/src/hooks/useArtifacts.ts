import { create } from "@bufbuild/protobuf";
import { Code, ConnectError } from "@connectrpc/connect";
import { useCallback, useEffect, useRef, useState } from "react";
import {
  FetchArtifactRequestSchema,
  GetArtifactRequestSchema,
  GrantArtifactRequestSchema,
  ListArtifactsRequestSchema,
  RevokeArtifactGrantRequestSchema,
} from "../gen/sumi/artifact/v1/artifact_pb";
import type {
  FetchArtifactResponse,
  GetArtifactResponse,
  GrantArtifactRequest,
  GrantArtifactResponse,
  ListArtifactsResponse,
  RevokeArtifactGrantRequest,
  RevokeArtifactGrantResponse,
} from "../gen/sumi/artifact/v1/artifact_pb";
import { PayloadRequestLifecycle } from "../lib/collaboration/requestLifecycle";
import {
  fetchArtifact,
  getArtifact,
  grantArtifact,
  listArtifacts,
  revokeArtifactGrant,
} from "../lib/artifact";
import { useRemoteSnapshot, type SnapshotFailure } from "./useRemoteSnapshot";

const pageSize = 50;
type Failure = SnapshotFailure;
type Mutation =
  | Omit<GrantArtifactRequest, "$typeName" | "requestId">
  | Omit<RevokeArtifactGrantRequest, "$typeName" | "requestId">;
export type ArtifactSnapshot = {
  views: ListArtifactsResponse["views"];
  nextArtifactId: string;
  artifact?: GetArtifactResponse;
};
export type ArtifactFetch = {
  status: "idle" | "streaming" | "complete" | "cancelled" | "error";
  stream?: AsyncIterable<FetchArtifactResponse>;
  error?: unknown;
  cancel: () => void;
};

export function useArtifacts(
  owningWorkId: string | undefined,
  artifactId?: string,
  enabled = true,
) {
  const targetId = `${owningWorkId ?? "artifact-index"}:${artifactId ?? ""}`;
  const load = useCallback(
    async (signal: AbortSignal): Promise<ArtifactSnapshot> => {
      const [listed, artifact] = await Promise.all([
        listArtifacts(
          create(ListArtifactsRequestSchema, {
            owningWorkId: owningWorkId ?? "",
            afterArtifactId: "",
            limit: pageSize,
          }),
          signal,
        ),
        artifactId
          ? getArtifact(
              create(GetArtifactRequestSchema, { artifactId }),
              signal,
            )
          : Promise.resolve(undefined),
      ]);
      return {
        views: listed.views,
        nextArtifactId: listed.nextArtifactId,
        artifact,
      };
    },
    [artifactId, owningWorkId],
  );
  const classifyError = useCallback((error: unknown): Failure => {
    const code = ConnectError.from(error).code;
    return {
      message:
        error instanceof Error ? error.message : "Could not load Artifacts.",
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
  const [fetch, setFetch] = useState<ArtifactFetch>({
    status: "idle",
    cancel: () => {},
  });
  const fetchGeneration = useRef(0);
  const fetchController = useRef<AbortController | undefined>(undefined);
  const actionGeneration = useRef(0);
  const pageGeneration = useRef(0);
  const pageController = useRef<AbortController | undefined>(undefined);
  const grantLifecycle = useRef(
    new PayloadRequestLifecycle<Mutation>(fingerprint),
  );
  const revokeLifecycle = useRef(
    new PayloadRequestLifecycle<Mutation>(fingerprint),
  );
  useEffect(() => {
    actionGeneration.current += 1;
    pageGeneration.current += 1;
    pageController.current?.abort();
    fetchGeneration.current += 1;
    fetchController.current?.abort();
    setAction({ pending: false });
    setFetch({ status: "idle", cancel: () => {} });
    return () => {
      actionGeneration.current += 1;
      pageGeneration.current += 1;
      pageController.current?.abort();
      fetchGeneration.current += 1;
      fetchController.current?.abort();
    };
  }, [enabled, targetId]);
  const refresh = useCallback(() => {
    pageGeneration.current += 1;
    pageController.current?.abort();
    return snapshot.refresh("retrying");
  }, [snapshot]);
  const mutate = useCallback(
    async <T>(
      lifecycle: PayloadRequestLifecycle<Mutation>,
      payload: Mutation,
      invoke: (requestId: string) => Promise<T>,
    ) => {
      const binding = snapshot.capture();
      if (!binding) throw new Error("Artifact snapshot is not active");
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
  const loadMore = useCallback(async () => {
    const current = snapshot.state.data;
    const binding = snapshot.capture();
    if (!current?.nextArtifactId || !binding) return;
    const generation = ++pageGeneration.current;
    pageController.current?.abort();
    const controller = new AbortController();
    pageController.current = controller;
    try {
      const page = await listArtifacts(
        create(ListArtifactsRequestSchema, {
          owningWorkId: owningWorkId ?? "",
          afterArtifactId: current.nextArtifactId,
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
          views: [...current.views, ...page.views],
          nextArtifactId: page.nextArtifactId,
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
  }, [classifyError, owningWorkId, snapshot]);
  const startFetch = useCallback(
    (id: string, version: bigint) => {
      const binding = snapshot.capture();
      if (!binding) throw new Error("Artifact snapshot is not active");
      fetchController.current?.abort();
      const generation = ++fetchGeneration.current;
      const controller = new AbortController();
      fetchController.current = controller;
      const source = fetchArtifact(
        create(FetchArtifactRequestSchema, { artifactId: id, version }),
        controller.signal,
      );
      const stream: AsyncIterable<FetchArtifactResponse> = {
        async *[Symbol.asyncIterator]() {
          try {
            for await (const frame of source) {
              if (
                controller.signal.aborted ||
                fetchGeneration.current !== generation ||
                !snapshot.isCurrent(binding)
              )
                return;
              yield frame;
            }
            if (
              fetchGeneration.current === generation &&
              snapshot.isCurrent(binding)
            ) {
              fetchGeneration.current += 1;
              fetchController.current = undefined;
              setFetch((current) => ({ ...current, status: "complete" }));
            }
          } catch (error) {
            if (
              fetchGeneration.current === generation &&
              snapshot.isCurrent(binding)
            ) {
              fetchGeneration.current += 1;
              fetchController.current = undefined;
              setFetch((current) => ({
                ...current,
                status: controller.signal.aborted ? "cancelled" : "error",
                error: controller.signal.aborted ? undefined : error,
              }));
            }
            throw error;
          }
        },
      };
      const handle: ArtifactFetch = {
        status: "streaming",
        stream,
        cancel: () => {
          if (fetchGeneration.current !== generation) return;
          fetchGeneration.current += 1;
          controller.abort();
          setFetch({ status: "cancelled", cancel: () => {} });
        },
      };
      setFetch(handle);
      return handle;
    },
    [snapshot],
  );
  return {
    status:
      snapshot.state.status === "retrying" ? "loading" : snapshot.state.status,
    data: snapshot.state.data,
    error: snapshot.state.failure?.message,
    action,
    fetch,
    refresh,
    loadMore,
    grant: (payload: Omit<GrantArtifactRequest, "$typeName" | "requestId">) =>
      mutate(grantLifecycle.current, payload, (requestId) =>
        grantArtifact(
          create(GrantArtifactRequestSchema, { ...payload, requestId }),
        ),
      ) as Promise<GrantArtifactResponse>,
    revoke: (
      payload: Omit<RevokeArtifactGrantRequest, "$typeName" | "requestId">,
    ) =>
      mutate(revokeLifecycle.current, payload, (requestId) =>
        revokeArtifactGrant(
          create(RevokeArtifactGrantRequestSchema, { ...payload, requestId }),
        ),
      ) as Promise<RevokeArtifactGrantResponse>,
    startFetch,
  };
}

function fingerprint(payload: Mutation) {
  return JSON.stringify(payload, (_, value) =>
    typeof value === "bigint" ? value.toString() : value,
  );
}
