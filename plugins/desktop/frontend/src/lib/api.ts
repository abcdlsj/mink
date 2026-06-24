import type {
  AgentDMItem,
  AgentItem,
  AgentRun,
  CapabilityView,
  ChannelItem,
  CommandItem,
  ContextInspectView,
  ContextResetResult,
  DirectChatItem,
  DeleteConversationResult,
  MemoryDocDetail,
  MemoryOverviewView,
  ModelItem,
  ParticipantsView,
  PersonaItem,
  RecentItem,
  RunDetail,
  SessionDetail,
  SkillView,
  ThreadDetail,
  ThreadItem,
  ThreadSummary,
  TaskStateCard,
  ToolItem,
  WorkspaceState,
  AttachmentView,
} from "./types";

const j = async <T>(p: Promise<Response>): Promise<T> => {
  const r = await p;
  if (!r.ok) {
    const detail = await r.text().catch(() => "");
    const trimmed = detail.trim();
    throw new Error(trimmed !== "" ? trimmed : `${r.status} ${r.statusText}`);
  }
  return r.json();
};

const get = <T>(path: string): Promise<T> => j<T>(fetch(path));

const post = <T>(path: string, body: unknown = {}): Promise<T> =>
  j<T>(
    fetch(path, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(body),
    }),
  );

