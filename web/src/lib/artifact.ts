import type {
  FetchArtifactResponse,
  FetchArtifactRequest,
  GetArtifactRequest,
  GrantArtifactRequest,
  ListArtifactsRequest,
  RevokeArtifactGrantRequest,
} from "../gen/sumi/artifact/v1/artifact_pb";
import { artifactClient } from "../api/clients";

export const listArtifacts = (
  request: ListArtifactsRequest,
  signal?: AbortSignal,
) => artifactClient.listArtifacts(request, { signal });
export const getArtifact = (
  request: GetArtifactRequest,
  signal?: AbortSignal,
) => artifactClient.getArtifact(request, { signal });
export const grantArtifact = (request: GrantArtifactRequest) =>
  artifactClient.grantArtifact(request);
export const revokeArtifactGrant = (request: RevokeArtifactGrantRequest) =>
  artifactClient.revokeArtifactGrant(request);
export const fetchArtifact = (
  request: FetchArtifactRequest,
  signal?: AbortSignal,
): AsyncIterable<FetchArtifactResponse> =>
  artifactClient.fetchArtifact(request, { signal });
