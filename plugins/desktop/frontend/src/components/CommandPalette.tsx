import { useEffect, useMemo, useState } from "react";
import { Command } from "cmdk";
import { Hash, AtSign, MessageSquare, Sparkles, Cpu, Settings as SettingsIcon } from "lucide-react";
import { useStore } from "@/lib/store";
import { cn } from "@/lib/utils";

const MAX = { channel: 4, thread: 4, agent: 3, command: 3, model: 2, settings: 1 };

interface CommandItemRow {
  type: "channel" | "thread" | "agent" | "command" | "model" | "settings";
  id: string;
  label: string;
  meta?: string;
  onRun: () => void;
}

export function CommandPalette() {
  const open = useStore((s) => s.paletteOpen);
  const setPalette = useStore((s) => s.setPalette);
  const channels = useStore((s) => s.channels);
  const threads = useStore((s) => s.threads);
  const agents = useStore((s) => s.agents);
  const commands = useStore((s) => s.commands);
  const models = useStore((s) => s.models);
  const openChannel = useStore((s) => s.openChannel);
  const openThread = useStore((s) => s.openThread);
  const newAgentChat = useStore((s) => s.newAgentChat);

  const [query, setQuery] = useState("");

  useEffect(() => {
    if (!open) setQuery("");
  }, [open]);

  const { mode, normalized } = parseQuery(query);

  const allItems = useMemo<CommandItemRow[]>(() => {
    const items: CommandItemRow[] = [];

    channels.forEach((c) =>
      items.push({
        type: "channel",
        id: c.id,
        label: "#" + c.name,
        meta: c.topic || (c.has_running ? "running" : ""),
        onRun: () => {
          void openChannel(c.id);
          setPalette(false);
        },
      }),
    );

    threads.forEach((t) => {
      const ch = channels.find((c) => c.id === t.channel_id);
      items.push({
        type: "thread",
        id: t.id,
        label: t.title,
        meta: ch ? "#" + ch.name : undefined,
        onRun: () => {
          void openThread(t.id);
          setPalette(false);
        },
      });
    });

    agents.forEach((a) =>
      items.push({
        type: "agent",
        id: a.id,
        label: "@" + a.display,
        meta: a.role,
        onRun: () => {
          void newAgentChat(a.id);
          setPalette(false);
        },
      }),
    );

    commands.forEach((c) =>
      items.push({
        type: "command",
        id: c.name,
        label: c.name,
        meta: c.summary,
        onRun: () => {
          setPalette(false);
        },
      }),
    );

    models.forEach((m) =>
      items.push({
        type: "model",
        id: m.name,
        label: m.model,
        meta: m.provider + (m.ready ? " · ready" : ""),
        onRun: () => {
          setPalette(false);
        },
      }),
    );

    items.push({
      type: "settings",
      id: "open-settings",
      label: "Open Settings",
      onRun: () => setPalette(false),
    });

    return items;
  }, [channels, threads, agents, commands, models, openChannel, openThread, newAgentChat, setPalette]);

  const filtered = useMemo(() => {
    return allItems.filter((it) => {
      if (mode && mode !== it.type) return false;
      if (!normalized) return true;
      return it.label.toLowerCase().includes(normalized) || (it.meta || "").toLowerCase().includes(normalized);
    });
  }, [allItems, mode, normalized]);

  const grouped = useMemo(() => {
    const sections: Record<string, { title: string; rows: CommandItemRow[] }> = {
      channel: { title: "Channels", rows: [] },
      thread: { title: "Threads", rows: [] },
      agent: { title: "Agents", rows: [] },
      command: { title: "Commands", rows: [] },
      model: { title: "Models", rows: [] },
      settings: { title: "Settings", rows: [] },
    };
    filtered.forEach((it) => sections[it.type].rows.push(it));
    Object.keys(sections).forEach((k) => {
      const limit = MAX[k as keyof typeof MAX];
      sections[k].rows = sections[k].rows.slice(0, limit);
    });
    return sections;
  }, [filtered]);

  if (!open) return null;

  return (
    <div
      className="fixed inset-0 z-50 flex items-start justify-center pt-[120px] bg-[rgba(31,41,51,0.08)]"
      onClick={() => setPalette(false)}
    >
      <Command
        loop
        shouldFilter={false}
        label="Command palette"
        onClick={(e) => e.stopPropagation()}
        className="w-[600px] max-w-[92vw] bg-panel border border-border rounded-lg overflow-hidden shadow-[0_24px_56px_rgba(31,41,51,0.12),_0_4px_12px_rgba(31,41,51,0.06)]"
      >
        <Command.Input
          autoFocus
          value={query}
          onValueChange={setQuery}
          placeholder="Search #channel, thread, @agent, /command, model"
          className="w-full border-none bg-transparent px-[18px] py-4 text-[14px] outline-none border-b border-border-soft placeholder:text-text-faint"
        />
        <Command.List className="max-h-[420px] overflow-y-auto py-1.5 pb-2.5">
          <Command.Empty className="px-4 py-6 text-center text-[12.5px] text-text-faint">
            No results. Try #channel, thread, @agent, /command, or model name.
          </Command.Empty>
          {(["channel", "thread", "agent", "command", "model", "settings"] as const).map((key) => {
            const sec = grouped[key];
            if (!sec.rows.length) return null;
            return (
              <Command.Group
                key={key}
                heading={sec.title}
                className="text-text [&_[cmdk-group-heading]]:text-[10px] [&_[cmdk-group-heading]]:uppercase [&_[cmdk-group-heading]]:tracking-[0.7px] [&_[cmdk-group-heading]]:text-text-faint [&_[cmdk-group-heading]]:px-[18px] [&_[cmdk-group-heading]]:py-1.5 [&_[cmdk-group-heading]]:font-semibold"
              >
                {sec.rows.map((row) => (
                  <Command.Item
                    key={key + ":" + row.id}
                    value={row.label + " " + (row.meta || "")}
                    onSelect={row.onRun}
                    className={cn(
                      "flex items-center justify-between gap-2 px-[18px] py-1.5 text-[13px] cursor-pointer text-text",
                      "data-[selected=true]:bg-accent-bg",
                    )}
                  >
                    <span className="flex items-center gap-2 flex-1 min-w-0">
                      <PaletteIcon type={row.type} />
                      <span className="truncate">{row.label}</span>
                    </span>
                    {row.meta && (
                      <span className="text-[12px] text-text-faint truncate max-w-[60%]">
                        {row.meta}
                      </span>
                    )}
                  </Command.Item>
                ))}
              </Command.Group>
            );
          })}
        </Command.List>
      </Command>
    </div>
  );
}

function PaletteIcon({ type }: { type: CommandItemRow["type"] }) {
  const cls = "size-3.5 text-text-faint shrink-0";
  if (type === "channel") return <Hash className={cls} />;
  if (type === "thread") return <MessageSquare className={cls} />;
  if (type === "agent") return <AtSign className={cls} />;
  if (type === "command") return <Sparkles className={cls} />;
  if (type === "model") return <Cpu className={cls} />;
  return <SettingsIcon className={cls} />;
}

function parseQuery(q: string): { mode: CommandItemRow["type"] | null; normalized: string } {
  const lower = q.replace(/^\s+/, "").toLowerCase();
  if (lower.startsWith("#")) return { mode: "channel", normalized: lower.slice(1).trim() };
  if (lower.startsWith("@")) return { mode: "agent", normalized: lower.slice(1).trim() };
  if (lower.startsWith("/")) return { mode: "command", normalized: lower.trim() };
  if (lower.startsWith("thread ") || lower === "thread") {
    return { mode: "thread", normalized: lower.slice(6).trim() };
  }
  if (lower.startsWith("model ") || lower === "model") {
    return { mode: "model", normalized: lower.slice(5).trim() };
  }
  return { mode: null, normalized: lower.trim() };
}
