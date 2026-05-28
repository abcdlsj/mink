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

export interface AgentItem {
  id: string;
  display: string;
  role?: string;
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
}

export interface SessionItem {
  id: string;
  title: string;
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
  status: EventStatus;
  thread_id?: string;
  time: string;
  duration_ms?: number;
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
  description?: string;
  tools?: string[];
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
