import { create } from "@bufbuild/protobuf";
import { afterEach, describe, expect, it, vi } from "vitest";
import { collaborationClient, inboxClient } from "../api/clients";
import {
  ClaimInboxItemRequestSchema,
  ClaimInboxItemResponseSchema,
  CompleteInboxItemRequestSchema,
  CompleteInboxItemResponseSchema,
  InboxItemSchema,
  ListInboxItemsRequestSchema,
  ListInboxItemsResponseSchema,
  ObserveTargetResponseSchema,
} from "../gen/sumi/inbox/v1/inbox_pb";
import {
  GetMessageResponseSchema,
  MessageSchema,
  MessageTargetSchema,
} from "../gen/sumi/space/v1/space_pb";
import {
  claimInboxItem,
  completeInboxItem,
  listInboxItems,
  resolveInboxDestination,
} from "./inbox";

afterEach(() => vi.restoreAllMocks());

describe("message inbox transport", () => {
  it("forwards list and mutations through the shared Inbox service", async () => {
    const listRequest = create(ListInboxItemsRequestSchema, { limit: 17 });
    const listResponse = create(ListInboxItemsResponseSchema);
    const controller = new AbortController();
    vi.spyOn(inboxClient, "listInboxItems").mockResolvedValueOnce(listResponse);
    await expect(listInboxItems(listRequest, controller.signal)).resolves.toBe(
      listResponse,
    );
    expect(inboxClient.listInboxItems).toHaveBeenCalledWith(listRequest, {
      signal: controller.signal,
    });

    const claimRequest = create(ClaimInboxItemRequestSchema, {
      requestId: crypto.randomUUID(),
      inboxItemId: crypto.randomUUID(),
    });
    const claimResponse = create(ClaimInboxItemResponseSchema);
    vi.spyOn(inboxClient, "claimInboxItem").mockResolvedValueOnce(
      claimResponse,
    );
    await expect(claimInboxItem(claimRequest)).resolves.toBe(claimResponse);

    const completeRequest = create(CompleteInboxItemRequestSchema, {
      requestId: crypto.randomUUID(),
      inboxItemId: crypto.randomUUID(),
    });
    const completeResponse = create(CompleteInboxItemResponseSchema);
    vi.spyOn(inboxClient, "completeInboxItem").mockResolvedValueOnce(
      completeResponse,
    );
    await expect(completeInboxItem(completeRequest)).resolves.toBe(
      completeResponse,
    );
  });

  it("observes attention and resolves a thread root without copying message bodies into Inbox", async () => {
    const root = create(MessageSchema, { id: crypto.randomUUID() });
    const item = create(InboxItemSchema, {
      id: crypto.randomUUID(),
      spaceId: crypto.randomUUID(),
      triggerMessageId: crypto.randomUUID(),
      target: create(MessageTargetSchema, {
        target: { case: "threadRootMessageId", value: root.id },
      }),
    });
    vi.spyOn(inboxClient, "observeTarget").mockResolvedValueOnce(
      create(ObserveTargetResponseSchema),
    );
    vi.spyOn(collaborationClient, "getMessage").mockResolvedValueOnce(
      create(GetMessageResponseSchema, { message: root }),
    );

    await expect(resolveInboxDestination(item)).resolves.toEqual({
      spaceId: item.spaceId,
      threadRoot: root,
    });
    expect(inboxClient.observeTarget).toHaveBeenCalledOnce();
    expect(collaborationClient.getMessage).toHaveBeenCalledWith({
      messageId: root.id,
    });
  });
});
