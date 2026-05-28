import { create } from "zustand";
import type {
  AgentItem,
  ChannelItem,
  CommandItem,
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

  loadInitial: () => Promise<void>;
  openChannel: (id: string) => Promise<void>;
  openThread: (id: string) => Promise<void>;
  openAgent: (id: string) => Promise<void>;
  setPalette: (open: boolean) => void;
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
    });
  },

  async openAgent(id) {
    const ag = get().agents.find((a) => a.id === id);
    set({
      view: "agent",
      activeAgent: id,
      activeChannel: null,
      activeThread: null,
      detail: {
        item: {
          id,
          title: "@" + (ag?.display || id),
          updated_at: new Date().toISOString(),
          running: ag?.status === "running",
        },
        summary: ag?.role || "",
        messages: [],
      },
      participants: null,
    });
  },

  setPalette(open) {
    set({ paletteOpen: open });
  },
}));
