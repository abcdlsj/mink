import {
  Bot,
  FileStack,
  KeyRound,
  ListTodo,
  MessageSquare,
  MonitorCog,
  Moon,
  Sun,
} from "lucide-react";
import { useTheme } from "../lib/theme";
import { IconButton } from "./IconButton";

export type WorkspaceModule = "conversation" | "agents" | "computers";
export type ConversationView = "chat" | "work" | "artifacts" | "authority";

export function PrimaryRail({
  active,
  conversationView,
  factsAvailable,
  onSelect,
  onSelectConversationView,
}: {
  active: WorkspaceModule;
  conversationView: ConversationView;
  factsAvailable: boolean;
  onSelect: (module: WorkspaceModule) => void;
  onSelectConversationView: (view: ConversationView) => void;
}) {
  const { theme, toggle } = useTheme();

  return (
    <nav className="icon-rail" aria-label="Primary">
      <div className="brand-mark" aria-label="Sumi">
        S
      </div>
      <div className="rail-actions">
        <RailButton
          label="Chat"
          active={active === "conversation" && conversationView === "chat"}
          onClick={() => onSelectConversationView("chat")}
        >
          <MessageSquare size={20} strokeWidth={1.8} />
        </RailButton>
        <RailButton
          label="Work"
          active={active === "conversation" && conversationView === "work"}
          onClick={() => onSelectConversationView("work")}
        >
          <ListTodo size={20} strokeWidth={1.8} />
        </RailButton>
        <RailButton
          label="Artifacts"
          active={active === "conversation" && conversationView === "artifacts"}
          onClick={() => onSelectConversationView("artifacts")}
        >
          <FileStack size={20} strokeWidth={1.8} />
        </RailButton>
        <RailButton
          label="Authority"
          active={active === "conversation" && conversationView === "authority"}
          onClick={() => onSelectConversationView("authority")}
        >
          <KeyRound size={20} strokeWidth={1.8} />
        </RailButton>
        {factsAvailable && (
          <>
            <span className="rail-divider" />
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
      <div className="rail-utilities">
        <IconButton
          className="rail-button"
          label={theme === "light" ? "Use dark theme" : "Use light theme"}
          tooltipPlacement="right"
          onClick={toggle}
        >
          {theme === "light" ? (
            <Moon size={18} strokeWidth={1.8} />
          ) : (
            <Sun size={18} strokeWidth={1.8} />
          )}
        </IconButton>
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
    <IconButton
      className={`rail-button ${active ? "active" : ""}`}
      label={label}
      tooltipPlacement="right"
      aria-current={active ? "page" : undefined}
      onClick={onClick}
    >
      {children}
    </IconButton>
  );
}
