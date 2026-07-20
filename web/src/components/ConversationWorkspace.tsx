import { useState, type ReactNode } from "react";
import {
  Archive,
  FileStack,
  LogOut,
  MessageSquare,
  PanelLeftOpen,
  PanelRightOpen,
  Plus,
  RotateCw,
  Search,
  Send,
  ShieldCheck,
  UsersRound,
} from "lucide-react";
import type { useBootstrap } from "../hooks/useBootstrap";
import type { useSession } from "../hooks/useSession";

type View = "chat" | "work" | "artifacts";
type Bootstrap = ReturnType<typeof useBootstrap>;
type Session = ReturnType<typeof useSession>;

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
  session,
  navigationOpen,
  contextOpen,
  onOpenNavigation,
  onOpenContext,
}: {
  bootstrap: Bootstrap;
  session: Session;
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
          <SessionIndicator session={session} />
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
          <span>
            {session.status === "authenticated" ||
            session.status === "logging-out"
              ? session.human.name
              : "Human session required"}
          </span>
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
        ) : session.status === "loading" || session.status === "retrying" ? (
          <div className="loading-state" role="status">
            <span className="status-dot loading" />
            <div>
              <h2>Checking Human session</h2>
              <p>Confirming browser access with the local Server.</p>
            </div>
          </div>
        ) : session.status === "error" ? (
          <div className="offline-state" role="alert">
            <span className="offline-mark">!</span>
            <div>
              <h2>Authentication unavailable</h2>
              <p>The Server is online, but session status could not be read.</p>
            </div>
            <button
              className="command-button"
              type="button"
              onClick={session.retry}
            >
              <RotateCw size={16} />
              Retry
            </button>
          </div>
        ) : session.status === "unauthenticated" ? (
          <div className="authentication-state" role="status">
            <span className="authentication-mark" aria-hidden="true">
              <ShieldCheck size={30} strokeWidth={1.7} />
            </span>
            <div>
              <h2>Authentication required</h2>
              <p>This browser has no active Human session.</p>
            </div>
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
          placeholder={
            session.status === "authenticated" ||
            session.status === "logging-out"
              ? "Connect an Agent to begin a conversation"
              : "Authenticate to begin a conversation"
          }
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

function SessionIndicator({ session }: { session: Session }) {
  if (session.status !== "authenticated" && session.status !== "logging-out") {
    const label = sessionLabel(session.status);
    return (
      <div className={`session-indicator ${session.status}`} aria-label={label}>
        <span className={`status-dot ${session.status}`} />
        <span>{label}</span>
      </div>
    );
  }
  return (
    <div className="session-control">
      <span className="status-dot ready" />
      <span className="session-name">{session.human.name}</span>
      <button
        className="icon-button compact"
        type="button"
        aria-label="Log out"
        title="Log out browser session"
        onClick={session.logout}
        disabled={session.status === "logging-out"}
      >
        <LogOut size={15} />
      </button>
    </div>
  );
}

function sessionLabel(status: Session["status"]) {
  if (status === "loading" || status === "retrying") return "Checking session";
  if (status === "error") return "Session unavailable";
  return "Human signed out";
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
