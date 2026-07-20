import { Bot, MessageSquare, MonitorCog } from "lucide-react";

export type WorkspaceModule = "conversation" | "agents" | "computers";

export function PrimaryRail({
  active,
  factsAvailable,
  onSelect,
}: {
  active: WorkspaceModule;
  factsAvailable: boolean;
  onSelect: (module: WorkspaceModule) => void;
}) {
  return (
    <nav className="icon-rail" aria-label="Primary">
      <div className="brand-mark" aria-label="Sumi">
        S
      </div>
      <div className="rail-actions">
        <RailButton
          label="Chat"
          active={active === "conversation"}
          onClick={() => onSelect("conversation")}
        >
          <MessageSquare size={20} strokeWidth={1.8} />
        </RailButton>
        {factsAvailable && (
          <>
            <RailButton
              label="Agents"
              active={active === "agents"}
              onClick={() => onSelect("agents")}
            >
              <Bot size={20} strokeWidth={1.8} />
            </RailButton>
            <RailButton
              label="Computers"
              active={active === "computers"}
              onClick={() => onSelect("computers")}
            >
              <MonitorCog size={20} strokeWidth={1.8} />
            </RailButton>
          </>
        )}
      </div>
    </nav>
  );
}

function RailButton({
  label,
  active,
  children,
  onClick,
}: {
  label: string;
  active: boolean;
  children: React.ReactNode;
  onClick: () => void;
}) {
  return (
    <button
      className={`icon-button rail-button ${active ? "active" : ""}`}
      type="button"
      aria-label={label}
      aria-current={active ? "page" : undefined}
      title={label}
      onClick={onClick}
    >
      {children}
    </button>
  );
}
