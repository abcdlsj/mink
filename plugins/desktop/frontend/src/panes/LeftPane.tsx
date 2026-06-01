import { Hash, AtSign, MessageCircle, Plus, Search, ChevronDown } from "lucide-react";
import { useEffect, useRef, useState } from "react";
import { useStore } from "@/lib/store";
import { Button } from "@/components/ui/button";
import { cn, relTime } from "@/lib/utils";

export function LeftPane() {
  const channels = useStore((s) => s.channels);
  const agents = useStore((s) => s.agents);
  const directChats = useStore((s) => s.directChats);
  const recent = useStore((s) => s.recent);
  const view = useStore((s) => s.view);
  const activeChannel = useStore((s) => s.activeChannel);
  const activeAgent = useStore((s) => s.activeAgent);
  const activeThread = useStore((s) => s.activeThread);
  const openChannel = useStore((s) => s.openChannel);
  const createChannel = useStore((s) => s.createChannel);
  const openAgent = useStore((s) => s.openAgent);
  const openDirectChat = useStore((s) => s.openDirectChat);
  const newDirectChat = useStore((s) => s.newDirectChat);
  const setPalette = useStore((s) => s.setPalette);

  const handleCreateChannel = async () => {
    const name = window.prompt("Channel name (letters / numbers / dashes)");
    if (name === null) return;
    const trimmed = name.trim();
    if (trimmed === "") return;
    try {
      await createChannel(trimmed);
    } catch (err) {
      window.alert("Could not create channel: " + (err instanceof Error ? err.message : String(err)));
    }
  };

  return (
    <aside className="h-full border-r border-border bg-panel-2 overflow-y-auto px-2 pb-4 pt-2.5">
      <div className="flex flex-col gap-1.5 px-1 pb-1.5">
        <NewMenu
          onChannel={() => {
            if (channels[0]) void openChannel(channels[0].id);
          }}
          onCreateChannel={() => {
            void handleCreateChannel();
          }}
          onDirect={() => {
            void newDirectChat();
          }}
          onMessageAgent={(id) => void openAgent(id)}
          agents={agents}
        />
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

      <GroupLabel>Agent DMs</GroupLabel>
      <ul className="flex flex-col gap-px">
        {agents.map((a) => (
          <NavItem
            key={a.id}
            icon={<AtSign className="size-4" />}
            name={a.display}
            running={a.status === "running"}
            active={view === "agent" && activeAgent === a.id}
            onClick={() => void openAgent(a.id)}
            tooltip={a.status === "running" ? `${a.display} · running` : undefined}
          />
        ))}
      </ul>

      <GroupLabel>Direct Chats</GroupLabel>
      <ul className="flex flex-col gap-px">
        {directChats.length === 0 && (
          <li className="px-2 py-1.5 text-[11.5px] text-text-faint">No direct chats yet.</li>
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

      <GroupLabel>Recent</GroupLabel>
      <ul className="flex flex-col gap-px">
        {recent.length === 0 && (
          <li className="px-2 py-1.5 text-[11.5px] text-text-faint">No recent activity.</li>
        )}
        {recent.map((r) => (
          <li key={r.id}>
            <button
              onClick={() => {
                if (r.kind === "channel") void openChannel(r.id);
                else if (r.kind === "agent_dm") {
                  // Recent gives a Space id but openAgent needs the persona id.
                  // We pull it from the displayed title which is "@<display>";
                  // fall back to opening by id if the lookup fails.
                  const personaID = r.title.startsWith("@") ? r.title.slice(1) : r.title;
                  void openAgent(personaID);
                } else if (r.kind === "direct_chat") void openDirectChat(r.id);
              }}
              className={cn(
                "w-full text-left px-2 py-1.5 rounded-sm border-l-2 border-transparent cursor-pointer transition-colors",
                ((r.kind === "channel" && activeChannel === r.id) ||
                  (r.kind === "direct_chat" && activeThread === r.id))
                  ? "bg-accent-bg border-l-accent"
                  : "hover:bg-panel",
              )}
              title={r.kind}
            >
              <div className="flex items-center gap-1.5 text-[12.5px] text-text">
                {r.kind === "channel" && <Hash className="size-3 text-text-faint shrink-0" />}
                {r.kind === "direct_chat" && <MessageCircle className="size-3 text-text-faint shrink-0" />}
                {r.kind === "agent_dm" && <AtSign className="size-3 text-text-faint shrink-0" />}
                <span className="truncate">{r.title}</span>
              </div>
              {r.subtitle && (
                <div className="text-[11px] text-text-faint mt-0.5 truncate">{r.subtitle}</div>
              )}
              <div className="text-[10.5px] text-text-faint">{relTime(r.updated_at)}</div>
            </button>
          </li>
        ))}
      </ul>
    </aside>
  );
}

function NewMenu({
  onChannel,
  onCreateChannel,
  onDirect,
  onMessageAgent,
  agents,
}: {
  onChannel: () => void;
  onCreateChannel: () => void;
  onDirect: () => void;
  onMessageAgent: (id: string) => void;
  agents: { id: string; display: string }[];
}) {
  const [open, setOpen] = useState(false);
  const ref = useRef<HTMLDivElement | null>(null);

  useEffect(() => {
    if (!open) return;
    const onDown = (e: MouseEvent) => {
      if (!ref.current?.contains(e.target as Node)) setOpen(false);
    };
    document.addEventListener("mousedown", onDown);
    return () => document.removeEventListener("mousedown", onDown);
  }, [open]);

  return (
    <div ref={ref} className="relative">
      <Button
        variant="default"
        size="default"
        className="justify-between w-full"
        onClick={() => setOpen((v) => !v)}
      >
        <span className="flex items-center gap-2">
          <Plus className="size-3" />
          <span>New</span>
        </span>
        <ChevronDown className={cn("size-3 text-text-faint transition-transform", open && "rotate-180")} />
      </Button>
      {open && (
        <div className="absolute z-30 mt-1 w-[220px] rounded-md border border-border bg-panel shadow-[0_8px_24px_rgba(31,41,51,0.10)] py-1 text-[13px]">
          <MenuItem
            label="Start direct chat"
            sub="A standalone conversation."
            onClick={() => {
              setOpen(false);
              onDirect();
            }}
          />
          <MenuItem
            label="Create channel"
            sub="A new shared room."
            onClick={() => {
              setOpen(false);
              onCreateChannel();
            }}
          />
          <MenuItem
            label="Open channel"
            sub="Jump to the workspace channel."
            onClick={() => {
              setOpen(false);
              onChannel();
            }}
          />
          <div className="my-1 border-t border-border-soft" />
          <div className="px-3 pb-0.5 pt-1 text-[10.5px] uppercase tracking-[0.7px] text-text-whisper font-display font-semibold">
            Message agent
          </div>
          {agents.length === 0 ? (
            <div className="px-3 py-1.5 text-text-faint text-[12.5px]">No agents configured.</div>
          ) : (
            agents.map((a) => (
              <button
                key={a.id}
                onClick={() => {
                  setOpen(false);
                  onMessageAgent(a.id);
                }}
                className="w-full text-left px-3 py-1.5 hover:bg-panel-2 cursor-pointer flex items-center gap-2"
              >
                <AtSign className="size-3 text-text-faint" />
                <span>{a.display}</span>
              </button>
            ))
          )}
        </div>
      )}
    </div>
  );
}

function MenuItem({ label, sub, onClick }: { label: string; sub: string; onClick: () => void }) {
  return (
    <button
      onClick={onClick}
      className="w-full text-left px-3 py-1.5 hover:bg-panel-2 cursor-pointer"
    >
      <div className="text-text">{label}</div>
      <div className="text-[11px] text-text-faint">{sub}</div>
    </button>
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
          active ? "bg-accent-bg text-text border-l-accent" : "hover:bg-panel hover:text-text",
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
        status === "running" && "bg-running dot-pulse",
        status === "done" && "bg-done",
        status === "error" && "bg-error",
        status === "idle" && "bg-text-faint",
        className,
      )}
    />
  );
}
