import { create } from "@bufbuild/protobuf";
import { Code, ConnectError } from "@connectrpc/connect";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { artifactClient } from "../api/clients";
import {
  FetchArtifactMetadataSchema,
  FetchArtifactRequestSchema,
  FetchArtifactResponseSchema,
  GetArtifactRequestSchema,
  GrantArtifactRequestSchema,
  ListArtifactsRequestSchema,
  RevokeArtifactGrantRequestSchema,
  type FetchArtifactResponse,
} from "../gen/sumi/artifact/v1/artifact_pb";
import {
  fetchArtifact,
  getArtifact,
  grantArtifact,
  listArtifacts,
  revokeArtifactGrant,
} from "./artifact";

vi.mock("../api/clients", () => ({
  artifactClient: {
    listArtifacts: vi.fn(),
    getArtifact: vi.fn(),
    grantArtifact: vi.fn(),
    revokeArtifactGrant: vi.fn(),
    fetchArtifact: vi.fn(),
  },
}));

beforeEach(() => vi.clearAllMocks());

describe("artifact transport", () => {
  it("forwards generated requests, opaque keyset, request IDs, and signal unchanged", () => {
    const controller = new AbortController();
    const listRequest = create(ListArtifactsRequestSchema, {
      owningWorkId: "work-1",
      afterArtifactId: "opaque-server-keyset",
      limit: 17,
    });
    const getRequest = create(GetArtifactRequestSchema, {
      artifactId: "artifact-1",
    });
    const grantRequest = create(GrantArtifactRequestSchema, {
      requestId: "grant-request-id",
      artifactId: "artifact-1",
    });
    const revokeRequest = create(RevokeArtifactGrantRequestSchema, {
      requestId: "revoke-request-id",
      grantId: "grant-1",
    });
    const fetchRequest = create(FetchArtifactRequestSchema, {
      artifactId: "artifact-1",
      version: 9n,
    });

    listArtifacts(listRequest, controller.signal);
    getArtifact(getRequest, controller.signal);
    grantArtifact(grantRequest);
    revokeArtifactGrant(revokeRequest);
    fetchArtifact(fetchRequest, controller.signal);

    expect(artifactClient.listArtifacts).toHaveBeenCalledWith(listRequest, {
      signal: controller.signal,
    });
    expect(artifactClient.getArtifact).toHaveBeenCalledWith(getRequest, {
      signal: controller.signal,
    });
    expect(artifactClient.grantArtifact).toHaveBeenCalledWith(grantRequest);
    expect(artifactClient.revokeArtifactGrant).toHaveBeenCalledWith(
      revokeRequest,
    );
    expect(artifactClient.fetchArtifact).toHaveBeenCalledWith(fetchRequest, {
      signal: controller.signal,
    });
  });

  it("returns the typed fetch iterable without buffering or changing frame order", async () => {
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
    vi.mocked(artifactClient.fetchArtifact).mockReturnValueOnce(stream);
    const request = create(FetchArtifactRequestSchema, {
      artifactId: "artifact-1",
      version: 9n,
    });

    const result = fetchArtifact(request);

    expect(result).toBe(stream);
    const frames: FetchArtifactResponse[] = [];
    for await (const frame of result) frames.push(frame);
    expect(frames).toEqual([metadata, firstChunk, secondChunk]);
  });

  it("keeps terminal stream errors typed and forwards caller abort", async () => {
    const controller = new AbortController();
    const error = new ConnectError("stream unavailable", Code.Unavailable);
    const stream: AsyncIterable<FetchArtifactResponse> = {
      async *[Symbol.asyncIterator]() {
        yield create(FetchArtifactResponseSchema, {
          payload: {
            case: "metadata",
            value: create(FetchArtifactMetadataSchema),
          },
        });
        throw error;
      },
    };
    vi.mocked(artifactClient.fetchArtifact).mockReturnValueOnce(stream);
    const request = create(FetchArtifactRequestSchema, {
      artifactId: "artifact-1",
      version: 9n,
    });

    const result = fetchArtifact(request, controller.signal);
    const iterator = result[Symbol.asyncIterator]();
    await expect(iterator.next()).resolves.toMatchObject({ done: false });
    await expect(iterator.next()).rejects.toBe(error);
    expect(artifactClient.fetchArtifact).toHaveBeenCalledWith(request, {
      signal: controller.signal,
    });
  });

  it("does not replace a cancellation error from the generated stream", async () => {
    const controller = new AbortController();
    const error = new DOMException("cancelled", "AbortError");
    const stream: AsyncIterable<FetchArtifactResponse> = {
      async *[Symbol.asyncIterator]() {
        if (controller.signal.aborted) throw error;
      },
    };
    vi.mocked(artifactClient.fetchArtifact).mockReturnValueOnce(stream);
    const request = create(FetchArtifactRequestSchema, {
      artifactId: "artifact-1",
      version: 9n,
    });
    controller.abort();

    const iterator = fetchArtifact(request, controller.signal)[
      Symbol.asyncIterator
    ]();
    await expect(iterator.next()).rejects.toBe(error);
    expect(artifactClient.fetchArtifact).toHaveBeenCalledWith(request, {
      signal: controller.signal,
    });
  });

  it.each([
    Code.Unauthenticated,
    Code.PermissionDenied,
    Code.NotFound,
    Code.Aborted,
    Code.Unavailable,
  ])("keeps %s mutation errors unchanged", async (code) => {
    const error = new ConnectError("current artifact fact", code);
    vi.mocked(artifactClient.grantArtifact).mockRejectedValueOnce(error);
    const request = create(GrantArtifactRequestSchema, {
      requestId: "caller-request-id",
      artifactId: "artifact-1",
    });

    await expect(grantArtifact(request)).rejects.toBe(error);
    expect(artifactClient.grantArtifact).toHaveBeenCalledWith(request);
  });
});
