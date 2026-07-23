import { create } from "@bufbuild/protobuf";
import { Code, ConnectError } from "@connectrpc/connect";
import { afterEach, describe, expect, it, vi } from "vitest";
import { humanInboxClient } from "../api/clients";
import {
  ListHumanInboxItemsRequestSchema,
  ListHumanInboxItemsResponseSchema,
} from "../gen/sumi/inbox/v1/inbox_pb";
import { listHumanInboxItems } from "./humanInbox";

afterEach(() => vi.restoreAllMocks());

describe("human inbox transport", () => {
  it("forwards the generated request, caller abort signal, response, and typed failure", async () => {
    const request = create(ListHumanInboxItemsRequestSchema, { limit: 17 });
    const response = create(ListHumanInboxItemsResponseSchema);
    const controller = new AbortController();
    const result = Promise.resolve(response);
    const client = vi
      .spyOn(humanInboxClient, "listHumanInboxItems")
      .mockReturnValueOnce(result);

    const received = listHumanInboxItems(request, controller.signal);
    expect(received).toBe(result);
    await expect(received).resolves.toBe(response);
    expect(client).toHaveBeenCalledWith(request, { signal: controller.signal });

    const failure = new ConnectError("denied", Code.PermissionDenied);
    const failed = Promise.reject(failure);
    client.mockReturnValueOnce(failed);
    await expect(listHumanInboxItems(request, controller.signal)).rejects.toBe(failure);
  });
});
