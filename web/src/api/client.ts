import { v7 as uuidv7 } from "uuid";
import createClient, { type Client } from "openapi-fetch";

import type {
  User,
  RegisterInput,
  RegisterResponse,
  LoginInput,
  LoginResponse,
  Space,
  CreateSpaceInput,
  Computer,
  PairingDetails,
  ConfirmPairingInput,
  Agent,
  CreateAgentInput,
  AgentMemoryContent,
  UpdateAgentInput,
  Member,
  UpdateMemberInput,
  Invitation,
  CreatedInvitation,
  CreateInvitationInput,
  Channel,
  ChannelList,
  ChannelMembers,
  DirectMessage,
  CreateChannelInput,
  Message,
  MessagePage,
  CreateMessageInput,
  Attachment,
  InboxItem,
  Task,
  CreateTaskInput,
  UpdateTaskInput,
  LinkTaskThreadInput,
  CompleteTaskInput,
  CloseTaskInput,
  AgentRuntime,
  ThreadRead,
  ThreadSubscription,
  CreateThreadReplyInput,
  ErrorEnvelope,
} from "./types";
export type { User, RegisterInput, LoginInput, Space, CreateSpaceInput, Computer, PairingDetails, Agent, AgentRuntime, AttentionConfig, AgentMemoryFile, AgentMemoryContent, UpdateAgentInput, Member, UpdateMemberInput, Invitation, CreatedInvitation, CreateInvitationInput, Channel, ChannelList, ChannelMembers, DirectMessage, CreateChannelInput, MessageAuthor, Message, MessagePage, MessageTaskRef, MessageTaskSummary, CreateMessageInput, Attachment, InboxItem, Task, TaskStatus, Run, RunStatus, SessionContinuity, ThreadReference, CreateTaskInput, UpdateTaskInput, LinkTaskThreadInput, CompleteTaskInput, CloseTaskInput, ThreadRead, ThreadSubscription, CreateThreadReplyInput } from "./types";

export class ApiRequestError extends Error {
  readonly code: string;
  readonly status: number;

  constructor(status: number, code: string, message: string) {
    super(message);
    this.name = "ApiRequestError";
    this.status = status;
    this.code = code;
  }
}

interface DynamicOperation {
  requestBody?: { content: { "application/json": unknown } };
  responses: {
    200: { content: { "application/json": unknown } };
    default: { content: { "application/json": ErrorEnvelope } };
  };
}

type DynamicPaths = Record<string, Record<"get" | "post" | "put" | "patch" | "delete", DynamicOperation>>;

// `openapi-fetch` builds a `Request`, which rejects a relative URL, so the client needs an absolute
// base. The wrapper then calls the global `fetch` in `(url, init)` form, resolving it per request:
// reading the global at call time honours a replaced implementation, and passing the path keeps each
// request identifiable instead of opaque behind a Request object.
const browserApi: Client<DynamicPaths> = createClient({
  baseUrl: globalThis.location?.origin ?? "http://localhost",
  credentials: "same-origin",
  fetch: async (request) => {
    const hasBody = request.method !== "GET" && request.method !== "HEAD";
    const body = hasBody ? await request.text() : undefined;
    const { pathname, search } = new URL(request.url);
    return globalThis.fetch(`${pathname}${search}`, {
      // A read passes no `method`, leaving `fetch` to default to GET, so a caller inspecting the
      // init can tell reads from writes.
      method: request.method === "GET" ? undefined : request.method,
      headers: Object.fromEntries(request.headers),
      body: body === "" ? undefined : body,
      credentials: "same-origin",
    });
  },
});

export async function register(input: RegisterInput): Promise<User> {
  const response = await mutate<RegisterResponse>("/api/v1/auth/register", "POST", input);
  return response.user;
}

export function currentUser(): Promise<User> {
  return apiRequest<User>("/api/v1/auth/me");
}

export async function login(input: LoginInput): Promise<User> {
  const response = await mutate<LoginResponse>("/api/v1/auth/login", "POST", input);
  return response.user;
}

export async function logout(): Promise<void> {
  const response = await fetch("/api/v1/auth/logout", {
    method: "POST",
    credentials: "same-origin",
    headers: { "Idempotency-Key": uuidv7() },
  });
  if (!response.ok && response.status !== 401) {
    throw new ApiRequestError(response.status, "logout_failed", "Could not end the Session");
  }
}

