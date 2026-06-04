import { Dot } from "../LeftPane";

export function WorkingAgents({ agents }: { agents: { id: string; display: string }[] }) {
  if (agents.length === 0) return null;
  return (
    <div className="flex min-w-0 flex-wrap items-center gap-1.5">
      {agents.map((agent) => (
        <span
          key={agent.id}
          className="inline-flex h-7 max-w-[180px] items-center gap-1.5 border border-border bg-panel-event px-2 text-[11.5px] text-text-muted"
          title={agent.display + " working"}
        >
          <Dot status="running" />
          <span className="truncate">
            <span className="text-text">{agent.display}</span> working
          </span>
        </span>
      ))}
    </div>
  );
}
