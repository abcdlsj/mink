import type { ListHumanInboxItemsRequest } from "../gen/sumi/inbox/v1/inbox_pb";
import { humanInboxClient } from "../api/clients";

export const listHumanInboxItems = (
  request: ListHumanInboxItemsRequest,
  signal?: AbortSignal,
) => humanInboxClient.listHumanInboxItems(request, { signal });