export function createSpace(input: CreateSpaceInput): Promise<Space> {
  return mutate<Space>("/api/v1/spaces", "POST", input);
}

export function listSpaces(): Promise<Space[]> {
  return apiRequest<Space[]>("/api/v1/spaces");
}

export function getPairingDetails(pairingId: string, code: string): Promise<PairingDetails> {
  return apiRequest<PairingDetails>(
    `/api/v1/computer-pairings/${encodeURIComponent(pairingId)}?code=${encodeURIComponent(code)}`,
  );
}

export function confirmPairing(
  pairingId: string,
  input: ConfirmPairingInput,
): Promise<Computer> {
  return mutate<Computer>(
    `/api/v1/computer-pairings/${encodeURIComponent(pairingId)}/confirm`,
    "POST",
    input,
  );
}

export function listComputers(spaceId: string): Promise<Computer[]> {
  return apiRequest<Computer[]>(`/api/v1/spaces/${encodeURIComponent(spaceId)}/computers`);
}

export function deleteComputer(computerId: string): Promise<Computer> {
  return mutate<Computer>(`/api/v1/computers/${encodeURIComponent(computerId)}`, "DELETE");
}

export function createAgent(
  spaceId: string,
  input: CreateAgentInput,
): Promise<Agent> {
  return mutate<Agent>(`/api/v1/spaces/${encodeURIComponent(spaceId)}/agents`, "POST", input);
}

export function listAgents(spaceId: string): Promise<Agent[]> {
  return apiRequest<Agent[]>(`/api/v1/spaces/${encodeURIComponent(spaceId)}/agents`);
}

export function getAgent(agentId: string): Promise<Agent> {
  return apiRequest<Agent>(`/api/v1/agents/${encodeURIComponent(agentId)}`);
}

export function updateAgent(agentId: string, input: UpdateAgentInput): Promise<Agent> {
  return mutate<Agent>(`/api/v1/agents/${encodeURIComponent(agentId)}`, "PATCH", input);
}

export function retireAgent(agentId: string): Promise<Agent> {
  return mutate<Agent>(`/api/v1/agents/${encodeURIComponent(agentId)}`, "DELETE");
}

export function readAgentMemory(agentId: string, path: string): Promise<AgentMemoryContent> {
  return apiRequest<AgentMemoryContent>(
    `/api/v1/agents/${encodeURIComponent(agentId)}/memory/read`,
    {
      method: "POST",
      body: JSON.stringify({ path }),
      headers: { "Content-Type": "application/json" },
    },
  );
}

export function getSpaceBySlug(slug: string): Promise<Space> {
  return apiRequest<Space>(`/api/v1/spaces/by-slug/${encodeURIComponent(slug)}`);
}

export function listMembers(spaceId: string): Promise<Member[]> {
  return apiRequest<Member[]>(`/api/v1/spaces/${encodeURIComponent(spaceId)}/members`);
}

export function updateMember(
  spaceId: string,
  memberId: string,
  input: UpdateMemberInput,
): Promise<Member> {
  return mutate<Member>(
    `/api/v1/spaces/${encodeURIComponent(spaceId)}/members/${encodeURIComponent(memberId)}`,
    "PATCH",
    input,
  );
}

export function createInvitation(
  spaceId: string,
  input: CreateInvitationInput,
): Promise<CreatedInvitation> {
  return mutate<CreatedInvitation>(
    `/api/v1/spaces/${encodeURIComponent(spaceId)}/invites`,
    "POST",
    input,
  );
}

export function getInvitation(token: string): Promise<Invitation> {
  return apiRequest<Invitation>(`/api/v1/invites/${encodeURIComponent(token)}`);
}

export function acceptInvitation(token: string): Promise<Member> {
  return mutate<Member>(`/api/v1/invites/${encodeURIComponent(token)}/accept`, "POST");
}

export function listChannels(spaceId: string): Promise<ChannelList> {
  return apiRequest<ChannelList>(`/api/v1/spaces/${encodeURIComponent(spaceId)}/channels`);
}

export function createChannel(spaceId: string, input: CreateChannelInput): Promise<Channel> {
  return mutate<Channel>(
    `/api/v1/spaces/${encodeURIComponent(spaceId)}/channels`,
    "POST",
    input,
  );
}

