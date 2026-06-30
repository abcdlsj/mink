import { create } from "zustand";
import type {
  AttachmentView,
  AgentDMItem,
  AgentItem,
  CapabilityView,
  ChannelItem,
  CommandItem,
  DirectChatItem,
  EventBlock,
  MessageView,
  ModelItem,
  ParticipantsView,
  PersonaItem,
  RecentItem,
  SessionDetail,
  ThreadDetail,
  ThreadItem,
  ToolItem,
  ViewMode,
  WorkspaceState,
} from "./types";
import { api } from "./api";
import { parseWebRoute, writeRouteAnchor, writeWebRoute, type RouteWriteOptions } from "./deeplink";

interface BusEvent {
  type: string;
  source?: string;
  session_id?: string;
  task_id?: string;
  run_id?: string;
  message_id?: string;
  tool_call_id?: string;
  tool?: string;
  input?: string;
  output?: string;
  text?: string;
  err?: string;
  time: string;
  space_id?: string;
  parent_message_id?: string;
  agent_id?: string;
  stream_id?: string;
}

interface StreamingTurn {
  messageID: string;
  streamID: string;
  agentID: string;
  spaceID: string;
  parentMessageID: string;
  startedAt: string;
  lastEventAt: number;
  content: string;
  reasoning: string;
  toolCalls: Map<string, EventBlock>;
}

interface State {
  ready: boolean;
  connectionStatus: "connecting" | "online" | "offline";
  connectionMessage: string;
  state: WorkspaceState | null;
  channels: ChannelItem[];
  threads: ThreadItem[];
  directChats: DirectChatItem[];
  agentDMs: AgentDMItem[];
  recent: RecentItem[];
  agents: AgentItem[];
  personas: PersonaItem[];
  models: ModelItem[];
  tools: ToolItem[];
  commands: CommandItem[];
  capabilities: CapabilityView | null;
  detail: SessionDetail | null;
  threadDetail: ThreadDetail | null;
  participants: ParticipantsView | null;

  view: ViewMode;
  activeChannel: string | null;
  activeDirect: string | null;
  activeThread: string | null;
  activeAgentSpace: string | null;
  activeAgentID: string | null;
  activeAnchor: string | null;
  expandedTaskID: string | null;

  paletteOpen: boolean;
  quickCreateOpen: boolean;
  composerHint: { text: string; at: number } | null;
  routeNotice: { text: string; at: number } | null;
  sending: boolean;
  sendingByScope: Record<string, boolean>;
  streaming: StreamingTurn | null;
  streamingByID: Record<string, StreamingTurn>;

  loadInitial: () => Promise<void>;
  syncNow: () => Promise<void>;
  refreshCapabilities: () => Promise<void>;
  openChannel: (id: string, routeOpts?: RouteWriteOptions) => Promise<void>;
  createChannel: (name: string) => Promise<ChannelItem>;
  setChannelAgentMode: (channelID: string, personaID: string, mode: string) => Promise<void>;
  addAgentToChannel: (channelID: string, personaID: string) => Promise<void>;
  setThreadAgentMode: (spaceID: string, parentMessageID: string, personaID: string, mode: string) => Promise<void>;
  openThread: (id: string, routeOpts?: RouteWriteOptions) => Promise<void>;
  closeThread: (routeOpts?: RouteWriteOptions) => void;
  openAgentDetail: (id: string, routeOpts?: RouteWriteOptions) => void;
  openAgentsPanel: (routeOpts?: RouteWriteOptions) => void;
  openAgent: (id: string, routeOpts?: RouteWriteOptions) => Promise<void>;
  newAgentChat: (personaID: string, title?: string) => Promise<void>;
  updateAgentChatTitle: (id: string, title: string) => Promise<void>;
  openTaskBoard: (routeOpts?: RouteWriteOptions) => void;
  openDirectChat: (id: string, routeOpts?: RouteWriteOptions) => Promise<void>;
  newDirectChat: (title?: string, agentID?: string) => Promise<void>;
  updateDirectChatTitle: (id: string, title: string) => Promise<void>;
  deleteConversation: (input: { kind: "channel" | "direct_chat" | "agent_dm" | "thread"; id: string; parentMessageID?: string }) => Promise<void>;
  setPalette: (open: boolean) => void;
  setQuickCreate: (open: boolean) => void;
  send: (input: string, personaID?: string, options?: SendOptions) => Promise<void>;
  retryMessage: (spaceID: string, messageID: string) => Promise<void>;
  stop: () => Promise<void>;
  expandTaskInRail: (taskID: string) => void;
  focusTaskOrigin: (taskID: string, messageID?: string) => void;
  collapseTaskInRail: () => void;
  connectStream: () => () => void;
  applyEvent: (ev: BusEvent) => void;
  openCurrentRoute: () => Promise<void>;
}

function activeSessionID(s: State): string {
  if (s.view === "direct" && s.activeDirect) return s.activeDirect;
  if (s.view === "agent" && s.activeAgentSpace) return s.detail?.item.id || s.activeAgentSpace;
  return s.activeChannel || "";
}

function scopeKey(spaceID: string, parentMessageID?: string): string {
  return parentMessageID ? spaceID + "::thread:" + parentMessageID : spaceID;
}

interface SendOptions {
  parentMessageID?: string | null;
  scopeKey?: string;
  attachments?: AttachmentView[];
}

class ScopeMatcher {
  constructor(private readonly s: State) {}

  activeScopeKey(): string {
    if (this.s.threadDetail && !this.s.threadDetail.unsupported && !this.s.threadDetail.not_found) {
      return scopeKey(this.s.threadDetail.space_id, this.s.threadDetail.parent_id);
    }
    const sid = activeSessionID(this.s);
    return sid ? scopeKey(sid) : "";
  }

  lifecycleEventInScope(ev: BusEvent): boolean {
    if (!ev.space_id) return false;
    if (this.s.threadDetail && !this.s.threadDetail.unsupported && !this.s.threadDetail.not_found) {
      if (ev.space_id !== this.s.activeChannel) return false;
      if (!ev.parent_message_id) return true;
      if (ev.parent_message_id === this.s.threadDetail.parent_id) return true;
      if (ev.message_id === this.s.threadDetail.parent_id) return true;
      return this.s.threadDetail.replies.some((r) => r.id === ev.parent_message_id || r.id === ev.message_id);
    }
    if (this.s.view === "agent") {
      if (!this.s.detail) return false;
      return ev.space_id === this.s.detail.item.id;
    }
    if (this.s.view === "channel") {
      if (ev.space_id !== this.s.activeChannel) return false;
      return !ev.parent_message_id;
    }
    if (this.s.view === "direct") {
      if (ev.space_id !== this.s.activeDirect) return false;
      return !ev.parent_message_id;
    }
    return false;
  }

