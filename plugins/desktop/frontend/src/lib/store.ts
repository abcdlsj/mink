import { create } from "zustand";
import type {
  AgentItem,
  ChannelItem,
  CommandItem,
  EventBlock,
  MessageView,
  ModelItem,
  ParticipantsView,
  PersonaItem,
  SessionDetail,
  ThreadItem,
  ToolItem,
  ViewMode,
  WorkspaceState,
} from "./types";
import { api } from "./api";

interface BusEvent {
  type: string;
  session_id?: string;
  tool_call_id?: string;
  tool?: string;
  input?: string;
  output?: string;
  text?: string;
  err?: string;
  time: string;
}

interface StreamingTurn {
  messageID: string;
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
  agents: AgentItem[];
  personas: PersonaItem[];
  models: ModelItem[];
  tools: ToolItem[];
  commands: CommandItem[];
  detail: SessionDetail | null;
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
  openAgent: (id: string) => Promise<void>;
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

export const useStore = create<State>((set, get) => ({
  ready: false,
  state: null,
  channels: [],
  threads: [],
  agents: [],
  personas: [],
  models: [],
  tools: [],
  commands: [],
  detail: null,
  participants: null,

  view: "channel",
  activeChannel: null,
  activeThread: null,
  activeAgent: null,

  paletteOpen: false,
  sending: false,
  streaming: null,

  async loadInitial() {
    const [state, channels, threads, agents, personas, models, tools, commands] = await Promise.all([
      api.state(),
      api.channels(),
      api.threads(),
      api.agents(),
      api.personas(),
      api.models(),
      api.tools(),
      api.commands(),
    ]);
    set({ state, channels, threads, agents, personas, models, tools, commands, ready: true });
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
      participants,
      streaming: null,
    });
  },

  async openThread(id) {
    const detail = await api.thread(id);
    const participants = await api.participants(get().activeChannel || "", id);
    set({
      view: "thread",
      activeThread: id,
      activeAgent: null,
      detail,
      participants,
      streaming: null,
    });
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
      participants: null,
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
    const userMsg: MessageView = {
      id: "u-" + newID(),
      role: "user",
      content: input,
      time: new Date().toISOString(),
    };
    const activeAg = get().activeAgent;
    set({
      sending: true,
      detail: {
        ...detail,
        item: { ...detail.item, running: true },
        messages: [...detail.messages, userMsg],
      },
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
      await api.send(sid, input, personaID);
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

    switch (ev.type) {
      case "turn.started": {
        const placeholder: MessageView = {
          id: "a-" + newID(),
          role: "agent",
          author_id: cur.agents[0]?.id,
          author_name: cur.agents[0]?.display || "Sumi",
          content: "",
          reasoning: "",
          time: ev.time,
          events: [],
        };
        set({
          streaming: {
            messageID: placeholder.id,
            startedAt: ev.time,
            content: "",
            reasoning: "",
            toolCalls: new Map(),
          },
          detail: {
            ...detail,
            item: { ...detail.item, running: true },
            messages: [...detail.messages, placeholder],
          },
        });
        return;
      }
      case "turn.chunk": {
        if (!cur.streaming) return;
        const next = cur.streaming.content + (ev.text || "");
        set({
          streaming: { ...cur.streaming, content: next },
          detail: {
            ...detail,
            messages: detail.messages.map((m) =>
              m.id === cur.streaming!.messageID ? { ...m, content: next } : m,
            ),
          },
        });
        return;
      }
      case "turn.reasoning": {
        if (!cur.streaming) return;
        const next = cur.streaming.reasoning + (ev.text || "");
        set({
          streaming: { ...cur.streaming, reasoning: next },
          detail: {
            ...detail,
            messages: detail.messages.map((m) =>
              m.id === cur.streaming!.messageID ? { ...m, reasoning: next } : m,
            ),
          },
        });
        return;
      }
      case "tool.call.started": {
        if (!cur.streaming) return;
        const id = ev.tool_call_id || "tc-" + newID();
        const block: EventBlock = {
          kind: "tool_call",
          tool_name: ev.tool,
          args: ev.input,
          status: "running",
          time: ev.time,
        };
        cur.streaming.toolCalls.set(id, block);
        set({
          detail: updateStreamEvents(detail, cur.streaming.messageID, cur.streaming.toolCalls),
        });
        return;
      }
      case "tool.call.finished":
      case "tool.call.failed": {
        if (!cur.streaming) return;
        const id = ev.tool_call_id || "";
        const prev = cur.streaming.toolCalls.get(id);
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
        cur.streaming.toolCalls.set(id, block);
        set({
          detail: updateStreamEvents(detail, cur.streaming.messageID, cur.streaming.toolCalls),
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
        });
        set({
          detail: updateStreamEvents(detail, cur.streaming.messageID, cur.streaming.toolCalls),
        });
        return;
      }
      case "agent.delegate.finished":
      case "agent.delegate.failed": {
        if (!cur.streaming) return;
        const id = ev.tool_call_id || "";
        const prev = cur.streaming.toolCalls.get(id);
        const failed = ev.type === "agent.delegate.failed";
        const block: EventBlock = {
          kind: "delegate",
          status: failed ? "error" : "done",
          time: ev.time,
          agent_id: prev?.agent_id || ev.tool,
          agent_display: prev?.agent_display || prettyAgentName(ev.tool, cur.agents),
          task: prev?.task,
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
      case "turn.finished":
      case "turn.error": {
        const detailNow = get().detail;
        if (!detailNow) return;
        const isError = ev.type === "turn.error";
        let messages = detailNow.messages;
        if (isError && cur.streaming) {
          const errMsg = ev.err || "Turn failed";
          messages = messages.map((m) =>
            m.id === cur.streaming!.messageID
              ? {
                  ...m,
                  events: [
                    ...(m.events || []),
                    {
                      kind: "service_notice",
                      status: "error",
                      output: errMsg,
                      time: ev.time,
                    },
                  ],
                }
              : m,
          );
        }
        set({
          sending: false,
          streaming: null,
          detail: { ...detailNow, item: { ...detailNow.item, running: false }, messages },
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
