export type ViewMode = "channel" | "thread" | "agent" | "home";

export interface WorkspaceState {
  workspace: string;
  provider: string;
  model: string;
  runtime: string;
  ready: boolean;
  data_dir: string;
}

export interface ChannelItem {
  id: string;
  name: string;
  topic?: string;
  agents: string[];
  agent_modes?: Record<string, string>;
  updated_at: string;
  unread_count: number;
  has_running: boolean;
}

export interface ThreadItem {
  id: string;
  channel_id: string;
  title: string;
  updated_at: string;
  event_count: number;
  has_running: boolean;
}

export interface DirectChatItem {
  id: string;
  kind?: "direct_chat" | "agent_dm";
  persona_id?: string;
  persona_name?: string;
  title: string;
  agents: string[];
  updated_at: string;
  unread_count: number;
  has_running: boolean;
}

export interface AgentDMItem {
  id: string;
  persona_id: string;
  persona_name?: string;
  title: string;
  updated_at: string;
  message_count: number;
}

export interface RecentItem {
  id: string;
  kind: "channel" | "direct_chat" | "agent_dm";
  title: string;
  subtitle?: string;
  updated_at: string;
}

export interface AgentItem {
  id: string;
  display: string;
  role?: string;
  runtime?: string;
  model?: string;
  status: string;
}

export interface ToolCall {
  id?: string;
  name: string;
  args?: string;
}

export interface ToolResult {
  tool_call_id?: string;
  content?: string;
  error?: string;
}

export type EventKind = "tool_call" | "tool_result" | "reasoning" | "service_notice" | "mention" | "delegate";
export type EventStatus = "running" | "done" | "error" | "idle" | "pending";

export interface DelegateStep {
  tool: string;
  status: EventStatus;
  output?: string;
  err?: string;
  time: string;
}

export interface EventBlock {
  kind: EventKind;
  tool_name?: string;
  args?: string;
  output?: string;
  err?: string;
  status: EventStatus;
  duration_ms?: number;
  time: string;
  agent_id?: string;
  agent_display?: string;
  task?: string;
  task_id?: string;
  reply?: string;
  steps?: DelegateStep[];
}

export interface MessageView {
  id: string;
  role: "user" | "agent" | "assistant" | "system";
  author_id?: string;
  author_name?: string;
  content?: string;
  reasoning?: string;
  time: string;
  events?: EventBlock[];
  thread_id?: string;
  thread_summary?: string;
  thread_info?: ThreadSummary;
  is_thread_reply?: boolean;
  task_accessory?: TaskAccessoryInfo;
  auto_reply_reason?: string;
  usage?: TokenUsage;
}

export interface TokenUsage {
  input: number;
  output: number;
  total: number;
  cost_usd?: number;
  model?: string;
  source?: string;
}

export interface TaskAccessoryInfo {
  task_id: string;
  worker_id: string;
  worker_display?: string;
  status: string;
  short_outcome?: string;
  terminal: boolean;
}

export interface ThreadSummary {
  parent_id: string;
  parent_preview: string;
  reply_count: number;
  last_reply_time: string;
  last_reply_author?: string;
  has_running_worker?: boolean;
}

export interface ThreadDetail {
  space_id: string;
  parent_id: string;
  parent?: MessageView;
  replies: MessageView[];
  participants?: AgentItem[];
  channel_agents?: string[];
  agent_modes?: Record<string, string>;
  recent_runs?: AgentRun[];
  active_worker_id?: string;
  last_reply_time?: string;
  not_found?: boolean;
  unsupported?: boolean;
  unsupported_hint?: string;
}

export interface SessionItem {
  id: string;
  title: string;
  persona_id?: string;
  persona_name?: string;
  runtime?: string;
  model?: string;
  updated_at: string;
  message_count?: number;
  event_count?: number;
  running?: boolean;
}

export interface SessionDetail {
  item: SessionItem;
  summary?: string;
  messages: MessageView[];
}

export interface AgentRun {
  id: string;
  agent_id: string;
  title: string;
  status: string;
  thread_id?: string;
  time: string;
  duration_ms?: number;
}

export interface RunStep {
  kind: string;
  title: string;
  at: string;
  ok: boolean;
}

export interface RunDetail {
  task_id: string;
  space_id: string;
  worker_id: string;
  worker_name?: string;
  title: string;
  status: string;
  outcome?: string;
  result_message_id?: string;
  trigger_message_id?: string;
  created_at: string;
  updated_at: string;
  key_steps?: RunStep[];
}

export interface ParticipantsView {
  agents: AgentItem[];
  running_agent?: string;
  active_runs?: AgentRun[];
  recent_runs?: AgentRun[];
}

export interface PersonaItem {
  id: string;
  display: string;
  runtime: string;
  model?: string;
  description?: string;
  tools?: string[];
  show_in_sidebar: boolean;
}

export interface ModelItem {
  name: string;
  provider: string;
  model: string;
  max_tokens?: number;
  context_window?: number;
  ready: boolean;
}

export interface ToolItem {
  name: string;
  description?: string;
  enabled: boolean;
}

export interface CommandItem {
  name: string;
  summary?: string;
  usage?: string;
}
