import { Code, ConnectError } from "@connectrpc/connect";

export function collaborationErrorMessage(error: unknown, action: string) {
  const connectError = ConnectError.from(error);
  if (connectError.code === Code.PermissionDenied) {
    return `The Server denied permission to ${action}. The loaded snapshot was kept.`;
  }
  if (connectError.code === Code.Unauthenticated) {
    return `Your Human session is no longer authorized to ${action}.`;
  }
  if (connectError.code === Code.InvalidArgument) {
    return connectError.rawMessage || `The ${action} request is invalid.`;
  }
  if (connectError.code === Code.AlreadyExists) {
    return `The ${action} request already exists.`;
  }
  if (connectError.code === Code.NotFound) {
    return `The target for ${action} is no longer available. Refresh and retry.`;
  }
  if (connectError.code === Code.Unavailable) {
    return `The Server is unavailable. Retry ${action} with the same content.`;
  }
  if (error instanceof Error && error.message) return error.message;
  return `Could not ${action}. Retry when the Server is available.`;
}

export function isInaccessibleCollaborationError(error: unknown) {
  const code = ConnectError.from(error).code;
  return (
    code === Code.Unauthenticated ||
    code === Code.PermissionDenied ||
    code === Code.NotFound
  );
}

export function isUnauthenticatedCollaborationError(error: unknown) {
  return ConnectError.from(error).code === Code.Unauthenticated;
}