export function joinChannel(channelId: string): Promise<Channel> {
  return mutate<Channel>(
    `/api/v1/channels/${encodeURIComponent(channelId)}/members/me`,
    "POST",
  );
}

export function listChannelMembers(channelId: string): Promise<ChannelMembers> {
  return apiRequest<ChannelMembers>(
    `/api/v1/channels/${encodeURIComponent(channelId)}/members`,
  );
}

export function addChannelAgents(
  channelId: string,
  agentMemberIds: string[],
): Promise<ChannelMembers> {
  return mutate<ChannelMembers>(
    `/api/v1/channels/${encodeURIComponent(channelId)}/members`,
    "POST",
    { agent_member_ids: agentMemberIds },
  );
}

export function listDirectMessages(spaceId: string): Promise<DirectMessage[]> {
  return apiRequest<DirectMessage[]>(`/api/v1/spaces/${encodeURIComponent(spaceId)}/dms`);
}

export function createDirectMessage(spaceId: string, memberId: string): Promise<DirectMessage> {
  return mutate<DirectMessage>(`/api/v1/spaces/${encodeURIComponent(spaceId)}/dms`, "POST", {
    member_id: memberId,
  });
}

export function listMessages(channelId: string): Promise<MessagePage> {
  return apiRequest<MessagePage>(`/api/v1/channels/${encodeURIComponent(channelId)}/messages`);
}

export function createMessage(channelId: string, input: CreateMessageInput): Promise<Message> {
  return mutate<Message>(`/api/v1/channels/${encodeURIComponent(channelId)}/messages`, "POST", input);
}

export async function uploadAttachment(spaceId: string, file: File): Promise<Attachment> {
  const attachment = await mutate<Attachment>("/api/v1/attachments/uploads", "POST", {
      space_id: spaceId,
      original_name: file.name,
      media_type: file.type || "application/octet-stream",
  });
  if (!attachment.upload_path) {
    throw new ApiRequestError(500, "upload_path_missing", "Attachment upload path is missing");
  }
  const digest = await crypto.subtle.digest("SHA-256", await file.arrayBuffer());
  const uploaded = await fetch(attachment.upload_path, {
    method: "PUT",
    body: file,
    credentials: "same-origin",
    headers: { "Idempotency-Key": uuidv7() },
  });
  if (!uploaded.ok) await throwResponseError(uploaded);
  return mutate<Attachment>(
    `/api/v1/attachments/${encodeURIComponent(attachment.id)}/complete`,
    "POST",
    { size: file.size, sha256: hexDigest(digest) },
  );
}

export function readThread(threadId: string): Promise<ThreadRead> {
  return apiRequest<ThreadRead>(`/api/v1/threads/${encodeURIComponent(threadId)}`);
}

export function setThreadSubscription(
  threadId: string,
  isFollowing: boolean,
): Promise<ThreadSubscription> {
  return mutate<ThreadSubscription>(
    `/api/v1/threads/${encodeURIComponent(threadId)}/subscription`,
    isFollowing ? "PUT" : "DELETE",
  );
}

export function createThreadReply(
  threadId: string,
  input: CreateThreadReplyInput,
): Promise<Message> {
  return mutate<Message>(
    `/api/v1/threads/${encodeURIComponent(threadId)}/messages`,
    "POST",
    input,
  );
}

export function listInbox(memberId: string): Promise<InboxItem[]> {
  return apiRequest<InboxItem[]>(`/api/v1/members/${encodeURIComponent(memberId)}/inbox`);
}

export function markInboxItemRead(itemId: string): Promise<InboxItem> {
  return mutate<InboxItem>(`/api/v1/inbox-items/${encodeURIComponent(itemId)}/read`, "POST");
}

export function listTasks(spaceId: string): Promise<Task[]> {
  return apiRequest<Task[]>(`/api/v1/spaces/${encodeURIComponent(spaceId)}/tasks`);
}

export function getTask(taskId: string): Promise<Task> {
  return apiRequest<Task>(`/api/v1/tasks/${encodeURIComponent(taskId)}`);
}

export function createTaskFromRootMessage(messageId: string, input: CreateTaskInput): Promise<Task> {
  return mutate<Task>(`/api/v1/root-messages/${encodeURIComponent(messageId)}/task`, "POST", input);
}