  streamMatchesMain(stream: StreamingTurn): boolean {
    if (stream.parentMessageID) return false;
    if (this.s.view === "agent") return !!this.s.detail && this.s.detail.item.id === stream.spaceID;
    if (this.s.view === "direct") return this.s.activeDirect === stream.spaceID;
    if (this.s.view === "channel") return this.s.activeChannel === stream.spaceID;
    return false;
  }

  streamMatchesThread(stream: StreamingTurn): boolean {
    if (!this.s.threadDetail || this.s.threadDetail.unsupported || this.s.threadDetail.not_found) return false;
    return this.s.threadDetail.space_id === stream.spaceID && this.s.threadDetail.parent_id === stream.parentMessageID;
  }
}

function activeScopeKey(s: State): string {
  return new ScopeMatcher(s).activeScopeKey();
}

function newID(): string {
  return Math.random().toString(36).slice(2, 10);
}

function lifecycleEventInScope(ev: BusEvent, s: State): boolean {
  return new ScopeMatcher(s).lifecycleEventInScope(ev);
}

function streamingViewUpdates(
  s: State,
  stream: StreamingTurn,
  placeholder: MessageView,
): Partial<State> {
  const detail = s.detail;
  const updates: Partial<State> = {};
  const matcher = new ScopeMatcher(s);
  if (detail && matcher.streamMatchesMain(stream)) {
    const exists = detail.messages.some((m) => m.id === placeholder.id);
    updates.detail = {
      ...detail,
      messages: exists ? detail.messages : [...detail.messages, placeholder],
    };
  }
  if (s.threadDetail && matcher.streamMatchesThread(stream)) {
    const exists = s.threadDetail.replies.some((m) => m.id === placeholder.id);
    updates.threadDetail = {
      ...s.threadDetail,
      replies: exists ? s.threadDetail.replies : [...s.threadDetail.replies, placeholder],
    };
  }
  return updates;
}

function streamingMessageUpdates(
  s: State,
  stream: StreamingTurn,
  patch: (m: MessageView) => MessageView,
): Partial<State> {
  const updates: Partial<State> = {};
  const matcher = new ScopeMatcher(s);
  if (s.detail && matcher.streamMatchesMain(stream)) {
    updates.detail = {
      ...s.detail,
      messages: s.detail.messages.map((m) => (m.id === stream.messageID ? patch(m) : m)),
    };
  }
  if (s.threadDetail && matcher.streamMatchesThread(stream)) {
    updates.threadDetail = {
      ...s.threadDetail,
      replies: s.threadDetail.replies.map((m) => (m.id === stream.messageID ? patch(m) : m)),
    };
  }
  return updates;
}

function streamMatchesMainScope(s: State, stream: StreamingTurn): boolean {
  return new ScopeMatcher(s).streamMatchesMain(stream);
}

function streamMatchesThreadScope(s: State, stream: StreamingTurn): boolean {
  return new ScopeMatcher(s).streamMatchesThread(stream);
}

function currentStreaming(streamingByID: Record<string, StreamingTurn>): StreamingTurn | null {
  return Object.values(streamingByID)[0] || null;
}

const staleStreamGraceMs = 20_000;

function eventTimeMs(time: string): number {
  const parsed = Date.parse(time);
  return Number.isNaN(parsed) ? Date.now() : parsed;
}

function activeRunIDs(s: State): Set<string> {
  const runs =
    s.threadDetail && !s.threadDetail.unsupported && !s.threadDetail.not_found
      ? s.threadDetail.active_runs || []
      : s.participants?.active_runs || [];
  return new Set(runs.map((r) => r.id));
}

function scopeMatchesStream(s: State, stream: StreamingTurn): boolean {
  const matcher = new ScopeMatcher(s);
  if (s.threadDetail && !s.threadDetail.unsupported && !s.threadDetail.not_found) {
    return matcher.streamMatchesThread(stream);
  }
  return matcher.streamMatchesMain(stream);
}

function pruneStreamingState(s: State): Partial<State> {
  if (Object.keys(s.streamingByID).length === 0) return {};
  const now = Date.now();
  const activeIDs = activeRunIDs(s);
  const next: Record<string, StreamingTurn> = {};
  const removed = new Set<string>();
  for (const stream of Object.values(s.streamingByID)) {
    if (!scopeMatchesStream(s, stream)) continue;
    if (activeIDs.has(stream.streamID)) {
      next[stream.streamID] = stream;
      continue;
    }
    if (now - stream.lastEventAt < staleStreamGraceMs) {
      next[stream.streamID] = stream;
      continue;
    }
    removed.add(stream.messageID);
  }
  if (removed.size === 0 && Object.keys(next).length === Object.keys(s.streamingByID).length) {
    return {};
  }
  const fail = (m: MessageView): MessageView =>
    m.status === "pending"
      ? {
          ...m,
          status: "failed",
          error: "Agent reply was interrupted. Retry to run this message again.",
        }
      : m;
  const updates: Partial<State> = {
    streaming: currentStreaming(next),
    streamingByID: next,
  };
  if (removed.size > 0) {
    if (s.threadDetail && !s.threadDetail.unsupported && !s.threadDetail.not_found) {
      updates.threadDetail = {
        ...s.threadDetail,
        replies: s.threadDetail.replies.map((m) => (removed.has(m.id) ? fail(m) : m)),
      };
    } else if (s.detail) {
      updates.detail = {
        ...s.detail,
        messages: s.detail.messages.map((m) => (removed.has(m.id) ? fail(m) : m)),
      };
    }
  }
  return updates;
}

function pruneVisibleStreamingState(s: State): Partial<State> {
  if (s.threadDetail && !s.threadDetail.unsupported && !s.threadDetail.not_found) {
    return pruneStreamingState(s);
  }
  if (s.detail) {
    return pruneStreamingState(s);
  }
  return {};
}

function applyRouteAnchor(anchor: string | undefined): Pick<State, "activeAnchor" | "expandedTaskID"> {
  const taskID =
    anchor?.startsWith("task:") || anchor?.startsWith("run:")
      ? anchor.slice(anchor.indexOf(":") + 1)
      : null;
  return {
    activeAnchor: anchor || null,
    expandedTaskID: taskID || null,
  };
}

function streamForEvent(s: State, ev: BusEvent): StreamingTurn | null {
  if (ev.stream_id) return s.streamingByID[ev.stream_id] || null;
  return s.streaming;
}

function updateStream(s: State, stream: StreamingTurn): Pick<State, "streaming" | "streamingByID"> {
  const streamingByID = { ...s.streamingByID, [stream.streamID]: stream };
  return { streaming: stream, streamingByID };
}

