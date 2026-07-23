import type { ListWorkAttentionItemsRequest } from "../gen/sumi/inbox/v1/inbox_pb";
import { workAttentionClient } from "../api/clients";

export const listWorkAttentionItems = (
  request: ListWorkAttentionItemsRequest,
  signal?: AbortSignal,
) => workAttentionClient.listWorkAttentionItems(request, { signal });
