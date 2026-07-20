import { Code, ConnectError, createClient } from "@connectrpc/connect";
import { createConnectTransport } from "@connectrpc/connect-web";
import { AgentService, type Agent } from "../gen/sumi/agent/v1/agent_pb";
import {
  Capability,
  GrantService,
  PrincipalKind as GrantPrincipalKind,
  ScopeKind,
} from "../gen/sumi/grant/v1/grant_pb";
import {
  HumanStatus,
  OrganizationService,
  type Human,
  type Organization,
} from "../gen/sumi/organization/v1/organization_pb";
import {
  CollaborationService,
  PrincipalKind,
  type Membership,
  type Message,
  type Principal,
  type Space,
} from "../gen/sumi/space/v1/space_pb";

const transport = createConnectTransport({ baseUrl: window.location.origin });
const organizations = createClient(OrganizationService, transport);
const agents = createClient(AgentService, transport);
const grants = createClient(GrantService, transport);
const collaboration = createClient(CollaborationService, transport);

export const MESSAGE_PAGE_LIMIT = 200;
export const MESSAGE_PAGE_BATCH = 5;

export type PermissionState =
  | { status: "allowed" }
  | { status: "denied" }
  | { status: "unknown"; error: string };

export type DirectorySnapshot = {
  organization: Organization;
  humans: Human[];
  agents: Agent[];
  spaces: Space[];
  createSpace: PermissionState;
};

export type SpacePermissions = {
  members: PermissionState;
  archive: PermissionState;
  send: PermissionState;
};

export type ConversationSnapshot = {
  space: Space;
  memberships: Membership[];
  messages: Message[];
  permissions: SpacePermissions;
  hasMore: boolean;
  nextAfterSequence: bigint;
};

export type ThreadSnapshot = {
  root: Message;
  replies: Message[];
  hasMore: boolean;
  nextAfterSequence: bigint;
};

export type MessagePage = {
  messages: Message[];
  hasMore: boolean;
  nextAfterSequence: bigint;
};

type SignalOptions = { signal?: AbortSignal };

