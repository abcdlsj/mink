import { useEffect, useRef, useState } from "react";
import { AtSign, Plus, Settings } from "lucide-react";
import type { AgentItem, ChannelItem, ThreadDetail } from "@/lib/types";
import { useStore } from "@/lib/store";
import { cn } from "@/lib/utils";

type GearScope =
  | { kind: "channel"; channel: ChannelItem }
  | { kind: "thread"; detail: ThreadDetail };

export function AgentGear({
  scope,
  agents,
}: {
  scope: GearScope;
  agents: AgentItem[];
}) {
  const [open, setOpen] = useState(false);
  const [picking, setPicking] = useState(false);
  const [pickQuery, setPickQuery] = useState("");
  const ref = useRef<HTMLDivElement | null>(null);
  const setChannelMode = useStore((s) => s.setChannelAgentMode);
  const setThreadMode = useStore((s) => s.setThreadAgentMode);
  const addAgent = useStore((s) => s.addAgentToChannel);

  useEffect(() => {
    if (!open) return;
    const onDown = (e: MouseEvent) => {
      if (!ref.current?.contains(e.target as Node)) {
        setOpen(false);
        setPicking(false);
        setPickQuery("");
      }
    };
    document.addEventListener("mousedown", onDown);
    return () => document.removeEventListener("mousedown", onDown);
  }, [open]);

  const joinedSource =
    scope.kind === "channel" ? scope.channel.agents : scope.detail.channel_agents;
  const modeMap =
    scope.kind === "channel" ? scope.channel.agent_modes : scope.detail.agent_modes;
  const joinedIDs = new Set(joinedSource || []);
  const joined = agents.filter((a) => joinedIDs.has(a.id));
  const candidates = agents.filter(
    (a) =>
      !joinedIDs.has(a.id) &&
      (pickQuery === "" ||
        a.id.toLowerCase().includes(pickQuery.toLowerCase()) ||
        a.display.toLowerCase().includes(pickQuery.toLowerCase())),
  );
  const modeFor = (id: string) => modeMap?.[id] || "mention_only";
  const flip = (id: string, next: string) => {
    if (scope.kind === "channel") void setChannelMode(scope.channel.id, id, next);
    else void setThreadMode(scope.detail.space_id, scope.detail.parent_id, id, next);
  };
  const heading =
    scope.kind === "channel" ? "Agents in this channel" : "Agents in this thread";
  const empty =
    scope.kind === "channel" ? "No agents joined yet." : null;
  if (scope.kind === "thread" && joined.length === 0) return null;

  return (
    <div className="relative shrink-0" ref={ref}>
      <button
        onClick={() => setOpen(!open)}
        className={cn(
          "inline-flex size-6 items-center justify-center border border-transparent text-text-muted hover:border-border hover:bg-accent hover:text-text",
          scope.kind === "channel" && "ml-1",
        )}
        title={scope.kind === "channel" ? "Channel agents" : "Thread agents"}
      >
        <Settings className="size-3.5" />
      </button>
      {open && (
        <div
          className={cn(
            "absolute z-30 mt-1 w-[280px] border-hard border-border bg-panel py-1 text-[13px] shadow-hard",
            scope.kind === "thread" && "right-0",
          )}
        >
          <div className="border-b border-border px-3 py-1.5 font-display text-[11px] font-extrabold uppercase text-text">
            {heading}
          </div>
          {joined.length === 0 && empty && (
            <div className="px-3 py-2 text-[11.5px] text-text-faint">{empty}</div>
          )}
          {joined.map((a) => {
            const m = modeFor(a.id);
            const next = m === "listen" ? "mention_only" : "listen";
            return (
              <button
                key={a.id}
                onClick={() => flip(a.id, next)}
                className="flex w-full cursor-pointer items-center justify-between px-3 py-1.5 hover:bg-accent"
              >
                <span className="flex items-center gap-1.5 text-text">
                  <AtSign className="size-3 text-text-faint" />
                  {a.display}
                </span>
                <span
                  className={cn(
                    "text-[11px]",
                    m === "listen" ? "font-semibold text-text" : "text-text-muted",
                  )}
                >
                  {m === "listen" ? "Listen" : "Mention only"}
                </span>
              </button>
            );
          })}
          {scope.kind === "channel" && (
            <div className="mt-1 border-t border-border pt-1">
              {!picking ? (
                <button
                  onClick={() => setPicking(true)}
                  className="flex w-full cursor-pointer items-center gap-1.5 px-3 py-1.5 text-text-muted hover:bg-accent hover:text-text"
                >
                  <Plus className="size-3" />
                  Add agent…
                </button>
              ) : (
                <div>
                  <input
                    autoFocus
                    value={pickQuery}
                    onChange={(e) => setPickQuery(e.target.value)}
                    placeholder="Search agents…"
                    className="w-full border-b border-border bg-bg px-3 py-1.5 text-[13px] outline-none"
                  />
                  <div className="max-h-[200px] overflow-y-auto">
                    {candidates.length === 0 && (
                      <div className="px-3 py-2 text-[11.5px] text-text-faint">No matching agent.</div>
                    )}
                    {candidates.map((a) => (
                      <button
                        key={a.id}
                        onClick={async () => {
                          await addAgent(scope.channel.id, a.id);
                          setPicking(false);
                          setPickQuery("");
                        }}
                        className="flex w-full cursor-pointer items-center gap-1.5 px-3 py-1.5 hover:bg-accent"
                      >
                        <AtSign className="size-3 text-text-faint" />
                        <span className="text-text">{a.display}</span>
                        {a.role && (
                          <span className="ml-auto text-[11px] text-text-faint truncate max-w-[55%]">{a.role}</span>
                        )}
                      </button>
                    ))}
                  </div>
                </div>
              )}
            </div>
          )}
          {scope.kind === "thread" && (
            <div className="mt-1 border-t border-border px-3 py-1.5 text-[10.5px] text-text-muted">
              Inherited from channel.
            </div>
          )}
        </div>
      )}
    </div>
  );
}