function removeStream(s: State, streamID: string): Pick<State, "streaming" | "streamingByID"> {
  const streamingByID = { ...s.streamingByID };
  delete streamingByID[streamID];
  return { streaming: currentStreaming(streamingByID), streamingByID };
}

function interruptLocalStreams(s: State, message: string): Partial<State> {
  const updates: Partial<State> = {
    streaming: null,
    streamingByID: {},
    sending: false,
    sendingByScope: {},
  };
  const patch = (m: MessageView): MessageView =>
    m.status === "pending"
      ? { ...m, status: "failed", error: message }
      : m;
  if (s.detail) {
    const streamIDs = new Set(Object.values(s.streamingByID).filter((stream) => streamMatchesMainScope(s, stream)).map((stream) => stream.messageID));
    updates.detail = {
      ...s.detail,
      messages: s.detail.messages.map((m) => (streamIDs.has(m.id) ? patch(m) : m)),
    };
  }
  if (s.threadDetail && !s.threadDetail.unsupported && !s.threadDetail.not_found) {
    const streamIDs = new Set(Object.values(s.streamingByID).filter((stream) => streamMatchesThreadScope(s, stream)).map((stream) => stream.messageID));
    updates.threadDetail = {
      ...s.threadDetail,
      replies: s.threadDetail.replies.map((m) => (streamIDs.has(m.id) ? patch(m) : m)),
    };
  }
  return updates;
}

async function refetchActiveScope(
  get: () => State,
  set: (partial: Partial<State>) => void,
): Promise<void> {
  const s = get();
  try {
    if (s.threadDetail && !s.threadDetail.unsupported && !s.threadDetail.not_found) {
      const updates: Partial<State> = {};
      const td = await api.threadDetail(s.threadDetail.space_id, s.threadDetail.parent_id);
      updates.threadDetail = td;
      if (s.view === "channel" && s.activeChannel) {
        const [detail, participants, channels] = await Promise.all([
          api.channel(s.activeChannel),
          api.participants(s.activeChannel, ""),
          api.channels(),
        ]);
        updates.detail = detail;
        updates.participants = participants;
        updates.channels = channels;
      }
      set(updates);
      return;
    }
    if (s.view === "channel" && s.activeChannel) {
      const [detail, participants, channels] = await Promise.all([
        api.channel(s.activeChannel),
        api.participants(s.activeChannel, ""),
        api.channels(),
      ]);
      set({ detail, participants, channels });
      return;
    }
    if (s.view === "direct" && s.activeDirect) {
      const detail = await api.directChat(s.activeDirect);
      set({ detail });
      return;
    }
    if (s.view === "agent" && s.activeAgentSpace) {
      const detail = await api.agentDM(s.activeAgentSpace);
      set({ detail });
      return;
    }
  } catch {}
}

async function refetchActiveChannelMeta(
  get: () => State,
  set: (partial: Partial<State>) => void,
): Promise<void> {
  const s = get();
  if (!s.activeChannel) return;
  try {
    const [channels, participants] = await Promise.all([
      api.channels(),
      api.participants(s.activeChannel, ""),
    ]);
    set({ channels, participants });
  } catch {}
}

async function refetchNavigation(
  set: (partial: Partial<State>) => void,
): Promise<void> {
  try {
    const [channels, threads, directChats, agentDMs, recent] = await Promise.all([
      api.channels(),
      api.threads(),
      api.directChats(),
      api.agentDMs(),
      api.recent(),
    ]);
    set({ channels, threads, directChats, agentDMs, recent });
  } catch {}
}

async function fetchNavigationSnapshot(): Promise<Pick<State, "channels" | "threads" | "directChats" | "agentDMs" | "recent">> {
  const [channels, threads, directChats, agentDMs, recent] = await Promise.all([
    api.channels(),
    api.threads(),
    api.directChats(),
    api.agentDMs(),
    api.recent(),
  ]);
  return { channels, threads, directChats, agentDMs, recent };
}

