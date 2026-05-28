import { Hash, AtSign, MessageSquare, Plus, Search } from "lucide-react";
import { useStore } from "@/lib/store";
import { Button } from "@/components/ui/button";
import { cn, relTime } from "@/lib/utils";

export function LeftPane() {
  const channels = useStore((s) => s.channels);
  const agents = useStore((s) => s.agents);
  const threads = useStore((s) => s.threads);
  const view = useStore((s) => s.view);
  const activeChannel = useStore((s) => s.activeChannel);
  const activeAgent = useStore((s) => s.activeAgent);
  const activeThread = useStore((s) => s.activeThread);
  const openChannel = useStore((s) => s.openChannel);
  const openAgent = useStore((s) => s.openAgent);
  const openThread = useStore((s) => s.openThread);
  const setPalette = useStore((s) => s.setPalette);

  return (
    <aside className="h-full border-r border-border bg-panel-2 overflow-y-auto px-2 pb-4 pt-2.5">
      <div className="flex flex-col gap-1.5 px-1 pb-1.5">
        <Button variant="default" size="default" className="justify-start">
          <Plus className="size-3" />
          <span>New</span>
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

      <GroupLabel>Direct Agents</GroupLabel>
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

      <GroupLabel>Recent Threads</GroupLabel>
      <ul className="flex flex-col gap-px">
        {threads.map((t) => {
          const ch = channels.find((c) => c.id === t.channel_id);
          return (
            <li key={t.id}>
              <button
                onClick={() => void openThread(t.id)}
                title={t.has_running ? `${t.title} · agent running` : undefined}
                className={cn(
                  "w-full text-left px-2 py-1.5 rounded-sm border-l-2 border-transparent cursor-pointer transition-colors",
                  view === "thread" && activeThread === t.id
                    ? "bg-accent-bg border-l-accent"
                    : "hover:bg-panel",
                )}
              >
                <div className="flex items-center gap-1.5 text-[12.5px] text-text">
                  {t.has_running && <Dot status="running" />}
                  <MessageSquare className="size-3 text-text-faint shrink-0" />
                  <span className="truncate">{t.title}</span>
                </div>
                <div className="text-[11px] text-text-faint mt-0.5">
                  {ch ? `#${ch.name} · ` : ""}
                  {relTime(t.updated_at)}
                </div>
              </button>
            </li>
          );
        })}
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
