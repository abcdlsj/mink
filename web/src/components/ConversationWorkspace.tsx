import { useState, type ReactNode } from "react";
import {
  Archive,
  FileStack,
  MessageSquare,
  PanelLeftOpen,
  PanelRightOpen,
  Plus,
  RotateCw,
  Search,
  Send,
  UsersRound,
} from "lucide-react";
import type { useBootstrap } from "../hooks/useBootstrap";

type View = "chat" | "work" | "artifacts";
type Bootstrap = ReturnType<typeof useBootstrap>;

const viewCopy: Record<View, { title: string; detail: string }> = {
  chat: {
    title: "No conversations yet",
    detail: "Your direct messages and spaces will appear here.",
  },
  work: {
    title: "No work in this space",
    detail: "Work begins only after an explicit commitment.",
  },
  artifacts: {
    title: "No artifacts published",
    detail: "Versioned results will remain linked to their source work.",
  },
};

export function ConversationWorkspace({
  bootstrap,
  navigationOpen,
  contextOpen,
  onOpenNavigation,
  onOpenContext,
}: {
  bootstrap: Bootstrap;
  navigationOpen: boolean;
  contextOpen: boolean;
  onOpenNavigation: () => void;
  onOpenContext: () => void;
}) {
  const [view, setView] = useState<View>("chat");
  const empty = viewCopy[view];

  return (
    <section className="conversation workspace-main" aria-label="Conversation">
      <header className="topbar">
        <div className="topbar-leading">
          {!navigationOpen && (
            <button
              className="icon-button"
              type="button"
              aria-label="Open navigation"
              title="Open navigation"
              onClick={onOpenNavigation}
            >
              <PanelLeftOpen size={18} />
            </button>
          )}
          <label className="search-field">
            <Search size={17} />
            <input type="search" placeholder="Search Sumi" disabled />
          </label>
        </div>
        <div className="topbar-actions">
          <ServerIndicator bootstrap={bootstrap} />
          <button
            className="icon-button compact-context-trigger"
            type="button"
            aria-label="Open context"
            title="Open context"
            onClick={onOpenContext}
          >
            <PanelRightOpen size={18} />
          </button>
          {!contextOpen && (
            <button
              className="icon-button desktop-context-trigger"
              type="button"
              aria-label="Open context"
              title="Open context"
              onClick={onOpenContext}
            >
              <PanelRightOpen size={18} />
            </button>
          )}
        </div>
      </header>

      <header className="space-header">
        <div>
          <span className="eyebrow">Conversation</span>
          <h1>Sumi workspace</h1>
        </div>
        <div className="space-presence">
          <UsersRound size={17} />
          <span>Local Human</span>
        </div>
      </header>

      <div className="tab-strip" role="tablist" aria-label="Conversation views">
        {(["chat", "work", "artifacts"] as const).map((item) => (
          <button
            className={view === item ? "selected" : ""}
            type="button"
            role="tab"
            aria-selected={view === item}
            onClick={() => setView(item)}
            key={item}
          >
            {capitalize(item)}
          </button>
        ))}
      </div>

      <div className="timeline" data-testid="timeline">
        {bootstrap.status === "loading" ? (
          <div className="loading-state" role="status">
            <span className="status-dot loading" />
            <div>
              <h2>Connecting to Sumi</h2>
              <p>Waiting for the local Server.</p>
            </div>
          </div>
        ) : bootstrap.status === "offline" ||
          bootstrap.status === "retrying" ? (
          <div className="offline-state" role="alert">
            <span className="offline-mark">!</span>
            <div>
              <h2>Server unavailable</h2>
              <p>
                The conversation shell is offline. Existing local files are
                unchanged.
              </p>
            </div>
            <button
              className="command-button"
              type="button"
              onClick={bootstrap.retry}
              disabled={bootstrap.status === "retrying"}
            >
              <RotateCw size={16} />
              {bootstrap.status === "retrying" ? "Retrying" : "Retry"}
            </button>
          </div>
        ) : (
          <div className="empty-state">
            <span className="empty-icon">{emptyIcon(view)}</span>
            <h2>{empty.title}</h2>
            <p>{empty.detail}</p>
          </div>
        )}
      </div>

      <footer className="composer" data-testid="main-composer">
        <textarea
          aria-label="Message"
          placeholder="Connect an Agent to begin a conversation"
          disabled
          rows={1}
        />
        <div className="composer-toolbar">
          <div>
            <button
              className="icon-button compact"
              type="button"
              aria-label="Add attachment"
              title="Attachments are not available yet"
              disabled
            >
              <Plus size={18} />
            </button>
            <label className="work-toggle">
              <input type="checkbox" disabled />
              As Work
            </label>
          </div>
          <button
            className="icon-button compact send-button"
            type="button"
            aria-label="Send message"
            title="Messaging is not available yet"
            disabled
          >
            <Send size={17} />
          </button>
        </div>
      </footer>
    </section>
  );
}

export function ServerIndicator({ bootstrap }: { bootstrap: Bootstrap }) {
  return (
    <div
      className={`server-indicator ${bootstrap.status}`}
      title={bootstrapLabel(bootstrap.status, bootstrap.value?.serverId)}
    >
      <span className={`status-dot ${bootstrap.status}`} />
      <span>{bootstrapLabel(bootstrap.status, bootstrap.value?.serverId)}</span>
      {bootstrap.status === "ready" && (
        <small>v{bootstrap.value.version}</small>
      )}
    </div>
  );
}

function capitalize(value: string) {
  return value.charAt(0).toUpperCase() + value.slice(1);
}

function emptyIcon(view: View): ReactNode {
  if (view === "work") return <Archive size={30} strokeWidth={1.6} />;
  if (view === "artifacts") return <FileStack size={30} strokeWidth={1.6} />;
  return <MessageSquare size={30} strokeWidth={1.6} />;
}

function bootstrapLabel(status: Bootstrap["status"], id?: string) {
  if (status === "offline") return "Server offline";
  if (status === "retrying") return "Retrying";
  if (status === "loading") return "Connecting";
  return `Server ${id?.slice(0, 8)}`;
}
