import { create } from "@bufbuild/protobuf";
import type {
  ClaimInboxItemRequest,
  CompleteInboxItemRequest,
  InboxItem,
  ListInboxItemsRequest,
  ObserveTargetRequest,
} from "../gen/sumi/inbox/v1/inbox_pb";
import { ObserveTargetRequestSchema } from "../gen/sumi/inbox/v1/inbox_pb";
import type { Message } from "../gen/sumi/space/v1/space_pb";
import { collaborationClient, inboxClient } from "../api/clients";

export type InboxDestination = {
  spaceId: string;
  threadRoot?: Message;
};

export const listInboxItems = (
  request: ListInboxItemsRequest,
  signal?: AbortSignal,
) => inboxClient.listInboxItems(request, { signal });

export const claimInboxItem = (request: ClaimInboxItemRequest) =>
  inboxClient.claimInboxItem(request);

export const completeInboxItem = (request: CompleteInboxItemRequest) =>
  inboxClient.completeInboxItem(request);

export const observeInboxTarget = (
  request: ObserveTargetRequest,
  signal?: AbortSignal,
) => inboxClient.observeTarget(request, { signal });

export async function resolveInboxDestination(
  item: InboxItem,
): Promise<InboxDestination> {
  if (!item.target?.target.case) {
    throw new Error("Inbox item target was empty");
  }
  await observeInboxTarget(
    create(ObserveTargetRequestSchema, { target: item.target, limit: 1 }),
  );
  if (item.target.target.case === "spaceId") {
    return { spaceId: item.spaceId };
  }
  const response = await collaborationClient.getMessage({
    messageId: item.target.target.value,
  });
  if (!response.message) {
    throw new Error("Inbox thread root was empty");
  }
  return { spaceId: item.spaceId, threadRoot: response.message };
}
