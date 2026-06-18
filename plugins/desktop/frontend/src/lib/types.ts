export type ViewMode = "channel" | "direct" | "agent" | "agent_detail" | "home" | "tasks";

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
  title_fixed?: boolean;
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
  status?: "pending" | "failed" | string;
  error?: string;
  time: string;
  events?: EventBlock[];
  thread_id?: string;
  thread_summary?: string;
  thread_info?: ThreadSummary;
  is_thread_reply?: boolean;
  task_accessory?: TaskAccessoryInfo;
  auto_reply_reason?: string;
  mentions?: string[];
  usage?: TokenUsage;
  runtime_meta?: Record<string, string>;
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
  archived_runs_count?: number;
  active_worker_id?: string;
  last_reply_time?: string;
  not_found?: boolean;
  unsupported?: boolean;
  unsupported_hint?: string;
}

export interface SessionItem {
  id: string;
  title: string;
  title_fixed?: boolean;
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

export interface DeleteConversationResult {
  ok: boolean;
  deleted_space?: boolean;
  deleted_sessions?: number;
  deleted_tasks?: number;
}

export interface ContextInspectMessage {
  id: string;
  role: string;
  author_id?: string;
  content?: string;
  tokens?: number;
  created_at?: string;
}

export interface ContextFilteredCount {
  reason: string;
  count: number;
}

export interface ContextInspectView {
  profile: string;
  source?: string;
  session_source?: string;
  session_id?: string;
  space_id?: string;
  parent_message_id?: string;
  agent_id?: string;
  token_limit?: number;
  raw_message_count: number;
  eligible_count: number;
  selected_count: number;
  summarized_count: number;
  filtered_counts?: ContextFilteredCount[];
  summary?: string;
  session_summary?: string;
  messages: ContextInspectMessage[];
  notes?: string[];
}

export interface ContextResetResult {
  ok: boolean;
  action: string;
  source?: string;
  session_source?: string;
  previous_session_id?: string;
  session_id?: string;
  cleared_summary?: boolean;
  removed_summary_messages?: number;
  note?: string;
}

export interface MemoryDocView {
  id: string;
  title: string;
  summary?: string;
  kind?: string;
  updated_at: string;
}

export interface MemoryScopeView {
  kind: string;
  key?: string;
  label: string;
  recent: MemoryDocView[];
}

export interface MemoryOverviewView {
  scopes: MemoryScopeView[];
}

export interface AgentRun {
  id: string;
  agent_id: string;
  title: string;
  status: string;
  lifecycle?: "active" | "archived";
  created_by?: string;
  assigned_by?: string;
  space_id?: string;
  thread_id?: string;
  trigger_message_id?: string;
  parent_message_id?: string;
  expected_outcome?: string;
  acceptance_criteria?: string;
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
  created_by?: string;
  assigned_by?: string;
  title: string;
  status: string;
  expected_outcome?: string;
  acceptance_criteria?: string;
  outcome?: string;
  result_message_id?: string;
  trigger_message_id?: string;
  created_at: string;
  updated_at: string;
  key_steps?: RunStep[];
  state?: TaskStateView;
}

export interface ParticipantsView {
  agents: AgentItem[];
  running_agent?: string;
  active_runs?: AgentRun[];
  recent_runs?: AgentRun[];
  archived_runs_count?: number;
}

export interface PersonaItem {
  id: string;
  display: string;
  runtime: string;
  model?: string;
  description?: string;
  tools?: string[];
  capabilities?: string[];
  task_policy?: string;
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

export interface TaskStateView {
  goal?: string;
  todo?: string[];
  checkpoint?: string;
  artifacts?: string[];
  blockers?: string[];
  related_ids?: string[];
}

export interface CapabilityView {
  skills: SkillView[];
  tasks: TaskStateCard[];
  archived_task_state_count?: number;
  action_proposals: ActionProposalCard[];
}

export interface SkillView {
  name: string;
  description?: string;
  when?: string;
  risk?: string;
  env?: string[];
  env_needs?: SkillEnvNeed[];
  entrypoints?: string[];
  examples?: string[];
  path?: string;
  configured: boolean;
  missing_env?: string[];
  last_action?: string;
  last_listed?: string;
  last_described?: string;
  last_used?: string;
  body?: string;
}

export interface SkillEnvNeed {
  name: string;
  configured: boolean;
  hint?: string;
}

export interface TaskStateCard {
  id: string;
  title: string;
  status: string;
  lifecycle?: "active" | "archived";
  created_by?: string;
  worker_id?: string;
  assignee_id?: string;
  assignee?: string;
  assigned_by?: string;
  space_id?: string;
  source?: string;
  source_message?: string;
  source_thread_id?: string;
  source_thread?: string;
  trigger_message_id?: string;
  parent_message_id?: string;
  updated_at: string;
  expected_outcome?: string;
  acceptance_criteria?: string;
  outcome?: string;
  state?: TaskStateView;
  latest_run?: string;
  run_status?: string;
  run_started?: string;
}

export interface ActionProposalCard {
  time: string;
  source?: string;
  tool?: string;
  result?: string;
  intent?: string;
  target?: string;
  risk?: string;
  preview?: string;
  rollback?: string;
  expires_at?: string;
}
