import { AtSign, MessageCircle, Plus } from "lucide-react";
import { Button } from "@/components/ui/button";
import { useStore } from "@/lib/store";
import { MemoryOverviewCard } from "./MemoryOverviewCard";

export function AgentDetailPane() {
  const agentID = useStore((s) => s.activeAgentID);
  const agents = useStore((s) => s.agents);
  const personas = useStore((s) => s.personas);
  const directChats = useStore((s) => s.directChats);
  const agentDMs = useStore((s) => s.agentDMs);
  const openAgent = useStore((s) => s.openAgent);
  const newAgentChat = useStore((s) => s.newAgentChat);
  const agent = agents.find((a) => a.id === agentID);
  const persona = personas.find((p) => p.id === agentID);
  if (!agentID || (!agent && !persona)) {
    return (
      <main className="h-full min-w-0 bg-panel px-6 py-6 text-[13px] leading-[20px] text-text-muted">
        <div className="font-semibold text-text">Agent not found.</div>
        <div>Pick another agent from Direct Messages.</div>
      </main>
    );
  }
  const display = persona?.display || agent?.display || agentID;
  const defaultDM = directChats.find((d) => d.kind === "agent_dm" && d.persona_id === agentID);
  const namedChats = agentDMs.filter((d) => d.persona_id === agentID);
  const runtime = persona?.runtime || agent?.runtime || "default";
  const model = persona?.model || agent?.model || "";
  const caps = persona?.capabilities || [];
  const tools = persona?.tools || [];
  return (
    <main className="h-full min-w-0 overflow-y-auto bg-panel px-5 py-5">
      <div className="mx-auto max-w-[880px]">
        <div className="border-b-hard border-border pb-4">
          <div className="flex items-start justify-between gap-3">
            <div>
              <div className="flex items-center gap-2 font-display text-[24px] font-extrabold text-text">
                <AtSign className="size-5 text-text-muted" />
                <span>{display}</span>
                <span className="border border-border-soft bg-panel-2 px-1.5 py-px font-mono text-[10.5px] font-semibold uppercase tracking-[0.2px] text-text">
                  Agent Profile
                </span>
              </div>
              <div className="mt-1 font-mono text-[11px] text-text-faint">{agentID}</div>
            </div>
            <div className="flex gap-2">
              <Button variant="default" onClick={() => void openAgent(defaultDM?.id || agentID)}>
                <MessageCircle className="size-3" />
                <span>Open default DM</span>
              </Button>
              <Button variant="primary" onClick={() => void newAgentChat(agentID)}>
                <Plus className="size-3" />
                <span>New Agent Chat</span>
              </Button>
            </div>
          </div>
          {(persona?.description || agent?.role) && (
            <div className="mt-4 max-w-[680px] text-[14px] leading-[22px] text-text-muted">
              {persona?.description || agent?.role}
            </div>
          )}
        </div>

        <div className="grid gap-4 py-5 md:grid-cols-2">
          <InfoCard title="Runtime">
            <InfoLine label="runtime" value={runtime} />
            {model && <InfoLine label="model" value={model} />}
            <InfoLine label="status" value={agent?.status || "idle"} />
          </InfoCard>
          <InfoCard title="Task Policy">
            <InfoLine label="policy" value={persona?.task_policy || "default"} />
            <InfoLine label="memory scopes" value={`persona:${agentID} + workspace + global`} />
            <InfoLine label="sidebar" value={persona?.show_in_sidebar === false ? "hidden" : "visible"} />
          </InfoCard>
          <InfoCard title="Capabilities">
            <PillList items={caps.length ? caps : ["none configured"]} />
          </InfoCard>
          <InfoCard title="Tools">
            <PillList items={tools.length ? tools : ["runtime default"]} />
          </InfoCard>
        </div>

        <InfoCard title="Memory">
          <MemoryOverviewCard personaID={agentID} />
        </InfoCard>

        <InfoCard title="Recent Agent Chats">
          {namedChats.length === 0 && !defaultDM ? (
            <div className="text-[12.5px] leading-[19px] text-text-faint">
              No chats yet. Use Message for the default DM, or New chat for a topic.
            </div>
          ) : (
            <div className="flex flex-col gap-1.5">
              {defaultDM && (
                <button
                  type="button"
                  onClick={() => void openAgent(defaultDM.id)}
                  className="flex items-center justify-between border border-border bg-panel-2 px-2.5 py-2 text-left text-[12.5px] text-text-muted hover:text-text"
                >
                  <span>Default DM</span>
                  <span className="font-mono text-[10.5px] text-text-faint">Default Agent DM</span>
                </button>
              )}
              {namedChats.slice(0, 6).map((chat) => (
                <button
                  key={chat.id}
                  type="button"
                  onClick={() => void openAgent(chat.id)}
                  className="flex items-center justify-between border border-border bg-panel-2 px-2.5 py-2 text-left text-[12.5px] text-text-muted hover:text-text"
                >
                  <span className="truncate">{chat.title || "New chat"}</span>
                  <span className="shrink-0 font-mono text-[10.5px] text-text-faint">{chat.message_count} msgs</span>
                </button>
              ))}
            </div>
          )}
        </InfoCard>
      </div>
    </main>
  );
}

function InfoCard({ title, children }: { title: string; children: React.ReactNode }) {
  return (
    <section className="border border-border bg-panel-2 px-3 py-3">
      <div className="mb-2 font-display text-[11px] font-extrabold uppercase text-text">{title}</div>
      {children}
    </section>
  );
}

function InfoLine({ label, value }: { label: string; value: string }) {
  return (
    <div className="flex items-center justify-between gap-3 border-b border-border-soft py-1.5 last:border-b-0">
      <span className="font-mono text-[10.5px] font-medium uppercase text-text-muted">{label}</span>
      <span className="truncate text-[12.5px] text-text">{value}</span>
    </div>
  );
}

function PillList({ items }: { items: string[] }) {
  return (
    <div className="flex flex-wrap gap-1.5">
      {items.map((item) => (
        <span key={item} className="border border-border bg-panel px-1.5 py-px font-mono text-[10.5px] font-medium text-text">
          {item}
        </span>
      ))}
    </div>
  );
}