export const useStore = create<State>((set, get) => ({
  ready: false,
  connectionStatus: "connecting",
  connectionMessage: "",
  state: null,
  channels: [],
  threads: [],
  directChats: [],
  agentDMs: [],
  recent: [],
  agents: [],
  personas: [],
  models: [],
  tools: [],
  commands: [],
  capabilities: null,
  detail: null,
  threadDetail: null,
  participants: null,

  view: "channel",
  activeChannel: null,
  activeDirect: null,
  activeThread: null,
  activeAgentSpace: null,
  activeAgentID: null,
  activeAnchor: null,
  expandedTaskID: null,

  paletteOpen: false,
  quickCreateOpen: false,
  composerHint: null,
  routeNotice: null,
  sending: false,
  sendingByScope: {},
  streaming: null,
  streamingByID: {},

  async loadInitial() {
    try {
      const [state, channels, threads, directChats, agentDMs, recent, agents, personas, models, tools, commands, capabilities] = await Promise.all([
        api.state(),
        api.channels(),
        api.threads(),
        api.directChats(),
        api.agentDMs(),
        api.recent(),
        api.agents(),
        api.personas(),
        api.models(),
        api.tools(),
        api.commands(),
        api.capabilities().catch(() => ({ skills: [], tasks: [], action_proposals: [] })),
      ]);
      set({
        state,
        channels,
        threads,
        directChats,
        agentDMs,
        recent,
        agents,
        personas,
        models,
        tools,
        commands,
        capabilities,
        ready: true,
        connectionStatus: "online",
        connectionMessage: "",
      });
      const initialRoute = parseWebRoute();
      if (initialRoute) {
        try {
          if (initialRoute.view === "home") {
            set({
              view: "home",
              detail: null,
              threadDetail: null,
              participants: null,
              activeChannel: null,
              activeDirect: null,
              activeThread: null,
              activeAgentSpace: null,
              activeAgentID: null,
              activeAnchor: null,
              expandedTaskID: null,
            });
            writeWebRoute({ view: "home" }, { replace: true });
            return;
          }
          if (initialRoute.view === "channel") {
            await get().openChannel(initialRoute.id, { replace: true });
            if (initialRoute.thread) await get().openThread(initialRoute.thread, { replace: true });
            set(applyRouteAnchor(initialRoute.anchor));
            if (initialRoute.anchor) writeRouteAnchor(initialRoute.anchor, { replace: true });
            return;
          }
          if (initialRoute.view === "direct") {
            await get().openDirectChat(initialRoute.id, { replace: true });
            set(applyRouteAnchor(initialRoute.anchor));
            if (initialRoute.anchor) writeRouteAnchor(initialRoute.anchor, { replace: true });
            return;
          }
          if (initialRoute.view === "agents") {
            get().openAgentsPanel({ replace: true });
            return;
          }
          if (initialRoute.view === "agent") {
            await get().openAgent(initialRoute.id, { replace: true });
            set(applyRouteAnchor(initialRoute.anchor));
            if (initialRoute.anchor) writeRouteAnchor(initialRoute.anchor, { replace: true });
            return;
          }
          if (initialRoute.view === "tasks") {
            get().openTaskBoard({ replace: true });
            set(applyRouteAnchor(initialRoute.anchor));
            if (initialRoute.anchor) writeRouteAnchor(initialRoute.anchor, { replace: true });
            return;
          }
        } catch {
          // Bad or stale deep links should not block the desktop from opening.
        }
      }
      if (channels.length) {
        await get().openChannel(channels[0].id, { replace: true });
      } else {
        set({
          view: "home",
          detail: null,
          threadDetail: null,
          participants: null,
          activeChannel: null,
          activeDirect: null,
          activeThread: null,
          activeAgentSpace: null,
          activeAgentID: null,
          activeAnchor: null,
          expandedTaskID: null,
        });
        writeWebRoute({ view: "home" }, { replace: true });
      }
    } catch (err) {
      set({
        ready: true,
        connectionStatus: "offline",
        connectionMessage: err instanceof Error ? err.message : "Desktop backend is offline.",
      });
    }
  },

  async syncNow() {
    if (!get().ready) return;
    try {
      const [state, nav, models, capabilities] = await Promise.all([
        api.state(),
        fetchNavigationSnapshot(),
        api.models(),
        api.capabilities().catch(() => get().capabilities || { skills: [], tasks: [], action_proposals: [] }),
      ]);
      set({
        state,
        ...nav,
        models,
        capabilities,
        connectionStatus: "online",
        connectionMessage: "",
      });
      await refetchActiveScope(get, set);
      set((cur) => pruneVisibleStreamingState(cur));
    } catch (err) {
      set({
        connectionStatus: "offline",
        connectionMessage: err instanceof Error ? err.message : "Desktop backend is offline.",
      });
    }
  },

  async refreshCapabilities() {
    try {
      const capabilities = await api.capabilities();
      set({ capabilities });
    } catch {
      set({ capabilities: { skills: [], tasks: [], action_proposals: [] } });
    }
  },

  async openChannel(id, routeOpts?: RouteWriteOptions) {
    const [detail, participants] = await Promise.all([
      api.channel(id),
      api.participants(id, ""),
    ]);
    set({
      view: "channel",
      activeChannel: id,
      activeDirect: null,
      activeThread: null,
      activeAgentSpace: null,
      activeAgentID: null,
      activeAnchor: null,
      detail,
      threadDetail: null,
      expandedTaskID: null,
      participants,
    });
    set((cur) => pruneVisibleStreamingState(cur));
    writeWebRoute({ view: "channel", id }, routeOpts);
  },

  async createChannel(name) {
    const item = await api.createChannel(name);
    const channels = await api.channels();
    set({ channels });
    await get().openChannel(item.id);
    return item;
  },

  async setChannelAgentMode(channelID, personaID, mode) {
    await api.setChannelAgentMode(channelID, personaID, mode);
    if (get().activeChannel === channelID) {
      await refetchActiveChannelMeta(get, set);
      return;
    }
    const channels = await api.channels();
    set({ channels });
  },

  async addAgentToChannel(channelID, personaID) {
    await api.addAgentToChannel(channelID, personaID);
    if (get().activeChannel === channelID) {
      await refetchActiveChannelMeta(get, set);
      return;
    }
    const channels = await api.channels();
    set({ channels });
  },

  async setThreadAgentMode(spaceID, parentMessageID, personaID, mode) {
    await api.setThreadAgentMode(spaceID, parentMessageID, personaID, mode);
    const s = get();
    if (s.threadDetail && s.threadDetail.space_id === spaceID && s.threadDetail.parent_id === parentMessageID) {
      const td = await api.threadDetail(spaceID, parentMessageID);
      set({ threadDetail: td });
    }
  },

  async openThread(id, routeOpts?: RouteWriteOptions) {
    const spaceId = get().activeChannel;
    if (!spaceId) return;
    const detail = await api.threadDetail(spaceId, id);
    set({
      activeThread: id,
      activeAnchor: null,
      threadDetail: detail,
    });
    set((cur) => pruneVisibleStreamingState(cur));
    writeWebRoute({ view: "channel", id: spaceId, thread: id }, routeOpts);
  },

  closeThread(routeOpts?: RouteWriteOptions) {
    const spaceId = get().activeChannel;
    set({ activeThread: null, activeAnchor: null, threadDetail: null, expandedTaskID: null });
    if (spaceId) writeWebRoute({ view: "channel", id: spaceId }, routeOpts);
  },

  openAgentDetail(id, routeOpts?: RouteWriteOptions) {
    const agentID = id.trim();
    if (!agentID || !get().agents.some((a) => a.id === agentID)) return;
    set({
      view: "agent_detail",
      activeAgentID: agentID,
      activeAgentSpace: null,
      activeChannel: null,
      activeDirect: null,
      activeThread: null,
      activeAnchor: null,
      detail: null,
      threadDetail: null,
      participants: null,
      expandedTaskID: null,
    });
    writeWebRoute({ view: "agent", id: "detail:" + agentID }, routeOpts);
  },

  openAgentsPanel(routeOpts?: RouteWriteOptions) {
    set({
      view: "agents",
      activeChannel: null,
      activeDirect: null,
      activeThread: null,
      activeAgentSpace: null,
      activeAgentID: null,
      activeAnchor: null,
      detail: null,
      threadDetail: null,
      participants: null,
      expandedTaskID: null,
    });
    writeWebRoute({ view: "agents" }, routeOpts);
  },

  async openAgent(id, routeOpts?: RouteWriteOptions) {
    const dmPersona = get().agentDMs.find((d) => d.id === id)?.persona_id || id;
    const ag = get().agents.find((a) => a.id === dmPersona);
    let detail: SessionDetail;
    try {
      detail = await api.agentDM(id);
    } catch {
      detail = {
        item: {
          id: "desktop:agent:" + id,
          title: ag?.display || id,
          updated_at: new Date().toISOString(),
        },
        summary: ag?.role || "",
        messages: [],
      };
    }
    if (!detail.item?.id) {
      const hint = "That conversation was deleted. Opened Sumi Home instead.";
      const nav = await fetchNavigationSnapshot().catch(() => ({
        channels: get().channels,
        threads: get().threads,
        directChats: get().directChats,
        agentDMs: get().agentDMs,
        recent: get().recent,
      }));
      set({
        ...nav,
        view: "home",
        detail: null,
        threadDetail: null,
        participants: null,
        activeChannel: null,
        activeDirect: null,
        activeThread: null,
        activeAgentSpace: null,
        activeAgentID: null,
        activeAnchor: null,
        expandedTaskID: null,
        routeNotice: { text: hint, at: Date.now() },
      });
      writeWebRoute({ view: "home" }, routeOpts);
      return;
    }
    if (!detail.item.title) detail.item.title = ag?.display || id;
    if (!detail.summary && ag?.role) detail.summary = ag.role;
    const [agentDMs, directChats] = await Promise.all([
      api.agentDMs().catch(() => get().agentDMs),
      api.directChats().catch(() => get().directChats),
    ]);
    set({
      view: "agent",
      activeAgentSpace: detail.item.id || id,
      activeAgentID: null,
      activeChannel: null,
      activeDirect: null,
      activeThread: null,
      activeAnchor: null,
      detail,
      threadDetail: null,
      participants: null,
      agentDMs,
      directChats,
      expandedTaskID: null,
    });
    set((cur) => pruneVisibleStreamingState(cur));
    writeWebRoute({ view: "agent", id: detail.item.id || id }, routeOpts);
  },

  async newAgentChat(personaID, title) {
    const trimmed = title?.trim() || "";
    if (!trimmed) {
      const existing = get().agentDMs.find((d) => d.persona_id === personaID);
      if (existing) {
        await get().openAgent(existing.id);
        return;
      }
      const fallback = get().directChats.find((d) => d.kind === "agent_dm" && d.persona_id === personaID);
      if (fallback) {
        await get().openAgent(fallback.id);
        return;
      }
    }
    const item = await api.createAgentDM(personaID, trimmed);
    const [agentDMs, directChats] = await Promise.all([api.agentDMs(), api.directChats()]);
    set({ agentDMs, directChats });
    await get().openAgent(item.id);
  },

  async updateAgentChatTitle(id, title) {
    const item = await api.updateAgentDMTitle(id, title);
    const [agentDMs, directChats] = await Promise.all([api.agentDMs(), api.directChats()]);
    const detail = get().detail;
    set({
      agentDMs,
      directChats,
      detail: detail && detail.item.id === id ? { ...detail, item: { ...detail.item, title: item.title } } : detail,
    });
  },

  openTaskBoard(routeOpts?: RouteWriteOptions) {
    set({
      view: "tasks",
      activeDirect: null,
      activeThread: null,
      activeAgentSpace: null,
      activeAgentID: null,
      activeAnchor: null,
      detail: null,
      threadDetail: null,
      participants: null,
      expandedTaskID: null,
    });
    writeWebRoute({ view: "tasks" }, routeOpts);
  },

  async newDirectChat(title, agentID) {
    const detail = await api.newDirect(title, agentID);
    if (!detail.item?.id) throw new Error("Direct chat was not created");
    let participants: ParticipantsView | null = null;
    try {
      participants = await api.participants(detail.item.id, "");
    } catch {
      participants = null;
    }
    set({
      view: "direct",
      activeDirect: detail.item.id,
      activeThread: null,
      activeChannel: null,
      activeAgentSpace: null,
      activeAgentID: null,
      activeAnchor: null,
      detail,
      participants,
    });
    set((cur) => pruneVisibleStreamingState(cur));
    writeWebRoute({ view: "direct", id: detail.item.id });
    try {
      const [directChats, recent] = await Promise.all([api.directChats(), api.recent()]);
      set({ directChats, recent });
    } catch {
    }
  },

  async updateDirectChatTitle(id, title) {
    const item = await api.updateDirectChatTitle(id, title);
    const [directChats] = await Promise.all([api.directChats()]);
    const detail = get().detail;
    set({
      directChats,
      detail: detail && detail.item.id === id ? { ...detail, item: { ...detail.item, title: item.title } } : detail,
    });
  },

  async openDirectChat(id, routeOpts?: RouteWriteOptions) {
    const direct = get().directChats.find((d) => d.id === id);
    if (direct?.kind === "agent_dm") {
      await get().openAgent(direct.id || direct.persona_id || id, routeOpts);
      return;
    }
    let detail: SessionDetail;
    let participants: ParticipantsView | null = null;
    try {
      const [d, p] = await Promise.all([
        api.directChat(id),
        api.participants("", id).catch(() => null),
      ]);
      detail = d;
      participants = p;
    } catch {
      return;
    }
    if (!detail.item?.id) return;
    if (!participants) {
      participants = { agents: [] };
    }
    set({
      view: "direct",
      activeDirect: id,
      activeThread: null,
      activeChannel: null,
      activeAgentSpace: null,
      activeAgentID: null,
      activeAnchor: null,
      detail,
      participants,
    });
    writeWebRoute({ view: "direct", id }, routeOpts);
  },

  async deleteConversation(input) {
    const kind = input.kind;
    const id = input.id;
    const parentMessageID = input.parentMessageID;
    if (!id) return;
    await api.deleteConversation({
      kind,
      id,
      parent_message_id: parentMessageID,
    });
    const [nav, capabilities] = await Promise.all([
      fetchNavigationSnapshot(),
      api.capabilities().catch(() => get().capabilities || { skills: [], tasks: [], action_proposals: [] }),
    ]);
    set({
      ...nav,
      capabilities,
      sending: false,
      sendingByScope: {},
      streaming: null,
      streamingByID: {},
    });
    const openHome = async () => {
      set({
        view: "home",
        detail: null,
        threadDetail: null,
        participants: null,
        activeChannel: null,
        activeDirect: null,
        activeThread: null,
        activeAgentSpace: null,
        activeAgentID: null,
        activeAnchor: null,
        expandedTaskID: null,
      });
      writeWebRoute({ view: "home" }, { replace: true });
    };
    if (kind === "thread") {
      if (get().threadDetail?.space_id === id && get().threadDetail?.parent_id === parentMessageID) {
        await openHome();
        return;
      }
      if (get().view === "channel" && get().activeChannel === id) {
        await refetchActiveScope(get, set);
      }
      return;
    }
    const s = get();
    const activeDeleted =
      (kind === "channel" && s.view === "channel" && (s.activeChannel === id || s.detail?.item.id === id)) ||
      (kind === "direct_chat" && s.view === "direct" && (s.activeDirect === id || s.detail?.item.id === id)) ||
      (kind === "agent_dm" && s.view === "agent" && (s.activeAgentSpace === id || s.detail?.item.id === id));
    if (!activeDeleted) return;
    await openHome();
  },

  async openCurrentRoute() {
    const route = parseWebRoute();
    if (!route) return;
    if (route.view === "home") {
      set({
        view: "home",
        detail: null,
        threadDetail: null,
        participants: null,
        activeChannel: null,
        activeDirect: null,
        activeThread: null,
        activeAgentSpace: null,
        activeAgentID: null,
        activeAnchor: null,
        expandedTaskID: null,
      });
      writeWebRoute({ view: "home" }, { replace: true });
      return;
    }
    if (route.view === "channel") {
      await get().openChannel(route.id, { replace: true });
      if (route.thread) await get().openThread(route.thread, { replace: true });
      set(applyRouteAnchor(route.anchor));
      if (route.anchor) writeRouteAnchor(route.anchor, { replace: true });
      return;
    }
    if (route.view === "direct") {
      await get().openDirectChat(route.id, { replace: true });
      set(applyRouteAnchor(route.anchor));
      if (route.anchor) writeRouteAnchor(route.anchor, { replace: true });
      return;
    }
    if (route.view === "agents") {
      get().openAgentsPanel({ replace: true });
      return;
    }
    if (route.view === "agent") {
      if (route.id.startsWith("detail:")) {
        get().openAgentDetail(route.id.slice("detail:".length), { replace: true });
        return;
      }
      await get().openAgent(route.id, { replace: true });
      set(applyRouteAnchor(route.anchor));
      if (route.anchor) writeRouteAnchor(route.anchor, { replace: true });
      return;
    }
    if (route.view === "tasks") {
      get().openTaskBoard({ replace: true });
      set(applyRouteAnchor(route.anchor));
      if (route.anchor) writeRouteAnchor(route.anchor, { replace: true });
    }
  },

  setPalette(open) {
    set({ paletteOpen: open });
  },

  setQuickCreate(open) {
    set({ quickCreateOpen: open });
  },

  expandTaskInRail(taskID) {
    set({ expandedTaskID: taskID, activeAnchor: "task:" + taskID });
    writeRouteAnchor("task:" + taskID);
  },

  focusTaskOrigin(taskID, messageID) {
    const anchor = messageID ? "message:" + messageID : "task:" + taskID;
    set({ expandedTaskID: taskID, activeAnchor: anchor });
    writeRouteAnchor(anchor);
  },

  collapseTaskInRail() {
    set({ expandedTaskID: null, activeAnchor: null });
    writeRouteAnchor(null);
  },

  async send(input, personaID, options) {
    const before = get();
    const sid = activeSessionID(before);
    const parentMessageID =
      options?.parentMessageID === null
        ? undefined
        : options?.parentMessageID ?? before.threadDetail?.parent_id;
    const sendScope = options?.scopeKey || (sid ? scopeKey(sid, parentMessageID) : activeScopeKey(before));
    const hasPayload = input.trim() || (options?.attachments?.length ?? 0) > 0;
    if (!sid || !sendScope || !hasPayload || before.sendingByScope[sendScope]) return;
    set({
      sending: true,
      sendingByScope: { ...before.sendingByScope, [sendScope]: true },
    });
    const clearSending = () => {
      const current = get().sendingByScope;
      const next = { ...current };
      delete next[sendScope];
      set({ sendingByScope: next, sending: Object.keys(next).length > 0 });
    };
    try {
      await api.send(sid, input, personaID, parentMessageID, options?.attachments);
      clearSending();
      if (activeSessionID(get()) === sid) {
        await refetchActiveScope(get, set);
      }
      void refetchNavigation(set);
    } catch (err) {
      const errMsg = err instanceof Error ? err.message : "Desktop backend is offline.";
      clearSending();
      set({
        composerHint: { text: "Send failed: " + errMsg, at: Date.now() },
      });
      if (activeSessionID(get()) === sid) {
        await refetchActiveScope(get, set);
      }
    }
  },

  async retryMessage(spaceID, messageID) {
    const before = get();
    const parentMessageID =
      before.threadDetail?.replies.find((m) => m.id === messageID)?.thread_id ||
      before.detail?.messages.find((m) => m.id === messageID)?.thread_id ||
      "";
    const retryScope = scopeKey(spaceID, parentMessageID);
    if (!spaceID || !messageID || before.sendingByScope[retryScope]) return;
    set({
      sending: true,
      sendingByScope: { ...before.sendingByScope, [retryScope]: true },
    });
    const clearSending = () => {
      const current = get().sendingByScope;
      const next = { ...current };
      delete next[retryScope];
      set({ sendingByScope: next, sending: Object.keys(next).length > 0 });
    };
    try {
      await api.retryMessage(spaceID, messageID);
      clearSending();
      await refetchActiveScope(get, set);
      void refetchNavigation(set);
    } catch (err) {
      clearSending();
      const errMsg = err instanceof Error ? err.message : "Retry failed.";
      set({ composerHint: { text: "Retry failed: " + errMsg, at: Date.now() } });
      await refetchActiveScope(get, set);
    }
  },

  async stop() {
    const sid = activeSessionID(get());
    if (!sid) return;
    try {
      await api.stop(sid);
    } catch {
          }
    const detail = get().detail;
    set({
      sending: false,
      sendingByScope: {},
      streaming: null,
      streamingByID: {},
      detail,
    });
    await refetchActiveScope(get, set);
    void refetchNavigation(set);
  },

  connectStream() {
    const wails = (window as any).runtime;
    if (wails && typeof wails.EventsOn === "function") {
      set({ connectionStatus: "online", connectionMessage: "" });
      const off = wails.EventsOn("bus", (ev: BusEvent) => {
        get().applyEvent(ev);
      });
      return () => {
        if (typeof off === "function") off();
      };
    }
    set({ connectionStatus: "connecting", connectionMessage: "Connecting to desktop backend..." });
    const src = new EventSource("/api/events");
    src.onopen = () => {
      set({ connectionStatus: "online", connectionMessage: "" });
    };
    src.onerror = () => {
      const message = "Agent reply was interrupted because the desktop backend disconnected. Retry to run this message again.";
      const cur = get();
      set({
        ...interruptLocalStreams(cur, message),
        connectionStatus: "offline",
        connectionMessage: "Desktop backend disconnected. Agent replies will not run until it reconnects.",
      });
      void refetchActiveScope(get, set);
      void refetchNavigation(set);
    };
    const onMessage = (e: MessageEvent) => {
      try {
        const ev = JSON.parse(e.data) as BusEvent;
        get().applyEvent(ev);
      } catch {
              }
    };
    src.addEventListener("bus", onMessage);
    return () => {
      src.removeEventListener("bus", onMessage);
      src.close();
    };
  },

  applyEvent(ev) {
    const cur = get();

    if (ev.type === "space.title.changed") {
      const tasks: Promise<unknown>[] = [
        api.agentDMs().then((agentDMs) => set({ agentDMs })).catch(() => undefined),
        refetchNavigation(set),
      ];
      if (cur.detail && cur.detail.item.id === ev.space_id) {
        tasks.push(refetchActiveScope(get, set));
      }
      void Promise.all(tasks);
      return;
    }

    if (
      ev.type === "space.created" ||
      ev.type === "space.updated" ||
      ev.type === "space.message.appended"
    ) {
      void refetchNavigation(set);
      if (lifecycleEventInScope(ev, cur)) void refetchActiveScope(get, set);
      return;
    }

    if (ev.type === "model.changed") {
      void Promise.all([
        api.state().then((state) => set({ state })).catch(() => undefined),
        api.models().then((models) => set({ models })).catch(() => undefined),
      ]);
      return;
    }

    if (
      ev.type === "task.created" ||
      ev.type === "task.updated" ||
      ev.type === "run.started" ||
      ev.type === "run.finished"
    ) {
      void get().refreshCapabilities();
      if (lifecycleEventInScope(ev, cur)) void refetchActiveScope(get, set);
      return;
    }

    if (
      ev.type === "action.proposal" ||
      ev.type === "skill.listed" ||
      ev.type === "skill.described" ||
      ev.type === "skill.used"
    ) {
      void get().refreshCapabilities();
      return;
    }

    if (ev.type === "routing.listening_ambiguous") {
      set({ composerHint: { text: "Mention a specific agent.", at: Date.now() } });
      if (lifecycleEventInScope(ev, cur)) void refetchActiveScope(get, set);
      return;
    }
    if (ev.type === "routing.listening_no_match") {
      set({ composerHint: { text: "No listening agent matched this. Mention one explicitly.", at: Date.now() } });
      if (lifecycleEventInScope(ev, cur)) void refetchActiveScope(get, set);
      return;
    }
    if (ev.type === "routing.channel.no_target") {
      set({ composerHint: { text: "No agent picked this up. Mention an agent or enable listening.", at: Date.now() } });
      if (lifecycleEventInScope(ev, cur)) void refetchActiveScope(get, set);
      return;
    }
    if (
      ev.type === "routing.budget_exhausted" ||
      ev.type === "routing.duplicate_skipped" ||
      ev.type === "routing.unknown_mention"
    ) {
      if (lifecycleEventInScope(ev, cur)) void refetchActiveScope(get, set);
      return;
    }

    const isStreamEvent =
      ev.type === "turn.started" ||
      ev.type === "turn.chunk" ||
      ev.type === "turn.reasoning" ||
      ev.type === "turn.finished" ||
      ev.type === "turn.error" ||
      ev.type === "tool.call.started" ||
      ev.type === "tool.call.finished" ||
      ev.type === "tool.call.failed";

    if (isStreamEvent) {
      if (!ev.stream_id) {
                                return;
      }
    }

            if (!isStreamEvent && ev.source && ev.source.startsWith("subtask:")) return;
    if (!isStreamEvent && !cur.detail) return;

    switch (ev.type) {
      case "turn.queued": {
        if (lifecycleEventInScope(ev, cur)) {
          void refetchActiveScope(get, set);
        }
        return;
      }
      case "turn.started": {
        if (lifecycleEventInScope(ev, cur)) {
          void refetchActiveChannelMeta(get, set);
          void refetchNavigation(set);
        }
        const author = ev.agent_id;
        if (!author) {
                              return;
        }
        if (!ev.stream_id) {
                              return;
        }
        const personaInfo =
          cur.personas.find((p) => p.id === author) ||
          cur.agents.find((a) => a.id === author);
        const display =
          (personaInfo as { display?: string } | undefined)?.display || author;
        const placeholder: MessageView = {
          id: ev.message_id || "a-" + newID(),
          role: "agent",
          author_id: author,
          author_name: display,
          content: "",
          reasoning: "",
          status: "pending",
          time: ev.time,
          events: [],
          thread_id: ev.parent_message_id || undefined,
          is_thread_reply: !!ev.parent_message_id,
        };
        const stream: StreamingTurn = {
          messageID: placeholder.id,
          streamID: ev.stream_id,
          agentID: author,
          spaceID: ev.space_id || "",
          parentMessageID: ev.parent_message_id || "",
          startedAt: ev.time,
          lastEventAt: eventTimeMs(ev.time),
          content: "",
          reasoning: "",
          toolCalls: new Map(),
        };
        const updates = streamingViewUpdates(cur, stream, placeholder);
        set({
          ...updateStream(cur, stream),
          ...updates,
        });
        return;
      }
      case "turn.chunk": {
        const stream = streamForEvent(cur, ev);
        if (!stream) return;
        const next = stream.content + (ev.text || "");
        const updated = { ...stream, content: next, lastEventAt: eventTimeMs(ev.time) };
        set({
          ...updateStream(cur, updated),
          ...streamingMessageUpdates(cur, updated, (m) => ({
            ...m,
            content: next,
          })),
        });
        return;
      }
      case "turn.reasoning": {
        const stream = streamForEvent(cur, ev);
        if (!stream) return;
        const next = stream.reasoning + (ev.text || "");
        const updated = { ...stream, reasoning: next, lastEventAt: eventTimeMs(ev.time) };
        set({
          ...updateStream(cur, updated),
          ...streamingMessageUpdates(cur, updated, (m) => ({
            ...m,
            reasoning: next,
          })),
        });
        return;
      }
      case "tool.call.started": {
        const stream = streamForEvent(cur, ev);
        if (!stream) return;
        const id = ev.tool_call_id || "tc-" + newID();
        const block: EventBlock = {
          kind: "tool_call",
          tool_name: ev.tool,
          args: ev.input,
          status: "running",
          time: ev.time,
        };
        const toolCalls = new Map(stream.toolCalls);
        toolCalls.set(id, block);
        const updated = { ...stream, toolCalls, lastEventAt: eventTimeMs(ev.time) };
        set({
          ...updateStream(cur, updated),
          ...streamingMessageUpdates(cur, updated, (m) => ({
            ...m,
            events: Array.from(toolCalls.values()),
          })),
        });
        return;
      }
      case "tool.call.finished":
      case "tool.call.failed": {
        const stream = streamForEvent(cur, ev);
        if (!stream) return;
        const id = ev.tool_call_id || "";
        const prev = stream.toolCalls.get(id);
        const failed = ev.type === "tool.call.failed";
        const block: EventBlock = {
          kind: "tool_call",
          tool_name: prev?.tool_name || ev.tool,
          args: prev?.args || ev.input,
          output: ev.output,
          err: ev.err,
          status: failed ? "error" : "done",
          duration_ms: 0,
          time: ev.time,
        };
        const toolCalls = new Map(stream.toolCalls);
        toolCalls.set(id, block);
        const updated = { ...stream, toolCalls, lastEventAt: eventTimeMs(ev.time) };
        set({
          ...updateStream(cur, updated),
          ...streamingMessageUpdates(cur, updated, (m) => ({
            ...m,
            events: Array.from(toolCalls.values()),
          })),
        });
        return;
      }
      case "agent.mention": {
        const stream = streamForEvent(cur, ev);
        if (!stream) return;
        const id = ev.tool_call_id || "mn-" + newID();
        const block: EventBlock = {
          kind: "mention",
          status: "running",
          time: ev.time,
          agent_id: ev.tool,
          agent_display: prettyAgentName(ev.tool, cur.agents),
        };
        const toolCalls = new Map(stream.toolCalls);
        toolCalls.set(id, block);
        const updated = { ...stream, toolCalls, lastEventAt: eventTimeMs(ev.time) };
        set({
          ...updateStream(cur, updated),
          ...streamingMessageUpdates(cur, updated, (m) => ({
            ...m,
            events: Array.from(toolCalls.values()),
          })),
        });
        return;
      }
      case "agent.mention.reply": {
        const stream = streamForEvent(cur, ev);
        if (!stream) return;
        const id = ev.tool_call_id || "";
        const prev = stream.toolCalls.get(id);
        const block: EventBlock = {
          kind: "mention",
          status: "done",
          time: ev.time,
          agent_id: prev?.agent_id || ev.tool,
          agent_display: prev?.agent_display || prettyAgentName(ev.tool, cur.agents),
          reply: ev.output,
        };
        const toolCalls = new Map(stream.toolCalls);
        toolCalls.set(id, block);
        const updated = { ...stream, toolCalls, lastEventAt: eventTimeMs(ev.time) };
        set({
          ...updateStream(cur, updated),
          ...streamingMessageUpdates(cur, updated, (m) => ({
            ...m,
            events: Array.from(toolCalls.values()),
          })),
        });
        return;
      }
      case "agent.delegate.started": {
        const stream = streamForEvent(cur, ev);
        if (!stream) return;
        const id = ev.tool_call_id || "dg-" + newID();
        const block: EventBlock = {
          kind: "delegate",
          status: "pending",
          time: ev.time,
          agent_id: ev.tool,
          agent_display: prettyAgentName(ev.tool, cur.agents),
          task: ev.input,
          task_id: ev.task_id,
        };
        const toolCalls = new Map(stream.toolCalls);
        toolCalls.set(id, block);
        const updated = { ...stream, toolCalls, lastEventAt: eventTimeMs(ev.time) };
        set({
          ...updateStream(cur, updated),
          ...streamingMessageUpdates(cur, updated, (m) => ({
            ...m,
            events: Array.from(toolCalls.values()),
          })),
        });
        return;
      }
      case "agent.delegate.progress": {
        const stream = streamForEvent(cur, ev);
        if (!stream) return;
        const id = ev.tool_call_id || "";
        const prev = stream.toolCalls.get(id);
        if (!prev) return;
        const toolCalls = new Map(stream.toolCalls);
        toolCalls.set(id, {
          ...prev,
          status: "running",
          time: ev.time,
          task_id: ev.task_id || prev.task_id,
        });
        const updated = { ...stream, toolCalls };
        set({
          ...updateStream(cur, updated),
          ...streamingMessageUpdates(cur, updated, (m) => ({
            ...m,
            events: Array.from(toolCalls.values()),
          })),
        });
        return;
      }
      case "agent.delegate.finished":
      case "agent.delegate.failed":
      case "agent.delegate.canceled": {
        if (lifecycleEventInScope(ev, cur)) {
          void refetchActiveScope(get, set);
        }
        const stream = streamForEvent(cur, ev);
        if (!stream) return;
        const id = ev.tool_call_id || "";
        const prev = stream.toolCalls.get(id);
        const failed = ev.type === "agent.delegate.failed" || ev.type === "agent.delegate.canceled";
        const block: EventBlock = {
          kind: "delegate",
          status: failed ? "error" : "done",
          time: ev.time,
          agent_id: prev?.agent_id || ev.tool,
          agent_display: prev?.agent_display || prettyAgentName(ev.tool, cur.agents),
          task: prev?.task,
          task_id: ev.task_id || prev?.task_id,
          output: ev.output,
          err: ev.err,
        };
        const toolCalls = new Map(stream.toolCalls);
        toolCalls.set(id, block);
        const updated = { ...stream, toolCalls };
        set({
          ...updateStream(cur, updated),
          ...streamingMessageUpdates(cur, updated, (m) => ({
            ...m,
            events: Array.from(toolCalls.values()),
          })),
        });
        return;
      }
      case "service.notice": {
        const stream = streamForEvent(cur, ev);
        if (!stream) return;
        const id = "n-" + newID();
        const toolCalls = new Map(stream.toolCalls);
        toolCalls.set(id, {
          kind: "service_notice",
          output: ev.text,
          status: "done",
          time: ev.time,
        });
        const updated = { ...stream, toolCalls };
        set({
          ...updateStream(cur, updated),
          ...streamingMessageUpdates(cur, updated, (m) => ({
            ...m,
            events: Array.from(toolCalls.values()),
          })),
        });
        return;
      }
      case "turn.finished": {
        if (lifecycleEventInScope(ev, cur)) {
          void refetchActiveScope(get, set);
        }
        const stream = streamForEvent(cur, ev);
        if (!stream || (ev.stream_id && ev.stream_id !== stream.streamID)) return;
        const placeholderID = stream.messageID;
        const streamState = removeStream(cur, stream.streamID);
        const updates: Partial<State> = {
          ...streamState,
        };
        if (cur.detail && streamMatchesMainScope(cur, stream)) {
          updates.detail = {
            ...cur.detail,
            messages: cur.detail.messages.filter((m) => m.id !== placeholderID),
          };
        }
        if (cur.threadDetail && streamMatchesThreadScope(cur, stream)) {
          updates.threadDetail = {
            ...cur.threadDetail,
            replies: cur.threadDetail.replies.filter((m) => m.id !== placeholderID),
          };
        }
        set(updates);
        void refetchNavigation(set);
        return;
      }
      case "turn.error": {
        if (lifecycleEventInScope(ev, cur)) {
          void refetchActiveScope(get, set);
        }
        const stream = streamForEvent(cur, ev);
        if (!stream || (ev.stream_id && ev.stream_id !== stream.streamID)) return;
        const errMsg = ev.err || "Turn failed";
        const errorPatch = (m: MessageView): MessageView => ({
          ...m,
          content: m.content || "",
          events: [
            ...(m.events || []),
            {
              kind: "service_notice",
              status: "error",
              output: errMsg,
              time: ev.time,
            },
          ],
        });
        const streamState = removeStream(cur, stream.streamID);
        const updates: Partial<State> = {
          ...streamState,
        };
        Object.assign(updates, streamingMessageUpdates(cur, stream, errorPatch));
        set(updates);
        void refetchNavigation(set);
        return;
      }
    }
  },
}));

function prettyAgentName(id: string | undefined, agents: AgentItem[]): string {
  if (!id || !id.trim()) return "Unknown agent";
  const a = agents.find((x) => x.id === id);
  if (a) return a.display;
  return "Unknown agent (" + id + ")";
}
