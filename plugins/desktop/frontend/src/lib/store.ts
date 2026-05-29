import { create } from "zustand";
import type {
  AgentItem,
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

interface BusEvent {
  type: string;
  source?: string;
  session_id?: string;
  task_id?: string;
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
  state: WorkspaceState | null;
  channels: ChannelItem[];
  threads: ThreadItem[];
  directChats: DirectChatItem[];
  recent: RecentItem[];
  agents: AgentItem[];
  personas: PersonaItem[];
  models: ModelItem[];
  tools: ToolItem[];
  commands: CommandItem[];
  detail: SessionDetail | null;
  threadDetail: ThreadDetail | null;
  participants: ParticipantsView | null;

  view: ViewMode;
  activeChannel: string | null;
  activeThread: string | null;
  activeAgent: string | null;

  paletteOpen: boolean;
  sending: boolean;
  streaming: StreamingTurn | null;

  loadInitial: () => Promise<void>;
  openChannel: (id: string) => Promise<void>;
  openThread: (id: string) => Promise<void>;
  closeThread: () => void;
  openAgent: (id: string) => Promise<void>;
  openDirectChat: (id: string) => Promise<void>;
  newDirectChat: () => Promise<void>;
  setPalette: (open: boolean) => void;
  send: (input: string, personaID?: string) => Promise<void>;
  stop: () => Promise<void>;
  connectStream: () => () => void;
  applyEvent: (ev: BusEvent) => void;
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
    if (!ev.parent_message_id) return false;
    if (ev.parent_message_id === s.threadDetail.parent_id) return true;
    // Trigger may be a reply inside this thread.
    return s.threadDetail.replies.some((r) => r.id === ev.parent_message_id);
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
      item: { ...detail.item, running: true },
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
      const detail = await api.channel(s.activeChannel);
      set({ detail });
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
  } catch {
    // ignore: stale refetch is not fatal; the streaming convergence
    // will heal the next time the user touches the scope.
  }
}

