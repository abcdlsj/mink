import { v7 as uuidv7 } from "uuid";

export interface User {
  id: string;
  display_name: string;
  email: string;
}

export interface RegisterInput {
  display_name: string;
  email: string;
  password: string;
}

export interface LoginInput {
  email: string;
  password: string;
}

export interface Space {
  id: string;
  name: string;
  slug: string;
  accent: string;
  owner_member_id: string;
  current_member_id: string;
  general_channel_id: string;
}

export interface CreateSpaceInput {
  name: string;
  slug: string;
  accent: string;
}

export interface Computer {
  id: string;
  space_id: string;
  name: string;
  hostname: string;
  os: "macos" | "linux";
  status: "online" | "offline" | "revoked";
  daemon_version: string;
  last_seen_at?: string;
  created_at: string;
}

export interface PairingDetails {
  pairing_id: string;
  hostname: string;
  os: "macos" | "linux";
  daemon_version: string;
  public_key_fingerprint: string;
  expires_at: string;
  status: "pending" | "confirmed" | "expired";
}

export interface Agent {
  member_id: string;
  space_id: string;
  computer_id: string;
  name: string;
  handle: string;
  access_level: "member" | "admin";
  role_text: string;
  role_revision: number;
  status: "provisioning" | "active" | "suspended" | "error" | "retired";
  driver_kind: "codex";
  attention_config: AttentionConfig;
  created_at: string;
  updated_at: string;
  retired_at?: string;
  last_error_code?: string;
  memory_files: AgentMemoryFile[];
}

export interface AttentionConfig {
  dm_immediate: true;
  mention_immediate: true;
  ambient_enabled: boolean;
  ambient_debounce_seconds: number;
  ambient_max_wait_seconds: number;
  max_retry_count: number;
}

export interface AgentMemoryFile {
  path: string;
  size: number;
  sha256: string;
  updated_at: string;
}

export interface AgentMemoryContent extends AgentMemoryFile {
  content: string;
}

export interface UpdateAgentInput {
  role_text?: string;
  attention_config?: AttentionConfig;
  lifecycle?:
    | { action: "suspend"; mode: "stop_after_current" | "cancel_now" }
    | { action: "resume" }
    | { action: "retry" }
    | { action: "retire" };
}

export interface Member {
  id: string;
  kind: "human" | "agent";
  display_name: string;
  handle: string;
  access_level: "owner" | "admin" | "member";
  permissions: string[];
}

export interface UpdateMemberInput {
  access_level?: string;
  permissions?: string[];
}

export interface Invitation {
  id: string;
  space_id: string;
  space_name: string;
  space_slug: string;
  email: string;
  expires_at: string;
}

export interface CreateInvitationInput {
  email: string;
  invite_token: string;
}

export interface Channel {
  id: string;
  space_id: string;
  kind: "public" | "private";
  name: string;
  slug: string;
  topic?: string;
  created_by_member_id: string;
  joined: boolean;
  archived_at?: string;
}

export interface ChannelList {
  channels: Channel[];
  can_create: boolean;
}

export interface DirectMessage {
  channel_id: string;
  space_id: string;
  other_member: Member;
  created_at: string;
}

export interface CreateChannelInput {
  name: string;
  slug: string;
  kind: "public" | "private";
  topic?: string;
}

export interface MessageAuthor {
  id: string;
  kind: "human" | "agent";
  display_name: string;
  handle: string;
}

export interface Message {
  id: string;
  channel_id: string;
  seq: number;
  author: MessageAuthor;
  body_markdown: string;
  mentions: string[];
  attachments: Attachment[];
  created_at: string;
  edited_at?: string;
  deleted_at?: string;
  thread_id?: number;
  reply_count: number;
}

export interface MessagePage {
  channel_id: string;
  snapshot_channel_seq: number;
  messages: Message[];
  has_more_before: boolean;
  has_more_after: boolean;
}

export interface CreateMessageInput {
  body_markdown: string;
  mentions: string[];
  attachment_ids?: string[];
}

export interface Attachment {
  id: string;
  space_id: string;
  uploader_member_id: string;
  original_name: string;
  media_type: string;
  size?: number;
  sha256?: string;
  status: "uploading" | "ready" | "deleted";
  upload_path?: string;
  download_path?: string;
  created_at: string;
}

export interface InboxItem {
  id: string;
  member_id: string;
  kind: "direct" | "mention" | "reply" | "thread_activity" | "channel_activity" | "approval" | "system";
  priority: "hard" | "ambient";
  channel_id?: string;
  channel_slug?: string;
  thread_id?: number;
  message_id?: string;
  approval_id?: string;
  sender_member_id?: string;
  sender_display_name?: string;
  summary?: string;
  status: "pending" | "deferred" | "handled";
  available_at: string;
  created_at: string;
}

export interface Approval {
  id: string;
  space_id: string;
  type: "agent.create";
  requested_by_member_id: string;
  requester_name: string;
  payload: {
    computer_id: string;
    name: string;
    role_text: string;
    driver_kind: "codex";
    access_level: "member";
    permissions: string[];
  };
  status: "pending" | "approved" | "rejected" | "canceled";
  resolved_by_member_id?: string;
  created_at: string;
  resolved_at?: string;
}

