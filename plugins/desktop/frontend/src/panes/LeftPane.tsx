import { useState } from "react";
import { AtSign, ChevronRight, Hash, MessageCircle, Plus, Search } from "lucide-react";
import { useStore } from "@/lib/store";
import { Button } from "@/components/ui/button";
import { cn, relTime } from "@/lib/utils";

export function LeftPane() {
  const channels = useStore((s) => s.channels);
  const directChats = useStore((s) => s.directChats);
  const agentDMs = useStore((s) => s.agentDMs);
  const personas = useStore((s) => s.personas);
  const view = useStore((s) => s.view);
  const activeChannel = useStore((s) => s.activeChannel);
  const activeAgent = useStore((s) => s.activeAgent);
  const activeThread = useStore((s) => s.activeThread);
  const openChannel = useStore((s) => s.openChannel);
  const openAgent = useStore((s) => s.openAgent);
  const openDirectChat = useStore((s) => s.openDirectChat);
  const newAgentChat = useStore((s) => s.newAgentChat);
  const setPalette = useStore((s) => s.setPalette);
  const setQuickCreate = useStore((s) => s.setQuickCreate);
  const [openSections, setOpenSections] = useState({
    channels: true,
    direct: true,
    personaAgents: true,
    agents: true,
  });
  const toggleSection = (key: keyof typeof openSections) => {
    setOpenSections((s) => ({ ...s, [key]: !s[key] }));
  };
  const sidebarPersonas = personas.filter((p) => p.show_in_sidebar !== false);
  const activePersonaID =
    view === "agent" && activeAgent
      ? agentDMs.find((dm) => dm.id === activeAgent)?.persona_id || activeAgent
      : "";
  const openLatestAgentChat = async (personaID: string) => {
    const existing = agentDMs.find((dm) => dm.persona_id === personaID);
    if (existing) {
      await openAgent(existing.id);
      return;
    }
    await newAgentChat(personaID);
  };

  return (
    <aside className="relative h-full overflow-y-auto border-r-hard border-border bg-panel-2 px-3 pb-5 pt-3">
      <div className="flex flex-col gap-2 pb-2 pl-2">
        <Button
          variant="default"
          size="default"
          className="justify-start bg-panel"
          onClick={() => setQuickCreate(true)}
        >
          <Plus className="size-3" />
          <span>New</span>
          <span className="ml-auto border border-border bg-bg px-1.5 py-px font-mono text-[11px] text-text-muted">
            ⌘T
          </span>
        </Button>
        <Button
          variant="default"
          size="default"
          className="justify-start bg-bg text-text-muted hover:text-text"
          onClick={() => setPalette(true)}
        >
          <Search className="size-3" />
          <span>Search</span>
          <span className="ml-auto border border-border bg-panel px-1.5 py-px font-mono text-[11px] text-text-muted">
            ⌘K
          </span>
        </Button>
      </div>

      <GroupLabel open={openSections.channels} count={channels.length} onToggle={() => toggleSection("channels")}>
        Channels
      </GroupLabel>
      {openSections.channels && (
        <ul className="flex flex-col gap-px">
          {channels.length === 0 && (
            <li className="px-2 py-1.5 text-[11.5px] text-text-faint">No channels yet.</li>
          )}
          {channels.map((c) => (
            <NavItem
              key={c.id}
              icon={<Hash className="size-4" />}
              name={c.name}
              running={c.has_running}
              badge={c.unread_count}
              active={view === "channel" && activeChannel === c.id}
              onClick={() => void openChannel(c.id)}
              tooltip={c.has_running ? `${c.name} · agent running` : undefined}
            />
          ))}
        </ul>
      )}

      <GroupLabel open={openSections.direct} count={directChats.length} onToggle={() => toggleSection("direct")}>
        Direct Messages
      </GroupLabel>
      {openSections.direct && (
        <ul className="flex flex-col gap-px">
          {directChats.length === 0 && (
            <li className="px-2 py-1.5 text-[11.5px] text-text-faint">No direct messages yet.</li>
          )}
          {directChats.map((dc) => (
            <NavItem
              key={dc.id}
              icon={<MessageCircle className="size-4" />}
              name={dc.title}
              running={dc.has_running}
              active={view === "thread" && activeThread === dc.id}
              onClick={() => void openDirectChat(dc.id)}
              tooltip={dc.has_running ? `${dc.title} · running` : undefined}
            />
          ))}
        </ul>
      )}

      <GroupLabel open={openSections.personaAgents} count={sidebarPersonas.length} onToggle={() => toggleSection("personaAgents")}>
        Agents
      </GroupLabel>
      {openSections.personaAgents && (
        <ul className="flex flex-col gap-px">
          {sidebarPersonas.length === 0 && (
            <li className="px-2 py-1.5 text-[11.5px] text-text-faint">No sidebar agents configured.</li>
          )}
          {sidebarPersonas.map((agent) => {
            const latest = agentDMs.find((dm) => dm.persona_id === agent.id);
            const active = view === "agent" && activePersonaID === agent.id;
            return (
              <li key={agent.id}>
                <div
                  className={cn(
                    "grid w-full cursor-pointer grid-cols-[18px_1fr_auto] items-center gap-2 border-2 border-transparent py-1.5 pl-2 pr-2 text-text-muted transition-colors",
                    active
                      ? "border-border border-l-[10px] border-l-accent bg-panel font-semibold text-text shadow-card"
                      : "hover:border-border hover:bg-panel hover:text-text",
                  )}
                  title={personaTooltip(agent)}
                >
                  <button
                    type="button"
                    onClick={() => void openLatestAgentChat(agent.id)}
                    className={cn(
                      "inline-flex size-6 items-center justify-center border border-transparent font-mono",
                      active ? "border-border bg-accent text-text" : "text-text-muted",
                    )}
                    tabIndex={-1}
                  >
                    <AtSign className="size-4" />
                  </button>
                  <button
                    type="button"
                    onClick={() => void openLatestAgentChat(agent.id)}
                    className="min-w-0 text-left"
                  >
                    <span className="block truncate text-[13px]">{agent.display || agent.id}</span>
                    <span className="block truncate font-mono text-[10.5px] text-text-faint">
                      {latest ? "latest " + relTime(latest.updated_at) : "start chat"}
                    </span>
                  </button>
                  <button
                    type="button"
                    onClick={(e) => {
                      e.stopPropagation();
                      void newAgentChat(agent.id);
                    }}
                    className="inline-flex size-6 items-center justify-center border border-border bg-panel text-text-muted hover:bg-accent hover:text-text"
                    title={"New chat with @" + (agent.display || agent.id)}
                  >
                    <Plus className="size-3" />
                  </button>
                </div>
              </li>
            );
          })}
        </ul>
      )}

      <GroupLabel open={openSections.agents} count={agentDMs.length} onToggle={() => toggleSection("agents")}>
        Agent Chats
      </GroupLabel>
      {openSections.agents && (
        <ul className="flex flex-col gap-px">
          {agentDMs.length === 0 && (
            <li className="px-2 py-1.5 text-[11.5px] text-text-faint">No agent chats yet.</li>
          )}
          {agentDMs.map((dm) => (
            <li key={dm.id}>
              <button
                onClick={() => void openAgent(dm.id)}
                className={cn(
                  "w-full cursor-pointer border-2 border-transparent px-2 py-1.5 text-left transition-colors",
                  view === "agent" && activeAgent === dm.id
                    ? "border-border border-l-[10px] border-l-accent bg-panel font-semibold text-text shadow-card"
                    : "text-text-muted hover:border-border hover:bg-panel hover:text-text",
                )}
                title={"@" + (dm.persona_name || dm.persona_id)}
              >
                <div className="flex items-center gap-1.5 text-[13px]">
                  <AtSign className="size-3 text-text-muted shrink-0" />
                  <span className="truncate">
                    {dm.title && dm.title !== "New chat" ? dm.title : "New chat"}
                  </span>
                </div>
                <div className="mt-0.5 truncate font-mono text-[11px] text-text-muted">
                  @{dm.persona_name || dm.persona_id}
                  {dm.updated_at ? " · " + relTime(dm.updated_at) : ""}
                </div>
              </button>
            </li>
          ))}
        </ul>
      )}
    </aside>
  );
}

