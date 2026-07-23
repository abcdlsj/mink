import { create } from "@bufbuild/protobuf";
import { Code, ConnectError } from "@connectrpc/connect";
import { afterEach, describe, expect, it, vi } from "vitest";
import { artifactClient } from "../api/clients";
import {
  FetchArtifactMetadataSchema,
  FetchArtifactRequestSchema,
  FetchArtifactResponseSchema,
  GetArtifactRequestSchema,
  GetArtifactResponseSchema,
  GrantArtifactRequestSchema,
  GrantArtifactResponseSchema,
  ListArtifactsRequestSchema,
  ListArtifactsResponseSchema,
  RevokeArtifactGrantRequestSchema,
  RevokeArtifactGrantResponseSchema,
  type FetchArtifactResponse,
} from "../gen/sumi/artifact/v1/artifact_pb";
import {
  fetchArtifact,
  getArtifact,
  grantArtifact,
  listArtifacts,
  revokeArtifactGrant,
} from "./artifact";

afterEach(() => vi.restoreAllMocks());

describe("artifact transport", () => {
  it("keeps all five generated RPC request, response, and error identities", async () => {
    const controller = new AbortController();

    const listRequest = create(ListArtifactsRequestSchema, {
      owningWorkId: "work-list",
      afterArtifactId: "opaque-server-keyset",
      limit: 17,
    });
    const listResponse = create(ListArtifactsResponseSchema, {
      nextArtifactId: "next-opaque-keyset",
    });
    const listResult = Promise.resolve(listResponse);
    const listSpy = vi
      .spyOn(artifactClient, "listArtifacts")
      .mockReturnValueOnce(listResult);
    const listed = listArtifacts(listRequest, controller.signal);
    expect(listed).toBe(listResult);
    await expect(listed).resolves.toBe(listResponse);
    expect(listSpy).toHaveBeenCalledWith(listRequest, {
      signal: controller.signal,
    });
    const listError = new ConnectError("list artifacts", Code.Unavailable);
    const listFailure = Promise.reject(listError);
    listSpy.mockReturnValueOnce(listFailure);
    const listedFailure = listArtifacts(listRequest, controller.signal);
    expect(listedFailure).toBe(listFailure);
    await expect(listedFailure).rejects.toBe(listError);

    const getRequest = create(GetArtifactRequestSchema, {
      artifactId: "artifact-get",
    });
    const getResponse = create(GetArtifactResponseSchema);
    const getResult = Promise.resolve(getResponse);
    const getSpy = vi
      .spyOn(artifactClient, "getArtifact")
      .mockReturnValueOnce(getResult);
    const gotten = getArtifact(getRequest, controller.signal);
    expect(gotten).toBe(getResult);
    await expect(gotten).resolves.toBe(getResponse);
    expect(getSpy).toHaveBeenCalledWith(getRequest, {
      signal: controller.signal,
    });
    const getError = new ConnectError("get artifact", Code.NotFound);
    const getFailure = Promise.reject(getError);
    getSpy.mockReturnValueOnce(getFailure);
    const gottenFailure = getArtifact(getRequest, controller.signal);
    expect(gottenFailure).toBe(getFailure);
    await expect(gottenFailure).rejects.toBe(getError);

    const grantRequest = create(GrantArtifactRequestSchema, {
      requestId: "grant-request-id",
      artifactId: "artifact-grant",
    });
    const grantResponse = create(GrantArtifactResponseSchema);
    const grantResult = Promise.resolve(grantResponse);
    const grantSpy = vi
      .spyOn(artifactClient, "grantArtifact")
      .mockReturnValueOnce(grantResult);
    const granted = grantArtifact(grantRequest);
    expect(granted).toBe(grantResult);
    await expect(granted).resolves.toBe(grantResponse);
    expect(grantSpy).toHaveBeenCalledWith(grantRequest);
    const grantError = new ConnectError(
      "grant artifact",
      Code.PermissionDenied,
    );
    const grantFailure = Promise.reject(grantError);
    grantSpy.mockReturnValueOnce(grantFailure);
    const grantedFailure = grantArtifact(grantRequest);
    expect(grantedFailure).toBe(grantFailure);
    await expect(grantedFailure).rejects.toBe(grantError);

    const revokeRequest = create(RevokeArtifactGrantRequestSchema, {
      requestId: "revoke-request-id",
      grantId: "grant-revoke",
    });
    const revokeResponse = create(RevokeArtifactGrantResponseSchema);
    const revokeResult = Promise.resolve(revokeResponse);
    const revokeSpy = vi
      .spyOn(artifactClient, "revokeArtifactGrant")
      .mockReturnValueOnce(revokeResult);
    const revoked = revokeArtifactGrant(revokeRequest);
    expect(revoked).toBe(revokeResult);
    await expect(revoked).resolves.toBe(revokeResponse);
    expect(revokeSpy).toHaveBeenCalledWith(revokeRequest);
    const revokeError = new ConnectError("revoke artifact grant", Code.Aborted);
    const revokeFailure = Promise.reject(revokeError);
    revokeSpy.mockReturnValueOnce(revokeFailure);
    const revokedFailure = revokeArtifactGrant(revokeRequest);
    expect(revokedFailure).toBe(revokeFailure);
    await expect(revokedFailure).rejects.toBe(revokeError);

    const fetchRequest = create(FetchArtifactRequestSchema, {
      artifactId: "artifact-fetch",
      version: 9n,
    });
    const metadata = create(FetchArtifactResponseSchema, {
      payload: {
        case: "metadata",
        value: create(FetchArtifactMetadataSchema),
      },
    });
    const firstChunk = create(FetchArtifactResponseSchema, {
      payload: { case: "chunk", value: new Uint8Array([1, 2]) },
    });
    const secondChunk = create(FetchArtifactResponseSchema, {
      payload: { case: "chunk", value: new Uint8Array([3]) },
    });
    const stream: AsyncIterable<FetchArtifactResponse> = {
      async *[Symbol.asyncIterator]() {
        yield metadata;
        yield firstChunk;
        yield secondChunk;
      },
    };
    const fetchSpy = vi
      .spyOn(artifactClient, "fetchArtifact")
      .mockReturnValueOnce(stream);
    const fetched = fetchArtifact(fetchRequest, controller.signal);
    expect(fetched).toBe(stream);
    const frames: FetchArtifactResponse[] = [];
    for await (const frame of fetched) frames.push(frame);
    expect(frames).toEqual([metadata, firstChunk, secondChunk]);
    expect(fetchSpy).toHaveBeenCalledWith(fetchRequest, {
      signal: controller.signal,
    });
    const fetchError = new ConnectError("fetch artifact", Code.Internal);
    const failedStream: AsyncIterable<FetchArtifactResponse> = {
      async *[Symbol.asyncIterator]() {
        yield metadata;
        throw fetchError;
      },
    };
    fetchSpy.mockReturnValueOnce(failedStream);
    const fetchedFailure = fetchArtifact(fetchRequest, controller.signal);
    expect(fetchedFailure).toBe(failedStream);
    const iterator = fetchedFailure[Symbol.asyncIterator]();
    await expect(iterator.next()).resolves.toMatchObject({ done: false });
    await expect(iterator.next()).rejects.toBe(fetchError);
  });

  it("preserves caller abort through the real generated fetch client surface", async () => {
    const controller = new AbortController();
    const request = create(FetchArtifactRequestSchema, {
      artifactId: "artifact-abort",
      version: 10n,
    });
    const abortError = new DOMException("cancelled", "AbortError");
    const stream: AsyncIterable<FetchArtifactResponse> = {
      async *[Symbol.asyncIterator]() {
        if (controller.signal.aborted) throw abortError;
      },
    };
    const fetchSpy = vi
      .spyOn(artifactClient, "fetchArtifact")
      .mockReturnValueOnce(stream);
    controller.abort();

    const iterator = fetchArtifact(request, controller.signal)[
      Symbol.asyncIterator
    ]();
    await expect(iterator.next()).rejects.toBe(abortError);
    expect(fetchSpy).toHaveBeenCalledWith(request, {
      signal: controller.signal,
    });
  });
});