export const useStore = create<State>((set, get) => ({
  ready: false,
  state: null,
  channels: [],
  threads: [],
  directChats: [],
  recent: [],
  agents: [],
  personas: [],
  models: [],
  tools: [],
  commands: [],
  detail: null,
  threadDetail: null,
  participants: null,

  view: "channel",
  activeChannel: null,
  activeThread: null,
  activeAgent: null,

  paletteOpen: false,
  sending: false,
  streaming: null,

  async loadInitial() {
    const [state, channels, threads, directChats, recent, agents, personas, models, tools, commands] = await Promise.all([
      api.state(),
      api.channels(),
      api.threads(),
      api.directChats(),
      api.recent(),
      api.agents(),
      api.personas(),
      api.models(),
      api.tools(),
      api.commands(),
    ]);
    set({ state, channels, threads, directChats, recent, agents, personas, models, tools, commands, ready: true });
    if (channels.length) {
      await get().openChannel(channels[0].id);
    }
  },

  async openChannel(id) {
    const detail = await api.channel(id);
    const participants = await api.participants(id, "");
    set({
      view: "channel",
      activeChannel: id,
      activeThread: null,
      activeAgent: null,
      detail,
      threadDetail: null,
      participants,
      streaming: null,
    });
  },

  async openThread(id) {
    const spaceId = get().activeChannel;
    if (!spaceId) return;
    const detail = await api.threadDetail(spaceId, id);
    set({
      activeThread: id,
      threadDetail: detail,
      streaming: null,
    });
  },

  closeThread() {
    set({ activeThread: null, threadDetail: null, streaming: null });
  },

  async openAgent(id) {
    const ag = get().agents.find((a) => a.id === id);
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
    set({
      view: "agent",
      activeAgent: id,
      activeChannel: null,
      activeThread: null,
      detail,
      threadDetail: null,
      participants: null,
      streaming: null,
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
      detail,
      participants: { agents: [] },
      streaming: null,
    });
    try {
      const [directChats, recent] = await Promise.all([api.directChats(), api.recent()]);
      set({ directChats, recent });
    } catch {
      // ignore refresh failure
    }
  },

  async openDirectChat(id) {
    let detail: SessionDetail;
    try {
      detail = await api.directChat(id);
    } catch {
      return;
    }
    if (!detail.item?.id) return;
    let participants: ParticipantsView | null = null;
    try {
      participants = await api.participants("", id);
    } catch {
      participants = { agents: [] };
    }
    set({
      view: "thread",
      activeThread: id,
      activeChannel: null,
      activeAgent: null,
      detail,
      participants,
      streaming: null,
    });
  },

  setPalette(open) {
    set({ paletteOpen: open });
  },

  async send(input, personaID) {
    const sid = activeSessionID(get());
    if (!sid || !input.trim() || get().sending) return;
    const detail = get().detail;
    if (!detail) return;
    const threadDetail = get().threadDetail;
    const parentMessageID = threadDetail?.parent_id;
    const userMsg: MessageView = {
      id: "u-" + newID(),
      role: "user",
      content: input,
      time: new Date().toISOString(),
      thread_id: parentMessageID,
      is_thread_reply: !!parentMessageID,
    };
    const activeAg = get().activeAgent;
    set({
      sending: true,
      detail: {
        ...detail,
        item: { ...detail.item, running: true },
        messages: [...detail.messages, userMsg],
      },
      threadDetail: threadDetail
        ? { ...threadDetail, replies: [...threadDetail.replies, userMsg] }
        : null,
      channels: get().channels.map((c) =>
        c.id === get().activeChannel ? { ...c, has_running: true } : c,
      ),
      threads: get().threads.map((t) =>
        t.id === get().activeThread ? { ...t, has_running: true } : t,
      ),
      agents: get().agents.map((a) =>
        a.id === activeAg ? { ...a, status: "running" } : a,
      ),
    });
    try {
      await api.send(sid, input, personaID, parentMessageID);
    } catch {
      set({ sending: false });
    }
  },

  async stop() {
    const sid = activeSessionID(get());
    if (!sid) return;
    try {
      await api.stop(sid);
    } catch {
      // noop
    }
    const detail = get().detail;
    set({
      sending: false,
      streaming: null,
      detail: detail ? { ...detail, item: { ...detail.item, running: false } } : detail,
      channels: get().channels.map((c) =>
        c.id === get().activeChannel ? { ...c, has_running: false } : c,
      ),
      threads: get().threads.map((t) =>
        t.id === get().activeThread ? { ...t, has_running: false } : t,
      ),
      agents: get().agents.map((a) =>
        a.id === get().activeAgent ? { ...a, status: "idle" } : a,
      ),
    });
  },

  connectStream() {
    const wails = (window as any).runtime;
    if (wails && typeof wails.EventsOn === "function") {
      const off = wails.EventsOn("bus", (ev: BusEvent) => {
        get().applyEvent(ev);
      });
      return () => {
        if (typeof off === "function") off();
      };
    }
    const src = new EventSource("/api/events");
    const onMessage = (e: MessageEvent) => {
      try {
        const ev = JSON.parse(e.data) as BusEvent;
        get().applyEvent(ev);
      } catch {
        // skip
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
        // Streaming events without a StreamID came from a publisher
        // that has not been migrated to P9.0 metadata. We cannot
        // correlate them safely; drop rather than guess.
        return;
      }
      if (!streamingEventInScope(ev, cur)) return;
    }

    // Legacy subtask source guard (for delegate.* and other event
    // types that have not been migrated to scope metadata yet).
    if (!isStreamEvent && ev.source && ev.source.startsWith("subtask:")) return;

    switch (ev.type) {
      case "turn.started": {
        if (lifecycleEventInScope(ev, cur)) {
          void refetchActiveScope(get, set);
        }
        const author = ev.agent_id;
        if (!author) {
          // Per Iris: no Sumi-by-default placeholder. If the publisher
          // didn't say who's typing, don't render a placeholder.
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
        set({
          streaming: {
            messageID: placeholder.id,
            streamID: ev.stream_id || "",
            agentID: author,
            spaceID: ev.space_id || "",
            parentMessageID: ev.parent_message_id || "",
            startedAt: ev.time,
            content: "",
            reasoning: "",
            toolCalls: new Map(),
          },
          ...updates,
        });
        return;
      }
      case "turn.chunk": {
        if (!cur.streaming) return;
        if (ev.stream_id !== cur.streaming.streamID) return;
        const next = cur.streaming.content + (ev.text || "");
        set({
          streaming: { ...cur.streaming, content: next },
          ...streamingMessageUpdates(cur, cur.streaming.messageID, (m) => ({
            ...m,
            content: next,
          })),
        });
        return;
      }
      case "turn.reasoning": {
        if (!cur.streaming) return;
        if (ev.stream_id !== cur.streaming.streamID) return;
        const next = cur.streaming.reasoning + (ev.text || "");
        set({
          streaming: { ...cur.streaming, reasoning: next },
          ...streamingMessageUpdates(cur, cur.streaming.messageID, (m) => ({
            ...m,
            reasoning: next,
          })),
        });
        return;
      }
      case "tool.call.started": {
        if (!cur.streaming) return;
        if (ev.stream_id !== cur.streaming.streamID) return;
        const id = ev.tool_call_id || "tc-" + newID();
        const block: EventBlock = {
          kind: "tool_call",
          tool_name: ev.tool,
          status: "running",
          time: ev.time,
        };
        cur.streaming.toolCalls.set(id, block);
        set({
          ...streamingMessageUpdates(cur, cur.streaming.messageID, (m) => ({
            ...m,
            events: Array.from(cur.streaming!.toolCalls.values()),
          })),
        });
        return;
      }
      case "tool.call.finished":
      case "tool.call.failed": {
        if (!cur.streaming) return;
        if (ev.stream_id !== cur.streaming.streamID) return;
        const id = ev.tool_call_id || "";
        const prev = cur.streaming.toolCalls.get(id);
        const failed = ev.type === "tool.call.failed";
        const block: EventBlock = {
          kind: "tool_call",
          tool_name: prev?.tool_name || ev.tool,
          status: failed ? "error" : "done",
          duration_ms: 0,
          time: ev.time,
        };
        cur.streaming.toolCalls.set(id, block);
        set({
          ...streamingMessageUpdates(cur, cur.streaming.messageID, (m) => ({
            ...m,
            events: Array.from(cur.streaming!.toolCalls.values()),
          })),
        });
        return;
      }
      case "agent.mention": {
        if (!cur.streaming) return;
        const id = ev.tool_call_id || "mn-" + newID();
        const block: EventBlock = {
          kind: "mention",
          status: "running",
          time: ev.time,
          agent_id: ev.tool,
          agent_display: prettyAgentName(ev.tool, cur.agents),
        };
        cur.streaming.toolCalls.set(id, block);
        set({
          detail: updateStreamEvents(detail, cur.streaming.messageID, cur.streaming.toolCalls),
        });
        return;
      }
      case "agent.mention.reply": {
        if (!cur.streaming) return;
        const id = ev.tool_call_id || "";
        const prev = cur.streaming.toolCalls.get(id);
        const block: EventBlock = {
          kind: "mention",
          status: "done",
          time: ev.time,
          agent_id: prev?.agent_id || ev.tool,
          agent_display: prev?.agent_display || prettyAgentName(ev.tool, cur.agents),
          reply: ev.output,
        };
        cur.streaming.toolCalls.set(id, block);
        set({
          detail: updateStreamEvents(detail, cur.streaming.messageID, cur.streaming.toolCalls),
        });
        return;
      }
      case "agent.delegate.started": {
        if (!cur.streaming) return;
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
        cur.streaming.toolCalls.set(id, block);
        set({
          detail: updateStreamEvents(detail, cur.streaming.messageID, cur.streaming.toolCalls),
        });
        return;
      }
      case "agent.delegate.progress": {
        if (!cur.streaming) return;
        const id = ev.tool_call_id || "";
        const prev = cur.streaming.toolCalls.get(id);
        if (!prev) return;
        cur.streaming.toolCalls.set(id, {
          ...prev,
          status: "running",
          time: ev.time,
          task_id: ev.task_id || prev.task_id,
        });
        set({
          detail: updateStreamEvents(detail, cur.streaming.messageID, cur.streaming.toolCalls),
        });
        return;
      }
      case "agent.delegate.finished":
      case "agent.delegate.failed":
      case "agent.delegate.canceled": {
        if (lifecycleEventInScope(ev, cur)) {
          void refetchActiveScope(get, set);
        }
        if (!cur.streaming) return;
        const id = ev.tool_call_id || "";
        const prev = cur.streaming.toolCalls.get(id);
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
        cur.streaming.toolCalls.set(id, block);
        set({
          detail: updateStreamEvents(detail, cur.streaming.messageID, cur.streaming.toolCalls),
        });
        return;
      }
      case "service.notice": {
        if (!cur.streaming) return;
        const id = "n-" + newID();
        cur.streaming.toolCalls.set(id, {
          kind: "service_notice",
          output: ev.text,
          status: "done",
          time: ev.time,
        });
        set({
          detail: updateStreamEvents(detail, cur.streaming.messageID, cur.streaming.toolCalls),
        });
        return;
      }
      case "turn.finished": {
        if (lifecycleEventInScope(ev, cur)) {
          void refetchActiveScope(get, set);
        }
        if (!cur.streaming || ev.stream_id !== cur.streaming.streamID) return;
        const detailNow = get().detail;
        const placeholderID = cur.streaming.messageID;
        const updates: Partial<State> = {
          sending: false,
          streaming: null,
        };
        if (detailNow) {
          updates.detail = {
            ...detailNow,
            item: { ...detailNow.item, running: false },
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
        updates.channels = get().channels.map((c) =>
          c.id === get().activeChannel ? { ...c, has_running: false } : c,
        );
        updates.threads = get().threads.map((t) =>
          t.id === get().activeThread ? { ...t, has_running: false } : t,
        );
        updates.agents = get().agents.map((a) =>
          a.id === get().activeAgent ? { ...a, status: "idle" } : a,
        );
        set(updates);
        return;
      }
      case "turn.error": {
        if (lifecycleEventInScope(ev, cur)) {
          void refetchActiveScope(get, set);
        }
        if (!cur.streaming || ev.stream_id !== cur.streaming.streamID) return;
        const errMsg = ev.err || "Turn failed";
        const placeholderID = cur.streaming.messageID;
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
        const updates: Partial<State> = {
          sending: false,
          streaming: null,
        };
        const detailNow = get().detail;
        if (detailNow) {
          updates.detail = {
            ...detailNow,
            item: { ...detailNow.item, running: false },
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
        updates.channels = get().channels.map((c) =>
          c.id === get().activeChannel ? { ...c, has_running: false } : c,
        );
        updates.threads = get().threads.map((t) =>
          t.id === get().activeThread ? { ...t, has_running: false } : t,
        );
        updates.agents = get().agents.map((a) =>
          a.id === get().activeAgent ? { ...a, status: "idle" } : a,
        );
        set(updates);
        return;
      }
    }
  },
}));

function updateStreamEvents(
  detail: SessionDetail,
  messageID: string,
  toolCalls: Map<string, EventBlock>,
): SessionDetail {
  const events = Array.from(toolCalls.values());
  return {
    ...detail,
    messages: detail.messages.map((m) =>
      m.id === messageID ? { ...m, events } : m,
    ),
  };
}

function prettyAgentName(id: string | undefined, agents: AgentItem[]): string {
  if (!id || !id.trim()) return "Unknown agent";
  const a = agents.find((x) => x.id === id);
  if (a) return a.display;
  return "Unknown agent (" + id + ")";
}