function personaTooltip(agent: import("@/lib/types").PersonaItem) {
  return [
    "@" + (agent.display || agent.id),
    "runtime: " + (agent.runtime || "default"),
    "model: " + (agent.model || (agent.runtime && agent.runtime !== "native" ? agent.runtime + " default" : "default")),
  ].join("\n");
}

function GroupLabel({
  children,
  count,
  open,
  onToggle,
}: {
  children: React.ReactNode;
  count: number;
  open: boolean;
  onToggle: () => void;
}) {
  return (
    <button
      onClick={onToggle}
      className="flex w-full items-center gap-1 border-b-2 border-border px-2 pb-1 pt-4 text-left font-display text-[10.5px] font-black uppercase tracking-[1.2px] text-text hover:text-text-muted"
    >
      <ChevronRight className={cn("size-3 text-text-faint transition-transform", open && "rotate-90")} />
      <span>{children}</span>
      <span className="ml-auto font-mono text-[10px] font-medium text-text-faint">{count}</span>
    </button>
  );
}

interface NavItemProps {
  icon: React.ReactNode;
  name: string;
  running?: boolean;
  badge?: number;
  active?: boolean;
  onClick?: () => void;
  tooltip?: string;
}

function NavItem({ icon, name, running, badge, active, onClick, tooltip }: NavItemProps) {
  return (
    <li>
      <button
        onClick={onClick}
        title={tooltip}
        className={cn(
          "grid w-full cursor-pointer grid-cols-[18px_1fr_auto] items-center gap-2 border-2 border-transparent py-1.5 pl-2 pr-2 text-text-muted transition-colors",
          active ? "border-border border-l-[10px] border-l-accent bg-panel font-semibold text-text shadow-card" : "hover:border-border hover:bg-panel hover:text-text",
        )}
      >
        <span
          className={cn(
            "inline-flex size-6 items-center justify-center border border-transparent font-mono",
            active ? "border-border bg-accent text-text" : "text-text-muted",
          )}
        >
          {icon}
        </span>
        <span className="flex items-center gap-1.5 min-w-0">
          {running && <Dot status="running" />}
          <span className="truncate text-[13px] text-left">{name}</span>
        </span>
        <span className="flex items-center gap-1 text-[11px] text-text-muted tabular-nums">
          {badge ? (
            <span className="inline-flex h-4 items-center border border-border bg-action px-1.5 text-[10.5px] font-semibold text-text">
              {badge}
            </span>
          ) : null}
        </span>
      </button>
    </li>
  );
}

export function Dot({ status, className }: { status: "running" | "done" | "error" | "idle"; className?: string }) {
  return (
    <span
      className={cn(
        "inline-block size-[7px] rounded-full border border-border shrink-0",
        status === "running" && "bg-running",
        status === "done" && "bg-done",
        status === "error" && "bg-error",
        status === "idle" && "bg-text-faint",
        className,
      )}
    />
  );
}
