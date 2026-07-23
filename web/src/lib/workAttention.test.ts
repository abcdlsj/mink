import { create } from "@bufbuild/protobuf";
import { Code, ConnectError } from "@connectrpc/connect";
import { afterEach, describe, expect, it, vi } from "vitest";
import { workAttentionClient } from "../api/clients";
import {
  ListWorkAttentionItemsRequestSchema,
  ListWorkAttentionItemsResponseSchema,
} from "../gen/sumi/inbox/v1/inbox_pb";
import { listWorkAttentionItems } from "./workAttention";

afterEach(() => vi.restoreAllMocks());

describe("work attention transport", () => {
  it("forwards the generated request, caller abort signal, response, and typed failure", async () => {
    const request = create(ListWorkAttentionItemsRequestSchema, { limit: 17 });
    const response = create(ListWorkAttentionItemsResponseSchema);
    const controller = new AbortController();
    const result = Promise.resolve(response);
    const client = vi
      .spyOn(workAttentionClient, "listWorkAttentionItems")
      .mockReturnValueOnce(result);

    const received = listWorkAttentionItems(request, controller.signal);
    expect(received).toBe(result);
    await expect(received).resolves.toBe(response);
    expect(client).toHaveBeenCalledWith(request, { signal: controller.signal });

    const failure = new ConnectError("denied", Code.PermissionDenied);
    const failed = Promise.reject(failure);
    client.mockReturnValueOnce(failed);
    await expect(
      listWorkAttentionItems(request, controller.signal),
    ).rejects.toBe(failure);
  });
});