export function updateTask(taskId: string, input: UpdateTaskInput): Promise<Task> {
  return mutate<Task>(`/api/v1/tasks/${encodeURIComponent(taskId)}`, "PATCH", input);
}

export function linkTaskThread(taskId: string, input: LinkTaskThreadInput): Promise<Task> {
  return mutate<Task>(`/api/v1/tasks/${encodeURIComponent(taskId)}/threads`, "POST", input);
}

export function unlinkTaskThread(taskId: string, threadId: string): Promise<Task> {
  return mutate<Task>(`/api/v1/tasks/${encodeURIComponent(taskId)}/threads/${encodeURIComponent(threadId)}`, "DELETE");
}

export function startTask(taskId: string, assigneeAgentMemberId: string): Promise<Task> {
  return mutate<Task>(`/api/v1/tasks/${encodeURIComponent(taskId)}/start`, "POST", {
    assignee_agent_member_id: assigneeAgentMemberId,
  });
}

export function submitTaskReview(taskId: string): Promise<Task> {
  return mutate<Task>(`/api/v1/tasks/${encodeURIComponent(taskId)}/submit-review`, "POST");
}

export function requestTaskChanges(taskId: string): Promise<Task> {
  return mutate<Task>(`/api/v1/tasks/${encodeURIComponent(taskId)}/request-changes`, "POST");
}

export function completeTask(taskId: string, input: CompleteTaskInput): Promise<Task> {
  return mutate<Task>(`/api/v1/tasks/${encodeURIComponent(taskId)}/done`, "POST", input);
}

export function closeTask(taskId: string, input: CloseTaskInput): Promise<Task> {
  return mutate<Task>(`/api/v1/tasks/${encodeURIComponent(taskId)}/close`, "POST", input);
}

export function resetTaskSession(taskId: string): Promise<Task> {
  return mutate<Task>(`/api/v1/tasks/${encodeURIComponent(taskId)}/reset-session`, "POST");
}

export function getAgentRuntime(agentId: string): Promise<AgentRuntime> {
  return apiRequest<AgentRuntime>(`/api/v1/agents/${encodeURIComponent(agentId)}/runs/current`);
}

export function grantMemberPermission(memberId: string, actionCode: string): Promise<Member> {
  return mutate<Member>(`/api/v1/members/${encodeURIComponent(memberId)}/permissions/${encodeURIComponent(actionCode)}`, "PUT");
}

export function revokeMemberPermission(memberId: string, actionCode: string): Promise<Member> {
  return mutate<Member>(`/api/v1/members/${encodeURIComponent(memberId)}/permissions/${encodeURIComponent(actionCode)}`, "DELETE");
}

async function apiRequest<T>(path: string, init?: RequestInit): Promise<T> {
  const method = (init?.method ?? "GET").toLowerCase();
  const body = typeof init?.body === "string" ? JSON.parse(init.body) : init?.body;
  const options = { body, headers: init?.headers } as never;
  const result = await (method === "get"
    ? browserApi.GET(path, options)
    : method === "post"
      ? browserApi.POST(path, options)
      : method === "put"
        ? browserApi.PUT(path, options)
        : method === "patch"
          ? browserApi.PATCH(path, options)
          : method === "delete"
            ? browserApi.DELETE(path, options)
            : Promise.reject(new Error(`Unsupported HTTP method: ${method}`)));
  if (result.error !== undefined) {
    await throwResponseError(result.response);
  }
  return result.data as T;
}

function mutate<T>(path: string, method: string, body?: unknown): Promise<T> {
  return apiRequest<T>(path, {
    method,
    body: body === undefined ? undefined : JSON.stringify(body),
    headers: body === undefined ? { "Idempotency-Key": uuidv7() } : mutationHeaders(),
  });
}

async function throwResponseError(response: Response): Promise<never> {
  const payload = (await response.json().catch(() => ({}))) as ErrorEnvelope;
  throw new ApiRequestError(
    response.status,
    payload.error?.code ?? "request_failed",
    payload.error?.message ?? "The request could not be completed",
  );
}

function hexDigest(buffer: ArrayBuffer): string {
  return Array.from(new Uint8Array(buffer), (byte) => byte.toString(16).padStart(2, "0")).join("");
}

function mutationHeaders(): HeadersInit {
  return {
    "Content-Type": "application/json",
    "Idempotency-Key": uuidv7(),
  };
}
