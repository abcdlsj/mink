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
  token_fingerprint: string;
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
  driver_kind: "codex" | "builtin";
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

export interface ChannelMembers {
  members: Member[];
  can_manage: boolean;
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
  agent_member_ids: string[];
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
    driver_kind: "codex" | "builtin";
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