export async function loadDirectory(
  humanId: string,
  options: SignalOptions = {},
): Promise<DirectorySnapshot> {
  const [organizationResponse, humanResponse, agentResponse, spaceResponse] =
    await Promise.all([
      organizations.getOrganization({}, options),
      organizations.listHumans({}, options),
      agents.listAgents({}, options),
      collaboration.listSpaces({}, options),
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
      collaboration.getSpace({ spaceId }, options),
      collaboration.listMembers({ spaceId }, options),
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
    await collaboration.getThread({ threadRootMessageId: root.id }, options);
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

export async function loadMessagePages(
  fetchPage: (afterSequence: bigint, limit: number) => Promise<Message[]>,
  afterSequence = 0n,
  maxPages = MESSAGE_PAGE_BATCH,
): Promise<MessagePage> {
  let cursor = afterSequence;
  let collected: Message[] = [];
  for (let pageIndex = 0; pageIndex < maxPages; pageIndex += 1) {
    const page = await fetchPage(cursor, MESSAGE_PAGE_LIMIT);
    collected = mergeMessages(collected, page);
    if (page.length < MESSAGE_PAGE_LIMIT) {
      return {
        messages: collected,
        hasMore: false,
        nextAfterSequence: maxSequence(collected, cursor),
      };
    }
    const next = maxSequence(page, cursor);
    if (next <= cursor) {
      throw new Error("Message pagination did not advance");
    }
    cursor = next;
  }
  return { messages: collected, hasMore: true, nextAfterSequence: cursor };
}

export function mergeMessages(
  current: readonly Message[],
  incoming: readonly Message[],
): Message[] {
  const byId = new Map<string, Message>();
  const bySequence = new Map<bigint, string>();
  for (const message of [...current, ...incoming]) {
    const previous = byId.get(message.id);
    if (previous && previous.targetSequence !== message.targetSequence) {
      throw new Error(`Message ${message.id} changed sequence`);
    }
    const sequenceOwner = bySequence.get(message.targetSequence);
    if (sequenceOwner && sequenceOwner !== message.id) {
      throw new Error(
        `Messages ${sequenceOwner} and ${message.id} share sequence ${message.targetSequence}`,
      );
    }
    byId.set(message.id, message);
    bySequence.set(message.targetSequence, message.id);
  }
  return [...byId.values()].sort((left, right) =>
    left.targetSequence < right.targetSequence
      ? -1
      : left.targetSequence > right.targetSequence
        ? 1
        : left.id.localeCompare(right.id),
  );
}

export function activeHumans(humans: readonly Human[]): Human[] {
  return humans.filter((human) => human.status === HumanStatus.ACTIVE);
}

export function principalForHuman(id: string): Principal {
  return { kind: PrincipalKind.HUMAN, id } as Principal;
}

export function principalForAgent(id: string): Principal {
  return { kind: PrincipalKind.AGENT, id } as Principal;
}

export async function createDM(input: {
  requestId: string;
  peer: Principal;
}): Promise<Space> {
  const response = await collaboration.createDM(input);
  return required(response.space, "Create DM response was empty");
}

export async function createGroup(input: {
  requestId: string;
  name: string;
}): Promise<Space> {
  const response = await collaboration.createGroup(input);
  return required(response.space, "Create Group response was empty");
}

export async function addSpaceMember(input: {
  requestId: string;
  spaceId: string;
  member: Principal;
}): Promise<void> {
  await collaboration.addMember(input);
}

export async function removeSpaceMember(input: {
  requestId: string;
  spaceId: string;
  member: Principal;
}): Promise<void> {
  await collaboration.removeMember(input);
}

export async function setSpaceArchived(input: {
  requestId: string;
  spaceId: string;
  archived: boolean;
}): Promise<void> {
  if (input.archived) {
    await collaboration.archiveSpace(input);
  } else {
    await collaboration.unarchiveSpace(input);
  }
}

export async function sendSpaceMessage(input: {
  requestId: string;
  spaceId: string;
  body: string;
}): Promise<Message> {
  const response = await collaboration.sendMessage({
    requestId: input.requestId,
    target: { target: { case: "spaceId", value: input.spaceId } },
    body: input.body,
  });
  return required(response.message, "Send Message response was empty");
}

export async function sendThreadMessage(input: {
  requestId: string;
  threadRootMessageId: string;
  body: string;
}): Promise<Message> {
  const response = await collaboration.sendMessage({
    requestId: input.requestId,
    target: {
      target: {
        case: "threadRootMessageId",
        value: input.threadRootMessageId,
      },
    },
    body: input.body,
  });
  return required(response.message, "Send Thread reply response was empty");
}

export class PayloadRequestLifecycle<T> {
  private fingerprint: string | undefined;
  private requestId: string;

  constructor(
    private readonly identify: (payload: T) => string = (payload) =>
      JSON.stringify(payload),
    private readonly nextId: () => string = () => crypto.randomUUID(),
  ) {
    this.requestId = nextId();
  }

  sync(payload: T): string {
    const nextFingerprint = this.identify(payload);
    if (
      this.fingerprint !== undefined &&
      this.fingerprint !== nextFingerprint
    ) {
      this.requestId = this.nextId();
    }
    this.fingerprint = nextFingerprint;
    return this.requestId;
  }

  complete(): void {
    this.fingerprint = undefined;
    this.requestId = this.nextId();
  }
}

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

async function loadMessages(
  target: { case: "spaceId" | "threadRootMessageId"; value: string },
  afterSequence: bigint,
  options: SignalOptions,
) {
  return loadMessagePages(async (cursor, limit) => {
    const response = await collaboration.listMessages(
      { target: { target }, afterSequence: cursor, limit },
      options,
    );
    return response.messages;
  }, afterSequence);
}

async function loadSpacePermissions(
  humanId: string,
  spaceId: string,
  signal?: AbortSignal,
): Promise<SpacePermissions> {
  const [members, archive, send] = await Promise.all([
    checkPermission(
      humanId,
      Capability.SPACE_MEMBERS_MANAGE,
      ScopeKind.SPACE,
      spaceId,
      signal,
    ),
    checkPermission(
      humanId,
      Capability.SPACE_ARCHIVE,
      ScopeKind.SPACE,
      spaceId,
      signal,
    ),
    checkPermission(
      humanId,
      Capability.MESSAGE_SEND,
      ScopeKind.SPACE,
      spaceId,
      signal,
    ),
  ]);
  return { members, archive, send };
}

async function checkPermission(
  humanId: string,
  capability: Capability,
  scopeKind: ScopeKind,
  scopeId: string,
  signal?: AbortSignal,
): Promise<PermissionState> {
  try {
    const response = await grants.checkPermission(
      {
        subject: { kind: GrantPrincipalKind.HUMAN, id: humanId },
        capability,
        scope: { kind: scopeKind, id: scopeId },
      },
      { signal },
    );
    return { status: response.allowed ? "allowed" : "denied" };
  } catch (error) {
    if (signal?.aborted) throw error;
    return {
      status: "unknown",
      error: collaborationErrorMessage(error, "check permission"),
    };
  }
}

function maxSequence(messages: readonly Message[], fallback: bigint) {
  return messages.reduce(
    (maximum, message) =>
      message.targetSequence > maximum ? message.targetSequence : maximum,
    fallback,
  );
}

function withoutMessages(page: MessagePage) {
  return {
    hasMore: page.hasMore,
    nextAfterSequence: page.nextAfterSequence,
  };
}

function required<T>(value: T | undefined, message: string): T {
  if (!value) throw new Error(message);
  return value;
}
