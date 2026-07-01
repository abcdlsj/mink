import { AtSign, MessageCircle, Plus } from "lucide-react";
import { Identicon } from "@/components/Identicon";
import { Button } from "@/components/ui/button";
import { useStore } from "@/lib/store";
import type { AgentItem, PersonaItem } from "@/lib/types";
import { cn } from "@/lib/utils";
import { shortenText } from "@/lib/task-helpers";

export function AgentsPanel() {
  const agents = useStore((s) => s.agents);
  const personas = useStore((s) => s.personas);
  const directChats = useStore((s) => s.directChats);
  const openAgentDetail = useStore((s) => s.openAgentDetail);
  const openAgent = useStore((s) => s.openAgent);
  const newAgentChat = useStore((s) => s.newAgentChat);
  const personaByID = new Map(personas.map((p) => [p.id, p]));
  const cards = agents.map((agent) => ({
    agent,
    persona: personaByID.get(agent.id),
    defaultDM: directChats.find((dm) => dm.kind === "agent_dm" && dm.persona_id === agent.id),
  }));

  return (
    <main className="h-full min-w-0 overflow-y-auto bg-panel px-4 py-5 md:px-6">
      <div className="mx-auto max-w-[1040px]">
        <header className="mb-5 border-b-hard border-border pb-4">
          <div className="flex flex-wrap items-end justify-between gap-3">
            <div>
              <div className="font-display text-[24px] font-extrabold tracking-tight text-text">Agents</div>
              <div className="mt-1 max-w-[620px] text-[13px] leading-[20px] text-text-muted">
                Agent directory. Pick an agent, open its profile, or start a focused conversation.
              </div>
            </div>
            <div className="border border-border-soft bg-panel-2 px-2 py-1 font-mono text-[11px] text-text-muted">
              {cards.length} available
            </div>
          </div>
        </header>

        {cards.length === 0 ? (
          <div className="border border-dashed border-border-soft bg-panel-2 px-4 py-8 text-[13px] text-text-muted">
            No agents configured.
          </div>
        ) : (
          <div className="grid gap-3 md:grid-cols-2">
            {cards.map(({ agent, persona, defaultDM }) => {
              const display = persona?.display || agent.display || agent.id;
              const summary = agentSummary(agent, persona);
              const status = agentStatus(agent, !!defaultDM);
              return (
                <article
                  key={agent.id}
                  className="group border-hard border-border bg-panel-2 p-3 shadow-card transition-colors hover:bg-panel"
                >
                  <button
                    type="button"
                    onClick={() => openAgentDetail(agent.id)}
                    className="grid w-full grid-cols-[44px_minmax(0,1fr)] gap-3 text-left"
                    title={"Open @" + display + " profile"}
                  >
                    <span className="size-11 overflow-hidden border border-agent-border bg-agent-bg">
                      <Identicon seed={agent.id} kind="agent" />
                    </span>
                    <span className="min-w-0">
                      <span className="flex min-w-0 items-center gap-2">
                        <span className="truncate font-display text-[16px] font-extrabold text-text">{display}</span>
                        <span className={cn(
                          "shrink-0 border px-1.5 py-px font-mono text-[10px] font-medium uppercase",
                          status.tone === "running"
                            ? "border-running-border bg-running-bg text-running"
                            : "border-border-soft bg-panel text-text-muted",
                        )}>
                          {status.label}
                        </span>
                      </span>
                      <span className="mt-1 line-clamp-1 text-[12.5px] leading-[18px] text-text-muted">
                        {summary}
                      </span>
                    </span>
                  </button>

                  <div className="mt-3 flex flex-wrap items-center gap-2">
                    <Button variant="default" size="sm" onClick={() => openAgentDetail(agent.id)}>
                      <AtSign className="size-3" />
                      <span>Profile</span>
                    </Button>
                    <Button variant="default" size="sm" onClick={() => void openAgent(agent.id)}>
                      <MessageCircle className="size-3" />
                      <span>DM</span>
                    </Button>
                    <Button variant="primary" size="sm" onClick={() => void newAgentChat(agent.id)}>
                      <Plus className="size-3" />
                      <span>New chat</span>
                    </Button>
                  </div>
                </article>
              );
            })}
          </div>
        )}
      </div>
    </main>
  );
}

function agentSummary(agent: AgentItem, persona?: PersonaItem): string {
  const role = shortenText(agent.role || persona?.description || "", 84);
  if (role) return role;
  const caps = persona?.capabilities || [];
  if (caps.length) return caps.slice(0, 2).join(" · ");
  return persona?.runtime || agent.runtime || "default runtime";
}

function agentStatus(agent: AgentItem, hasDefaultDM: boolean): { label: string; tone: "running" | "idle" } {
  const raw = (agent.status || "").toLowerCase();
  if (raw === "running" || raw === "working" || raw === "queued") {
    return { label: raw, tone: "running" };
  }
  return { label: hasDefaultDM ? "dm ready" : "ready", tone: "idle" };
}
