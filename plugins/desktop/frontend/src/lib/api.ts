import type {
  AgentDMItem,
  AgentItem,
  ChannelItem,
  CommandItem,
  DirectChatItem,
  ModelItem,
  ParticipantsView,
  PersonaItem,
  RecentItem,
  RunDetail,
  SessionDetail,
  ThreadDetail,
  ThreadItem,
  ThreadSummary,
  ToolItem,
  WorkspaceState,
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

export const api = {
  state: () => j<WorkspaceState>(fetch("/api/state")),
  channels: () => j<ChannelItem[]>(fetch("/api/channels")),
  createChannel: (name: string) =>
    j<ChannelItem>(
      fetch("/api/channel/create", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ name }),
      }),
    ),
  threads: () => j<ThreadItem[]>(fetch("/api/threads")),
  agents: () => j<AgentItem[]>(fetch("/api/agents")),
  channel: (id: string) => j<SessionDetail>(fetch("/api/channel?id=" + encodeURIComponent(id))),
  thread: (id: string) => j<SessionDetail>(fetch("/api/thread?id=" + encodeURIComponent(id))),
  agentDM: (agentID: string) =>
    j<SessionDetail>(fetch("/api/agent-dm?agent=" + encodeURIComponent(agentID))),
  agentDMs: () => j<AgentDMItem[]>(fetch("/api/agent-dms")),
  createAgentDM: (personaID: string) =>
    j<AgentDMItem>(
      fetch("/api/agent-dm/create", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ persona_id: personaID }),
      }),
    ),
  newDirect: () =>
    j<SessionDetail>(
      fetch("/api/new-direct", { method: "POST" }),
    ),
  directChats: () => j<DirectChatItem[]>(fetch("/api/direct-chats")),
  directChat: (id: string) =>
    j<SessionDetail>(fetch("/api/direct-chat?id=" + encodeURIComponent(id))),
  recent: () => j<RecentItem[]>(fetch("/api/recent")),
  run: (id: string) => j<RunDetail>(fetch("/api/run?id=" + encodeURIComponent(id))),
  threadsForSpace: (spaceId: string) =>
    j<ThreadSummary[]>(fetch("/api/threads-for-space?space=" + encodeURIComponent(spaceId))),
  threadDetail: (spaceId: string, parentId: string) => {
    const q = new URLSearchParams();
    q.set("space", spaceId);
    q.set("parent", parentId);
    return j<ThreadDetail>(fetch("/api/thread-detail?" + q));
  },
  participants: (channelID: string, threadID: string) => {
    const q = new URLSearchParams();
    if (channelID) q.set("channel", channelID);
    if (threadID) q.set("thread", threadID);
    return j<ParticipantsView>(fetch("/api/participants?" + q));
  },
  models: () => j<ModelItem[]>(fetch("/api/models")),
  tools: () => j<ToolItem[]>(fetch("/api/tools")),
  commands: () => j<CommandItem[]>(fetch("/api/commands")),
  personas: () => j<PersonaItem[]>(fetch("/api/personas")),
  send: (sessionID: string, input: string, personaID?: string, parentMessageID?: string) =>
    j<{ output: string }>(
      fetch("/api/send", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({
          session_id: sessionID,
          input,
          persona_id: personaID,
          parent_message_id: parentMessageID,
        }),
      }),
    ),
  stop: (sessionID: string) =>
    j<{ ok: boolean }>(
      fetch("/api/stop", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ session_id: sessionID }),
      }),
    ),
};