export const api = {
  state: () => get<WorkspaceState>("/api/state"),
  channels: () => get<ChannelItem[]>("/api/channels"),
  createChannel: (name: string) => post<ChannelItem>("/api/channel/create", { name }),
  threads: () => get<ThreadItem[]>("/api/threads"),
  agents: () => get<AgentItem[]>("/api/agents"),
  setChannelAgentMode: (channelID: string, personaID: string, mode: string) =>
    post<{ ok: string }>("/api/channel/agent-mode", { channel_id: channelID, persona_id: personaID, mode }),
  addAgentToChannel: (channelID: string, personaID: string) =>
    post<{ ok: string }>("/api/channel/add-agent", { channel_id: channelID, persona_id: personaID }),
  setThreadAgentMode: (
    spaceID: string,
    parentMessageID: string,
    personaID: string,
    mode: string,
  ) =>
    post<{ ok: string }>("/api/thread/agent-mode", {
      space_id: spaceID,
      parent_message_id: parentMessageID,
      persona_id: personaID,
      mode,
    }),
  channel: (id: string) => get<SessionDetail>("/api/channel?id=" + encodeURIComponent(id)),
  agentDM: (agentID: string) =>
    get<SessionDetail>("/api/agent-dm?agent=" + encodeURIComponent(agentID)),
  agentDMs: () => get<AgentDMItem[]>("/api/agent-dms"),
  createAgentDM: (personaID: string, title?: string) =>
    post<AgentDMItem>("/api/agent-dm/create", { persona_id: personaID, title }),
  updateAgentDMTitle: (id: string, title: string) =>
    post<AgentDMItem>("/api/agent-dm/title", { id, title }),
  newDirect: (title?: string, agentID?: string) =>
    post<SessionDetail>("/api/new-direct", { title: title || "", agent_id: agentID || "" }),
  directChats: () => get<DirectChatItem[]>("/api/direct-chats"),
  directChat: (id: string) =>
    get<SessionDetail>("/api/direct-chat?id=" + encodeURIComponent(id)),
  updateDirectChatTitle: (id: string, title: string) =>
    post<DirectChatItem>("/api/direct-chat/title", { id, title }),
  recent: () => get<RecentItem[]>("/api/recent"),
  run: (id: string) => get<RunDetail>("/api/run?id=" + encodeURIComponent(id)),
  updateTaskStatus: (taskID: string, status: string) =>
    post<AgentRun>("/api/task/status", { task_id: taskID, status }),
  createTask: (input: {
    space_id: string;
    source_message?: string;
    source_message_id?: string;
    source_thread?: string;
    source_thread_id?: string;
    created_by?: string;
    assignee_id?: string;
    assignee?: string;
    assigned_by?: string;
    title: string;
    outcome?: string;
    expected_outcome?: string;
    acceptance_criteria?: string;
    source?: string;
    explicit_task_intent?: boolean;
  }) =>
    post<TaskStateCard>("/api/task/create", input),
  assignTask: (input: {
    task_id: string;
    assignee_id?: string;
    assignee?: string;
    assigned_by?: string;
    outcome?: string;
    expected_outcome?: string;
    acceptance_criteria?: string;
  }) =>
    post<TaskStateCard>("/api/task/assign", input),
  threadsForSpace: (spaceId: string) =>
    get<ThreadSummary[]>("/api/threads-for-space?space=" + encodeURIComponent(spaceId)),
  threadDetail: (spaceId: string, parentId: string) => {
    const q = new URLSearchParams();
    q.set("space", spaceId);
    q.set("parent", parentId);
    return get<ThreadDetail>("/api/thread-detail?" + q);
  },
  participants: (channelID: string, threadID: string) => {
    const q = new URLSearchParams();
    if (channelID) q.set("channel", channelID);
    if (threadID) q.set("thread", threadID);
    return get<ParticipantsView>("/api/participants?" + q);
  },
  models: () => get<ModelItem[]>("/api/models"),
  tools: () => get<ToolItem[]>("/api/tools"),
  commands: () => get<CommandItem[]>("/api/commands"),
  capabilities: () => get<CapabilityView>("/api/capabilities"),
  skills: () => get<SkillView[]>("/api/skills"),
  skill: (name: string) => get<SkillView>("/api/skill?name=" + encodeURIComponent(name)),
  personas: () => get<PersonaItem[]>("/api/personas"),
  send: (sessionID: string, input: string, personaID?: string, parentMessageID?: string, attachments?: AttachmentView[]) =>
    post<{ output: string }>("/api/send", {
      session_id: sessionID,
      input,
      persona_id: personaID,
      parent_message_id: parentMessageID,
      attachments,
    }),
  retryMessage: (spaceID: string, messageID: string) =>
    post<{ output: string }>("/api/message/retry", { space_id: spaceID, message_id: messageID }),
  stop: (sessionID: string) =>
    post<{ ok: boolean }>("/api/stop", { session_id: sessionID }),
  deleteConversation: (input: { kind: string; id: string; parent_message_id?: string }) =>
    post<DeleteConversationResult>("/api/conversation/delete", input),
  contextInspect: (input: {
    space_id?: string;
    source?: string;
    session_source?: string;
    parent_message_id?: string;
    agent_id?: string;
    profile?: string;
  }) => {
    const q = new URLSearchParams();
    if (input.space_id) q.set("space_id", input.space_id);
    if (input.source) q.set("source", input.source);
    if (input.session_source) q.set("session_source", input.session_source);
    if (input.parent_message_id) q.set("parent_message_id", input.parent_message_id);
    if (input.agent_id) q.set("agent_id", input.agent_id);
    if (input.profile) q.set("profile", input.profile);
    return get<ContextInspectView>("/api/context/inspect?" + q);
  },
  contextReset: (input: {
    action: "runtime_session" | "summary";
    space_id?: string;
    source?: string;
    session_source?: string;
    parent_message_id?: string;
    agent_id?: string;
  }) =>
    post<ContextResetResult>("/api/context/reset", input),
  memoryOverview: (input: {
    persona_id?: string;
    source?: string;
    space_id?: string;
  }) => {
    const q = new URLSearchParams();
    if (input.persona_id) q.set("persona_id", input.persona_id);
    if (input.source) q.set("source", input.source);
    if (input.space_id) q.set("space_id", input.space_id);
    return get<MemoryOverviewView>("/api/memory/overview?" + q);
  },
  memoryDoc: (input: { scope: string; id: string }) => {
    const q = new URLSearchParams();
    q.set("scope", input.scope);
    q.set("id", input.id);
    return get<MemoryDocDetail>("/api/memory/doc?" + q);
  },
  updateMemory: (input: {
    persona_id?: string;
    source?: string;
    space_id?: string;
    scope: string;
    id: string;
    title: string;
    body: string;
    summary?: string;
    kind?: string;
    confidence?: string;
  }) =>
    post<{ ok: boolean; output: string; memory: MemoryDocDetail; overview: MemoryOverviewView }>("/api/memory/update", input),
  deleteMemory: (input: {
    persona_id?: string;
    source?: string;
    space_id?: string;
    scope: string;
    id: string;
  }) =>
    post<{ ok: boolean; output: string; overview: MemoryOverviewView }>("/api/memory/delete", input),
};