export interface Thread {
  channel_id: string;
  thread_id: number;
  root_message_id: string;
  created_by_member_id: string;
  created_at: string;
}

export interface ThreadRead {
  channel_id: string;
  thread_id: number;
  snapshot_channel_seq: number;
  root: Message;
  replies: Message[];
  is_following: boolean;
}

export interface ThreadSubscription {
  channel_id: string;
  thread_id: number;
  is_following: boolean;
}

export interface CreateThreadReplyInput extends CreateMessageInput {
  reply_to_message_id?: string;
}

interface ErrorEnvelope {
  error?: {
    code?: string;
    message?: string;
  };
}

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

export async function register(input: RegisterInput): Promise<User> {
  const response = await apiRequest<{ user: User }>("/api/v1/auth/register", {
    method: "POST",
    body: JSON.stringify(input),
    headers: mutationHeaders(),
  });
  return response.user;
}

export function currentUser(): Promise<User> {
  return apiRequest<User>("/api/v1/auth/me");
}

export async function login(input: LoginInput): Promise<User> {
  const response = await apiRequest<{ user: User }>("/api/v1/auth/login", {
    method: "POST",
    body: JSON.stringify(input),
    headers: mutationHeaders(),
  });
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
  return apiRequest<Space>("/api/v1/spaces", {
    method: "POST",
    body: JSON.stringify(input),
    headers: mutationHeaders(),
  });
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
  input: { space_id: string; name: string; code: string },
): Promise<Computer> {
  return apiRequest<Computer>(
    `/api/v1/computer-pairings/${encodeURIComponent(pairingId)}/confirm`,
    { method: "POST", body: JSON.stringify(input), headers: mutationHeaders() },
  );
}

export function listComputers(spaceId: string): Promise<Computer[]> {
  return apiRequest<Computer[]>(`/api/v1/spaces/${encodeURIComponent(spaceId)}/computers`);
}

export function revokeComputer(computerId: string): Promise<Computer> {
  return apiRequest<Computer>(`/api/v1/computers/${encodeURIComponent(computerId)}`, {
    method: "DELETE",
    headers: { "Idempotency-Key": uuidv7() },
  });
}

export function createAgent(
  spaceId: string,
  input: {
    computer_id: string;
    name: string;
    handle?: string;
    role_text: string;
    access_level: "member" | "admin";
    driver_kind: "codex";
  },
): Promise<Agent> {
  return apiRequest<Agent>(`/api/v1/spaces/${encodeURIComponent(spaceId)}/agents`, {
    method: "POST",
    body: JSON.stringify(input),
    headers: mutationHeaders(),
  });
}

export function listAgents(spaceId: string): Promise<Agent[]> {
  return apiRequest<Agent[]>(`/api/v1/spaces/${encodeURIComponent(spaceId)}/agents`);
}

export function getAgent(agentId: string): Promise<Agent> {
  return apiRequest<Agent>(`/api/v1/agents/${encodeURIComponent(agentId)}`);
}

export function updateAgent(agentId: string, input: UpdateAgentInput): Promise<Agent> {
  return apiRequest<Agent>(`/api/v1/agents/${encodeURIComponent(agentId)}`, {
    method: "PATCH",
    body: JSON.stringify(input),
    headers: mutationHeaders(),
  });
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
  return apiRequest<Member>(
    `/api/v1/spaces/${encodeURIComponent(spaceId)}/members/${encodeURIComponent(memberId)}`,
    {
      method: "PATCH",
      body: JSON.stringify(input),
      headers: mutationHeaders(),
    },
  );
}

export function createInvitation(
  spaceId: string,
  input: CreateInvitationInput,
): Promise<Invitation> {
  return apiRequest<Invitation>(`/api/v1/spaces/${encodeURIComponent(spaceId)}/invites`, {
    method: "POST",
    body: JSON.stringify(input),
    headers: mutationHeaders(),
  });
}

export function getInvitation(token: string): Promise<Invitation> {
  return apiRequest<Invitation>(`/api/v1/invites/${encodeURIComponent(token)}`);
}

export function acceptInvitation(token: string): Promise<Member> {
  return apiRequest<Member>(`/api/v1/invites/${encodeURIComponent(token)}/accept`, {
    method: "POST",
    headers: { "Idempotency-Key": uuidv7() },
  });
}

export function listChannels(spaceId: string): Promise<ChannelList> {
  return apiRequest<ChannelList>(`/api/v1/spaces/${encodeURIComponent(spaceId)}/channels`);
}

export function createChannel(spaceId: string, input: CreateChannelInput): Promise<Channel> {
  return apiRequest<Channel>(`/api/v1/spaces/${encodeURIComponent(spaceId)}/channels`, {
    method: "POST",
    body: JSON.stringify(input),
    headers: mutationHeaders(),
  });
}

