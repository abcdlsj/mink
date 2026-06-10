import { useStore } from "@/lib/store";
import { relTime } from "@/lib/utils";
import { Dot } from "./LeftPane";
import { personaForActiveAgent } from "./Message/message-helpers";

export function EmptyState() {
  const view = useStore((s) => s.view);
  const detail = useStore((s) => s.detail);
  const agents = useStore((s) => s.agents);
  const agentDMs = useStore((s) => s.agentDMs);
  const activeAgentSpace = useStore((s) => s.activeAgentSpace);
  const activeChannel = useStore((s) => s.activeChannel);
  const threads = useStore((s) => s.threads);
  const channels = useStore((s) => s.channels);
  const openThread = useStore((s) => s.openThread);

  if (view === "channel") {
    const ch = channels.find((c) => c.id === activeChannel);
    return (
      <div className="text-text-faint text-[13px] py-12 text-center">
        Start in #{ch?.name || "channel"}.
      </div>
    );
  }
  if (view === "direct") {
    return (
      <div className="text-text-faint text-[13px] py-12 text-center">
        Start this conversation.
      </div>
    );
  }

  const ag = personaForActiveAgent(agents, agentDMs, activeAgentSpace, detail?.item.persona_id);
  const recent = threads.slice(0, 3);

  return (
    <div className="py-6">
      <div className="mb-6 border-l-2 border-border-soft px-3 text-[13px] leading-[1.6] text-text-muted">
        No messages yet. Message {ag?.display || detail?.item?.persona_name || "this agent"} directly.
      </div>
      {recent.length > 0 && (
        <div>
          <div className="mb-2 border-b border-border font-display text-[10px] font-black uppercase tracking-[1px] text-text">
            Recently with {ag?.display || "this agent"}
          </div>
          <div className="flex flex-col gap-1">
            {recent.map((t) => {
              const ch = channels.find((c) => c.id === t.channel_id);
              return (
                <button
                  key={t.id}
                  onClick={() => void openThread(t.id)}
                  className="flex w-full cursor-pointer items-center justify-between gap-2 border-2 border-transparent px-2 py-1.5 text-left text-text-muted hover:border-border hover:bg-panel-2 hover:text-text"
                >
                  <span className="flex items-center gap-1.5 text-[12.5px] text-text min-w-0">
                    {t.has_running && <Dot status="running" />}
                    <span className="truncate">{t.title}</span>
                  </span>
                  <span className="text-[11px] text-text-faint shrink-0">
                    {ch ? `#${ch.name} · ` : ""}
                    {relTime(t.updated_at)}
                  </span>
                </button>
              );
            })}
          </div>
        </div>
      )}
    </div>
  );
}
