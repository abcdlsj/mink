import { Hash, AtSign, MessageCircle, Plus, Search } from "lucide-react";
import { useStore } from "@/lib/store";
import { Button } from "@/components/ui/button";
import { cn, relTime } from "@/lib/utils";

export function LeftPane() {
  const channels = useStore((s) => s.channels);
  const directChats = useStore((s) => s.directChats);
  const agentDMs = useStore((s) => s.agentDMs);
  const view = useStore((s) => s.view);
  const activeChannel = useStore((s) => s.activeChannel);
  const activeAgent = useStore((s) => s.activeAgent);
  const activeThread = useStore((s) => s.activeThread);
  const openChannel = useStore((s) => s.openChannel);
  const openAgent = useStore((s) => s.openAgent);
  const openDirectChat = useStore((s) => s.openDirectChat);
  const setPalette = useStore((s) => s.setPalette);
  const setQuickCreate = useStore((s) => s.setQuickCreate);

  return (
    <aside className="h-full border-r border-border bg-panel-2 overflow-y-auto px-2 pb-4 pt-2.5">
      <div className="flex flex-col gap-1.5 px-1 pb-1.5">
        <Button
          variant="default"
          size="default"
          className="justify-start"
          onClick={() => setQuickCreate(true)}
        >
          <Plus className="size-3" />
          <span>New</span>
          <span className="ml-auto rounded border border-border px-1.5 py-px font-mono text-[11px] text-text-faint">
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
          <span className="ml-auto rounded border border-border px-1.5 py-px font-mono text-[11px] text-text-faint">
            ⌘K
          </span>
        </Button>
      </div>

      <GroupLabel>Channels</GroupLabel>
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

      <GroupLabel>Direct Messages</GroupLabel>
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

      <GroupLabel>Agent Chats</GroupLabel>
      <ul className="flex flex-col gap-px">
        {agentDMs.length === 0 && (
          <li className="px-2 py-1.5 text-[11.5px] text-text-faint">No agent chats yet.</li>
        )}
        {agentDMs.map((dm) => (
          <li key={dm.id}>
            <button
              onClick={() => void openAgent(dm.id)}
              className={cn(
                "w-full text-left px-2 py-1.5 rounded-sm border-l-2 border-transparent cursor-pointer transition-colors",
                view === "agent" && activeAgent === dm.id
                  ? "border-l-accent font-medium text-text"
                  : "text-text-muted hover:text-text",
              )}
              title={"@" + (dm.persona_name || dm.persona_id)}
            >
              <div className="flex items-center gap-1.5 text-[13px]">
                <AtSign className="size-3 text-text-faint shrink-0" />
                <span className="truncate">
                  {dm.title && dm.title !== "New chat" ? dm.title : "New chat"}
                </span>
              </div>
              <div className="text-[11px] text-text-faint mt-0.5 truncate">
                @{dm.persona_name || dm.persona_id}
                {dm.updated_at ? " · " + relTime(dm.updated_at) : ""}
              </div>
            </button>
          </li>
        ))}
      </ul>
    </aside>
  );
}

function GroupLabel({ children }: { children: React.ReactNode }) {
  return (
    <div className="font-display text-[10.5px] uppercase tracking-[0.9px] text-text-faint pt-3.5 pb-1 px-2 font-semibold">
      {children}
    </div>
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
          "w-full grid grid-cols-[16px_1fr_auto] items-center gap-2 rounded-sm py-1.5 pl-2.5 pr-2 cursor-pointer border-l-2 border-transparent transition-colors text-text-muted",
          active ? "text-text border-l-accent font-medium" : "hover:text-text",
        )}
      >
        <span className={cn("inline-flex items-center", active ? "text-text" : "text-text-faint")}>
          {icon}
        </span>
        <span className="flex items-center gap-1.5 min-w-0">
          {running && <Dot status="running" />}
          <span className="truncate text-[13px] text-left">{name}</span>
        </span>
        <span className="flex items-center gap-1 text-[11px] text-text-faint tabular-nums">
          {badge ? (
            <span className="bg-accent-bg text-accent rounded-[10px] h-4 px-1.5 inline-flex items-center text-[10.5px]">
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
        "inline-block size-[7px] rounded-full shrink-0",
        status === "running" && "bg-running",
        status === "done" && "bg-done",
        status === "error" && "bg-error",
        status === "idle" && "bg-text-faint",
        className,
      )}
    />
  );
}
