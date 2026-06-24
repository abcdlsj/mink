import { useState } from "react";
import { Bot, ChevronRight, ClipboardList, Hash, MessageCircle, Plus, Search } from "lucide-react";
import { Identicon } from "@/components/Identicon";
import { useStore } from "@/lib/store";
import { Button } from "@/components/ui/button";
import { cn, relTime } from "@/lib/utils";

export function LeftPane() {
  const channels = useStore((s) => s.channels);
  const directChats = useStore((s) => s.directChats);
  const agentDMs = useStore((s) => s.agentDMs);
  const agents = useStore((s) => s.agents);
  const view = useStore((s) => s.view);
  const activeChannel = useStore((s) => s.activeChannel);
  const activeDirect = useStore((s) => s.activeDirect);
  const activeAgentSpace = useStore((s) => s.activeAgentSpace);
  const openChannel = useStore((s) => s.openChannel);
  const openAgent = useStore((s) => s.openAgent);
  const openAgentsPanel = useStore((s) => s.openAgentsPanel);
  const openDirectChat = useStore((s) => s.openDirectChat);
  const openTaskBoard = useStore((s) => s.openTaskBoard);
  const setPalette = useStore((s) => s.setPalette);
  const setQuickCreate = useStore((s) => s.setQuickCreate);
  const [openSections, setOpenSections] = useState({
    channels: true,
    direct: true,
    agents: true,
  });
  const toggleSection = (key: keyof typeof openSections) => {
    setOpenSections((s) => ({ ...s, [key]: !s[key] }));
  };
  const humanDirectChats = directChats.filter((dm) => dm.kind !== "agent_dm");
  const agentDirectChats = directChats.filter((dm) => dm.kind === "agent_dm");
  const directCount = humanDirectChats.length + agentDirectChats.length;

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
        <Button
          variant="default"
          size="default"
          className={cn(
            "justify-start bg-bg text-text-muted hover:text-text",
            view === "agents" && "bg-agent-bg text-agent",
          )}
          onClick={() => openAgentsPanel()}
        >
          <Bot className="size-3" />
          <span>Agents</span>
          {agents.length > 0 && (
            <span className="ml-auto text-[11px] text-text-faint">{agents.length} agents</span>
          )}
        </Button>
        <Button
          variant="default"
          size="default"
          className={cn(
            "justify-start bg-bg text-text-muted hover:text-text",
            view === "tasks" && "bg-accent text-text",
          )}
          onClick={() => openTaskBoard()}
        >
          <ClipboardList className="size-3" />
          <span>Task Board</span>
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
              icon={<Hash className="size-3" />}
              name={c.name}
              subtitle={c.topic || (c.agents.length ? `${c.agents.length} agents` : "channel")}
              time={relTime(c.updated_at)}
              running={c.has_running}
              badge={c.unread_count}
              active={view === "channel" && activeChannel === c.id}
              onClick={() => void openChannel(c.id)}
              tooltip={c.has_running ? `${c.name} · agent running` : undefined}
            />
          ))}
        </ul>
      )}

      <GroupLabel open={openSections.direct} count={directCount} onToggle={() => toggleSection("direct")}>
        Direct Messages
      </GroupLabel>
      {openSections.direct && (
        <ul className="flex flex-col gap-px">
          {directCount === 0 && (
            <li className="px-2 py-1.5 text-[11.5px] text-text-faint">No direct messages yet.</li>
          )}
          {humanDirectChats.map((dc) => (
            <NavItem
              key={dc.id}
              icon={<MessageCircle className="size-4" />}
              name={dc.title}
              subtitle="direct"
              time={relTime(dc.updated_at)}
              running={dc.has_running}
              active={view === "direct" && activeDirect === dc.id}
              onClick={() => void openDirectChat(dc.id)}
              tooltip={dc.has_running ? `${dc.title} · running` : undefined}
            />
          ))}
          {agentDirectChats.map((dc) => {
            const agentName = dc.persona_name || dc.persona_id || "agent";
            const title = chatTitle(dc.title, agentName);
            return (
              <NavItem
                key={dc.id}
                icon={<AgentBadge seed={dc.persona_id || dc.id} />}
                name={title}
                subtitle={`Default agent DM · ${agentName}`}
                time={relTime(dc.updated_at)}
                running={dc.has_running}
                active={view === "agent" && activeAgentSpace === dc.id}
                onClick={() => void openDirectChat(dc.id)}
                tooltip={dc.has_running ? `${title} · running` : `${agentName} · Default agent DM`}
              />
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
          {agentDMs.map((dm) => {
            const agentName = dm.persona_name || dm.persona_id || "agent";
            const title = chatTitle(dm.title);
            return (
              <li key={dm.id}>
                <button
                  onClick={() => void openAgent(dm.id)}
                  className={cn(
                    "w-full cursor-pointer border-2 border-transparent px-2 py-2 text-left transition-colors",
                    view === "agent" && activeAgentSpace === dm.id
                      ? "border-agent-border border-l-[10px] border-l-agent bg-agent-bg text-agent shadow-card"
                      : "text-text-muted hover:border-border hover:bg-panel hover:text-text",
                  )}
                  title={`${title} · ${agentName}`}
                >
                  <div className="flex items-center gap-1.5 text-[13px] font-semibold">
                    <AgentBadge seed={dm.persona_id || dm.id} />
                    <span className="truncate">{title}</span>
                  </div>
                  <div className="mt-0.5 truncate font-mono text-[10.5px] text-text-faint">
                    Agent Chat · {agentName}
                    {dm.updated_at ? " · " + relTime(dm.updated_at) : ""}
                  </div>
                </button>
              </li>
            );
          })}
        </ul>
      )}
    </aside>
  );
}

function AgentBadge({ seed }: { seed: string }) {
  return (
    <span className="inline-flex size-5 shrink-0 overflow-hidden border border-border bg-panel">
      <Identicon seed={seed} kind="agent" />
    </span>
  );
}

function chatTitle(raw: string | undefined, fallback = "New chat"): string {
  const title = (raw || "").trim();
  if (!title || title === "New chat") return fallback;
  return title.startsWith("@") ? title.slice(1).trim() || fallback : title;
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
      className="flex w-full items-center gap-1 border-b-2 border-border px-2 pb-1 pt-4 text-left font-display text-[11px] font-extrabold uppercase text-text hover:text-text-muted"
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
  subtitle?: string;
  time?: string;
  running?: boolean;
  badge?: number;
  active?: boolean;
  onClick?: () => void;
  tooltip?: string;
}

function NavItem({ icon, name, subtitle, time, running, badge, active, onClick, tooltip }: NavItemProps) {
  return (
    <li>
      <button
        onClick={onClick}
        title={tooltip}
        className={cn(
          "grid w-full cursor-pointer grid-cols-[18px_minmax(0,1fr)_auto] items-start gap-2 border-2 border-transparent py-2 pl-2 pr-2 text-text-muted transition-colors",
          active ? "border-border border-l-[10px] border-l-accent bg-panel text-text shadow-card" : "hover:border-border hover:bg-panel hover:text-text",
        )}
      >
        <span
          className={cn(
            "mt-0.5 inline-flex size-5 items-center justify-center border border-transparent font-mono",
            active ? "border-border bg-accent text-text" : "text-text-muted",
          )}
        >
          {icon}
        </span>
        <span className="min-w-0 text-left">
          <span className="flex min-w-0 items-center gap-1.5">
            {running && <Dot status="running" />}
            <span className="truncate text-[13px] font-semibold leading-[17px]">{name}</span>
          </span>
          {(subtitle || time) && (
            <span className="mt-0.5 flex min-w-0 items-center gap-1.5 font-mono text-[10.5px] leading-[14px] text-text-faint">
              {subtitle && <span className="truncate">{subtitle}</span>}
              {subtitle && time && <span className="shrink-0 text-text-whisper">·</span>}
              {time && <span className="shrink-0 tabular-nums">{time}</span>}
            </span>
          )}
        </span>
        <span className="mt-0.5 flex items-center gap-1 text-[11px] text-text-muted tabular-nums">
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
        "status-dot relative inline-block size-[7px] rounded-full border border-border shrink-0",
        status === "running" && "bg-running",
        status === "done" && "bg-done",
        status === "error" && "bg-error",
        status === "idle" && "bg-text-faint",
        status === "running" && "status-dot-running",
        className,
      )}
    />
  );
}