export function joinChannel(channelId: string): Promise<Channel> {
  return apiRequest<Channel>(`/api/v1/channels/${encodeURIComponent(channelId)}/members/me`, {
    method: "POST",
    headers: { "Idempotency-Key": uuidv7() },
  });
}

export function archiveChannel(channelId: string): Promise<Channel> {
  return apiRequest<Channel>(`/api/v1/channels/${encodeURIComponent(channelId)}/archive`, {
    method: "POST",
    headers: { "Idempotency-Key": uuidv7() },
  });
}

export function listDirectMessages(spaceId: string): Promise<DirectMessage[]> {
  return apiRequest<DirectMessage[]>(`/api/v1/spaces/${encodeURIComponent(spaceId)}/dms`);
}

export function createDirectMessage(spaceId: string, memberId: string): Promise<DirectMessage> {
  return apiRequest<DirectMessage>(`/api/v1/spaces/${encodeURIComponent(spaceId)}/dms`, {
    method: "POST",
    body: JSON.stringify({ member_id: memberId }),
    headers: mutationHeaders(),
  });
}

export function listMessages(channelId: string): Promise<MessagePage> {
  return apiRequest<MessagePage>(`/api/v1/channels/${encodeURIComponent(channelId)}/messages`);
}

export function createMessage(channelId: string, input: CreateMessageInput): Promise<Message> {
  return apiRequest<Message>(`/api/v1/channels/${encodeURIComponent(channelId)}/messages`, {
    method: "POST",
    body: JSON.stringify(input),
    headers: mutationHeaders(),
  });
}

export async function uploadAttachment(spaceId: string, file: File): Promise<Attachment> {
  const attachment = await apiRequest<Attachment>("/api/v1/attachments/uploads", {
    method: "POST",
    body: JSON.stringify({
      space_id: spaceId,
      original_name: file.name,
      media_type: file.type || "application/octet-stream",
    }),
    headers: mutationHeaders(),
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
  return apiRequest<Attachment>(`/api/v1/attachments/${encodeURIComponent(attachment.id)}/complete`, {
    method: "POST",
    body: JSON.stringify({ size: file.size, sha256: hexDigest(digest) }),
    headers: mutationHeaders(),
  });
}

export function createThread(channelId: string, rootMessageId: string): Promise<Thread> {
  return apiRequest<Thread>(`/api/v1/channels/${encodeURIComponent(channelId)}/threads`, {
    method: "POST",
    body: JSON.stringify({ root_message_id: rootMessageId }),
    headers: mutationHeaders(),
  });
}

export function readThread(channelId: string, threadId: number): Promise<ThreadRead> {
  return apiRequest<ThreadRead>(
    `/api/v1/channels/${encodeURIComponent(channelId)}/threads/${threadId}`,
  );
}

export function setThreadSubscription(
  channelId: string,
  threadId: number,
  isFollowing: boolean,
): Promise<ThreadSubscription> {
  return apiRequest<ThreadSubscription>(
    `/api/v1/channels/${encodeURIComponent(channelId)}/threads/${threadId}/subscription`,
    {
      method: isFollowing ? "PUT" : "DELETE",
      headers: { "Idempotency-Key": uuidv7() },
    },
  );
}

export function createThreadReply(
  channelId: string,
  threadId: number,
  input: CreateThreadReplyInput,
): Promise<Message> {
  return apiRequest<Message>(
    `/api/v1/channels/${encodeURIComponent(channelId)}/threads/${threadId}/messages`,
    {
      method: "POST",
      body: JSON.stringify(input),
      headers: mutationHeaders(),
    },
  );
}

export function listInbox(memberId: string): Promise<InboxItem[]> {
  return apiRequest<InboxItem[]>(`/api/v1/members/${encodeURIComponent(memberId)}/inbox`);
}

export function listApprovals(spaceId: string): Promise<Approval[]> {
  return apiRequest<Approval[]>(`/api/v1/spaces/${encodeURIComponent(spaceId)}/approvals`);
}

export function resolveApproval(
  approvalId: string,
  decision: "approve" | "reject",
): Promise<Approval> {
  return apiRequest<Approval>(
    `/api/v1/approvals/${encodeURIComponent(approvalId)}/${decision}`,
    { method: "POST", headers: { "Idempotency-Key": uuidv7() } },
  );
}

export function ackInboxItem(itemId: string): Promise<InboxItem> {
  return apiRequest<InboxItem>(`/api/v1/inbox/${encodeURIComponent(itemId)}/ack`, {
    method: "POST",
    headers: { "Idempotency-Key": uuidv7() },
  });
}

export function deferInboxItem(itemId: string, until: string): Promise<InboxItem> {
  return apiRequest<InboxItem>(`/api/v1/inbox/${encodeURIComponent(itemId)}/defer`, {
    method: "POST",
    body: JSON.stringify({ until }),
    headers: mutationHeaders(),
  });
}

async function apiRequest<T>(path: string, init?: RequestInit): Promise<T> {
  const response = await fetch(path, {
    credentials: "same-origin",
    ...init,
  });
  if (!response.ok) {
    await throwResponseError(response);
  }
  return (await response.json()) as T;
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
