import { create } from "zustand";
import type {
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
  activeThread: string | null;
  activeAgent: string | null;
  activeAnchor: string | null;
  expandedTaskID: string | null;

  paletteOpen: boolean;
  quickCreateOpen: boolean;
  composerHint: { text: string; at: number } | null;
  sending: boolean;
  streaming: StreamingTurn | null;
  streamingByID: Record<string, StreamingTurn>;

  loadInitial: () => Promise<void>;
  refreshCapabilities: () => Promise<void>;
  openChannel: (id: string, routeOpts?: RouteWriteOptions) => Promise<void>;
  createChannel: (name: string) => Promise<ChannelItem>;
  setChannelAgentMode: (channelID: string, personaID: string, mode: string) => Promise<void>;
  addAgentToChannel: (channelID: string, personaID: string) => Promise<void>;
  setThreadAgentMode: (spaceID: string, parentMessageID: string, personaID: string, mode: string) => Promise<void>;
  openThread: (id: string, routeOpts?: RouteWriteOptions) => Promise<void>;
  closeThread: (routeOpts?: RouteWriteOptions) => void;
  openAgent: (id: string, routeOpts?: RouteWriteOptions) => Promise<void>;
  newAgentChat: (personaID: string, title?: string) => Promise<void>;
  updateAgentChatTitle: (id: string, title: string) => Promise<void>;
  openDirectChat: (id: string, routeOpts?: RouteWriteOptions) => Promise<void>;
  newDirectChat: () => Promise<void>;
  setPalette: (open: boolean) => void;
  setQuickCreate: (open: boolean) => void;
  send: (input: string, personaID?: string) => Promise<void>;
  stop: () => Promise<void>;
  expandTaskInRail: (taskID: string) => void;
  collapseTaskInRail: () => void;
  connectStream: () => () => void;
  applyEvent: (ev: BusEvent) => void;
  openCurrentRoute: () => Promise<void>;
}

function activeSessionID(s: State): string {
  if (s.view === "thread" && s.activeThread) return s.activeThread;
  if (s.view === "agent" && s.activeAgent) return "desktop:agent:" + s.activeAgent;
  return s.activeChannel || "";
}

function newID(): string {
  return Math.random().toString(36).slice(2, 10);
}

function streamingEventInScope(ev: BusEvent, s: State): boolean {
  if (s.threadDetail && !s.threadDetail.unsupported && !s.threadDetail.not_found) {
    if (!ev.space_id || ev.space_id !== s.activeChannel) return false;
    return ev.parent_message_id === s.threadDetail.parent_id;
  }
  if (s.view === "agent") {
    if (!s.detail) return false;
    return ev.space_id === s.detail.item.id;
  }
  if (s.view === "channel" || s.view === "thread") {
    if (!ev.space_id || ev.space_id !== s.activeChannel) return false;
    return !ev.parent_message_id;
  }
  return false;
}

function lifecycleEventInScope(ev: BusEvent, s: State): boolean {
  if (!ev.space_id) return false;
  if (s.threadDetail && !s.threadDetail.unsupported && !s.threadDetail.not_found) {
    if (ev.space_id !== s.activeChannel) return false;
    if (ev.parent_message_id === s.threadDetail.parent_id) return true;
    if (ev.message_id === s.threadDetail.parent_id) return true;
    return s.threadDetail.replies.some((r) => r.id === ev.parent_message_id || r.id === ev.message_id);
  }
  if (s.view === "agent") {
    if (!s.detail) return false;
    return ev.space_id === s.detail.item.id;
  }
  if (s.view === "channel" || s.view === "thread") {
    if (ev.space_id !== s.activeChannel) return false;
    return !ev.parent_message_id;
  }
  return false;
}

function streamingViewUpdates(
  s: State,
  placeholder: MessageView,
  forThreadView: boolean,
): Partial<State> {
  const detail = s.detail;
  const updates: Partial<State> = {};
  if (detail) {
    updates.detail = {
      ...detail,
      messages: forThreadView ? detail.messages : [...detail.messages, placeholder],
    };
  }
  if (s.threadDetail) {
    updates.threadDetail = {
      ...s.threadDetail,
      replies: [...s.threadDetail.replies, placeholder],
    };
  }
  return updates;
}

function streamingMessageUpdates(
  s: State,
  messageID: string,
  patch: (m: MessageView) => MessageView,
): Partial<State> {
  const updates: Partial<State> = {};
  if (s.detail) {
    updates.detail = {
      ...s.detail,
      messages: s.detail.messages.map((m) => (m.id === messageID ? patch(m) : m)),
    };
  }
  if (s.threadDetail) {
    updates.threadDetail = {
      ...s.threadDetail,
      replies: s.threadDetail.replies.map((m) => (m.id === messageID ? patch(m) : m)),
    };
  }
  return updates;
}

