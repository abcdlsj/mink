import { Code, ConnectError } from "@connectrpc/connect";
import { Capability, ScopeKind } from "../../gen/sumi/grant/v1/grant_pb";
import {
  HumanStatus,
  type Human,
} from "../../gen/sumi/organization/v1/organization_pb";
import type { Message } from "../../gen/sumi/space/v1/space_pb";
import {
  agentClient,
  collaborationClient,
  organizationClient,
} from "../../api/clients";
import { loadMessagePages, mergeMessages } from "./pagination";
import { checkPermission, loadSpacePermissions } from "./permissions";
import { required } from "./required";
import type {
  ConversationSnapshot,
  DirectorySnapshot,
  MessagePage,
  ThreadSnapshot,
} from "./types";

type SignalOptions = { signal?: AbortSignal };

export async function loadDirectory(
  humanId: string,
  options: SignalOptions = {},
): Promise<DirectorySnapshot> {
  const [organizationResponse, humanResponse, agentResponse, spaceResponse] =
    await Promise.all([
      organizationClient.getOrganization({}, options),
      organizationClient.listHumans({}, options),
      agentClient.listAgents({}, options),
      collaborationClient.listSpaces({}, options),
    ]);
  const organization = required(
    organizationResponse.organization,
    "Organization response was empty",
  );
  const createSpace = await checkPermission(
    humanId,
    Capability.SPACE_CREATE,
    ScopeKind.ORGANIZATION,
    organization.id,
    options.signal,
  );
  return {
    organization,
    humans: humanResponse.humans,
    agents: agentResponse.agents,
    spaces: spaceResponse.spaces,
    createSpace,
  };
}

export async function loadConversation(
  humanId: string,
  spaceId: string,
  options: SignalOptions = {},
): Promise<ConversationSnapshot> {
  const [spaceResponse, memberResponse, messagePage, permissions] =
    await Promise.all([
      collaborationClient.getSpace({ spaceId }, options),
      collaborationClient.listMembers({ spaceId }, options),
      loadMessages({ case: "spaceId", value: spaceId }, 0n, options),
      loadSpacePermissions(humanId, spaceId, options.signal),
    ]);
  return {
    space: required(spaceResponse.space, "Space response was empty"),
    memberships: memberResponse.memberships,
    permissions,
    ...messagePage,
  };
}

export async function loadMoreConversationMessages(
  snapshot: ConversationSnapshot,
  options: SignalOptions = {},
): Promise<ConversationSnapshot> {
  const page = await loadMessages(
    { case: "spaceId", value: snapshot.space.id },
    snapshot.nextAfterSequence,
    options,
  );
  return {
    ...snapshot,
    messages: mergeMessages(snapshot.messages, page.messages),
    hasMore: page.hasMore,
    nextAfterSequence: page.nextAfterSequence,
  };
}

export async function loadThread(
  root: Message,
  options: SignalOptions = {},
): Promise<ThreadSnapshot> {
  let exists = true;
  try {
    await collaborationClient.getThread(
      { threadRootMessageId: root.id },
      options,
    );
  } catch (error) {
    if (ConnectError.from(error).code !== Code.NotFound) throw error;
    exists = false;
  }
  if (!exists) {
    return {
      root,
      replies: [],
      hasMore: false,
      nextAfterSequence: 0n,
    };
  }
  const page = await loadMessages(
    { case: "threadRootMessageId", value: root.id },
    0n,
    options,
  );
  return { root, replies: page.messages, ...withoutMessages(page) };
}

export async function loadMoreThreadMessages(
  snapshot: ThreadSnapshot,
  options: SignalOptions = {},
): Promise<ThreadSnapshot> {
  const page = await loadMessages(
    { case: "threadRootMessageId", value: snapshot.root.id },
    snapshot.nextAfterSequence,
    options,
  );
  return {
    ...snapshot,
    replies: mergeMessages(snapshot.replies, page.messages),
    hasMore: page.hasMore,
    nextAfterSequence: page.nextAfterSequence,
  };
}

export function activeHumans(humans: readonly Human[]): Human[] {
  return humans.filter((human) => human.status === HumanStatus.ACTIVE);
}

async function loadMessages(
  target: { case: "spaceId" | "threadRootMessageId"; value: string },
  afterSequence: bigint,
  options: SignalOptions,
) {
  return loadMessagePages(async (cursor, limit) => {
    const response = await collaborationClient.listMessages(
      { target: { target }, afterSequence: cursor, limit },
      options,
    );
    return response.messages;
  }, afterSequence);
}

function withoutMessages(page: MessagePage) {
  return {
    hasMore: page.hasMore,
    nextAfterSequence: page.nextAfterSequence,
  };
}