function currentStreaming(streamingByID: Record<string, StreamingTurn>): StreamingTurn | null {
  return Object.values(streamingByID)[0] || null;
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
  if (ev.stream_id && s.streamingByID[ev.stream_id]) return s.streamingByID[ev.stream_id];
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

async function refetchActiveScope(
  get: () => State,
  set: (partial: Partial<State>) => void,
): Promise<void> {
  const s = get();
  try {
    if (s.threadDetail && !s.threadDetail.unsupported && !s.threadDetail.not_found) {
      const td = await api.threadDetail(s.threadDetail.space_id, s.threadDetail.parent_id);
      set({ threadDetail: td });
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
    if (s.view === "thread" && s.activeThread) {
      const detail = await api.directChat(s.activeThread);
      set({ detail });
      return;
    }
    if (s.view === "agent" && s.activeAgent) {
      const detail = await api.agentDM(s.activeAgent);
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
  activeThread: null,
  activeAgent: null,
  activeAnchor: null,
  expandedTaskID: null,

  paletteOpen: false,
  quickCreateOpen: false,
  composerHint: null,
  sending: false,
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
          if (initialRoute.view === "agent") {
            await get().openAgent(initialRoute.id, { replace: true });
            set(applyRouteAnchor(initialRoute.anchor));
            if (initialRoute.anchor) writeRouteAnchor(initialRoute.anchor, { replace: true });
            return;
          }
        } catch {
          // Bad or stale deep links should not block the desktop from opening.
        }
      }
      const sumiDirect = directChats.find((d) => d.kind === "direct_chat" && d.title === "Sumi");
      if (sumiDirect) {
        await get().openDirectChat(sumiDirect.id, { replace: true });
      } else if (channels.length) {
        await get().openChannel(channels[0].id, { replace: true });
      }
    } catch (err) {
      set({
        ready: true,
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
      activeThread: null,
      activeAgent: null,
      activeAnchor: null,
      detail,
      threadDetail: null,
      expandedTaskID: null,
      participants,
      streaming: null,
      streamingByID: {},
    });
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
      streaming: null,
      streamingByID: {},
    });
    writeWebRoute({ view: "channel", id: spaceId, thread: id }, routeOpts);
  },

  closeThread(routeOpts?: RouteWriteOptions) {
    const spaceId = get().activeChannel;
    set({ activeThread: null, activeAnchor: null, threadDetail: null, streaming: null, streamingByID: {}, expandedTaskID: null });
    if (spaceId) writeWebRoute({ view: "channel", id: spaceId }, routeOpts);
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
          title: "@" + (ag?.display || id),
          updated_at: new Date().toISOString(),
        },
        summary: ag?.role || "",
        messages: [],
      };
    }
    if (!detail.item.title) detail.item.title = "@" + (ag?.display || id);
    if (!detail.summary && ag?.role) detail.summary = ag.role;
    const [agentDMs, directChats] = await Promise.all([
      api.agentDMs().catch(() => get().agentDMs),
      api.directChats().catch(() => get().directChats),
    ]);
    set({
      view: "agent",
      activeAgent: detail.item.id || id,
      activeChannel: null,
      activeThread: null,
      activeAnchor: null,
      detail,
      threadDetail: null,
      participants: null,
      agentDMs,
      directChats,
      streaming: null,
      streamingByID: {},
      expandedTaskID: null,
    });
    writeWebRoute({ view: "agent", id: detail.item.id || id }, routeOpts);
  },

  async newAgentChat(personaID, title) {
    const item = await api.createAgentDM(personaID, title);
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

  async newDirectChat() {
    let detail: SessionDetail;
    try {
      detail = await api.newDirect();
    } catch {
      return;
    }
    if (!detail.item?.id) return;
    set({
      view: "thread",
      activeThread: detail.item.id,
      activeChannel: null,
      activeAgent: null,
      activeAnchor: null,
      detail,
      participants: { agents: [] },
      streaming: null,
      streamingByID: {},
    });
    writeWebRoute({ view: "direct", id: detail.item.id });
    try {
      const [directChats, recent] = await Promise.all([api.directChats(), api.recent()]);
      set({ directChats, recent });
    } catch {
          }
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
      view: "thread",
      activeThread: id,
      activeChannel: null,
      activeAgent: null,
      activeAnchor: null,
      detail,
      participants,
      streaming: null,
      streamingByID: {},
    });
    writeWebRoute({ view: "direct", id }, routeOpts);
  },

  async openCurrentRoute() {
    const route = parseWebRoute();
    if (!route) return;
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
    if (route.view === "agent") {
      await get().openAgent(route.id, { replace: true });
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

  collapseTaskInRail() {
    set({ expandedTaskID: null, activeAnchor: null });
    writeRouteAnchor(null);
  },

  async send(input, personaID) {
    const sid = activeSessionID(get());
    if (!sid || !input.trim() || get().sending) return;
    const parentMessageID = get().threadDetail?.parent_id;
    set({ sending: true });
    try {
      await api.send(sid, input, personaID, parentMessageID);
      set({ sending: false });
      if (activeSessionID(get()) === sid) {
        await refetchActiveScope(get, set);
      }
      void refetchNavigation(set);
    } catch (err) {
      const errMsg = err instanceof Error ? err.message : "Desktop backend is offline.";
      set({
        sending: false,
        composerHint: { text: "Send failed: " + errMsg, at: Date.now() },
      });
      if (activeSessionID(get()) === sid) {
        await refetchActiveScope(get, set);
      }
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
      set({
        connectionStatus: "offline",
        connectionMessage: "Desktop backend disconnected. Agent replies will not run until it reconnects.",
      });
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

    const detail = cur.detail;
    if (!detail) return;

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
      if (!streamingEventInScope(ev, cur)) return;
    }

            if (!isStreamEvent && ev.source && ev.source.startsWith("subtask:")) return;

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
          id: "a-" + newID(),
          role: "agent",
          author_id: author,
          author_name: display,
          content: "",
          reasoning: "",
          time: ev.time,
          events: [],
          thread_id: ev.parent_message_id || undefined,
          is_thread_reply: !!ev.parent_message_id,
        };
        const inThreadView =
          !!cur.threadDetail &&
          !cur.threadDetail.unsupported &&
          !cur.threadDetail.not_found;
        const updates = streamingViewUpdates(cur, placeholder, inThreadView);
        const stream: StreamingTurn = {
          messageID: placeholder.id,
          streamID: ev.stream_id,
          agentID: author,
          spaceID: ev.space_id || "",
          parentMessageID: ev.parent_message_id || "",
          startedAt: ev.time,
          content: "",
          reasoning: "",
          toolCalls: new Map(),
        };
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
        const updated = { ...stream, content: next };
        set({
          ...updateStream(cur, updated),
          ...streamingMessageUpdates(cur, stream.messageID, (m) => ({
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
        const updated = { ...stream, reasoning: next };
        set({
          ...updateStream(cur, updated),
          ...streamingMessageUpdates(cur, stream.messageID, (m) => ({
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
          status: "running",
          time: ev.time,
        };
        const toolCalls = new Map(stream.toolCalls);
        toolCalls.set(id, block);
        const updated = { ...stream, toolCalls };
        set({
          ...updateStream(cur, updated),
          ...streamingMessageUpdates(cur, stream.messageID, (m) => ({
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
          status: failed ? "error" : "done",
          duration_ms: 0,
          time: ev.time,
        };
        const toolCalls = new Map(stream.toolCalls);
        toolCalls.set(id, block);
        const updated = { ...stream, toolCalls };
        set({
          ...updateStream(cur, updated),
          ...streamingMessageUpdates(cur, stream.messageID, (m) => ({
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
        const updated = { ...stream, toolCalls };
        set({
          ...updateStream(cur, updated),
          ...streamingMessageUpdates(cur, stream.messageID, (m) => ({
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
        const updated = { ...stream, toolCalls };
        set({
          ...updateStream(cur, updated),
          ...streamingMessageUpdates(cur, stream.messageID, (m) => ({
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
        const updated = { ...stream, toolCalls };
        set({
          ...updateStream(cur, updated),
          ...streamingMessageUpdates(cur, stream.messageID, (m) => ({
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
          ...streamingMessageUpdates(cur, stream.messageID, (m) => ({
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
          ...streamingMessageUpdates(cur, stream.messageID, (m) => ({
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
          ...streamingMessageUpdates(cur, stream.messageID, (m) => ({
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
        const detailNow = get().detail;
        const placeholderID = stream.messageID;
        const streamState = removeStream(cur, stream.streamID);
        const updates: Partial<State> = {
          sending: false,
          ...streamState,
        };
        if (detailNow) {
          updates.detail = {
            ...detailNow,
            messages: detailNow.messages.filter((m) => m.id !== placeholderID),
          };
        }
        const td = get().threadDetail;
        if (td) {
          updates.threadDetail = {
            ...td,
            replies: td.replies.filter((m) => m.id !== placeholderID),
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
        const placeholderID = stream.messageID;
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
          sending: false,
          ...streamState,
        };
        const detailNow = get().detail;
        if (detailNow) {
          updates.detail = {
            ...detailNow,
            messages: detailNow.messages.map((m) =>
              m.id === placeholderID ? errorPatch(m) : m,
            ),
          };
        }
        const td = get().threadDetail;
        if (td) {
          updates.threadDetail = {
            ...td,
            replies: td.replies.map((m) => (m.id === placeholderID ? errorPatch(m) : m)),
          };
        }
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
