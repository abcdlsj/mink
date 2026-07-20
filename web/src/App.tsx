import { useState, type ReactNode } from "react";
import {
  ArrowLeft,
  Archive,
  ChevronDown,
  CircleUserRound,
  FileStack,
  Inbox,
  MessageSquare,
  PanelLeftClose,
  PanelLeftOpen,
  PanelRightClose,
  PanelRightOpen,
  Plus,
  RotateCw,
  Search,
  Send,
  UsersRound,
} from "lucide-react";
import { useBootstrap } from "./hooks/useBootstrap";
import "./styles.css";

type View = "chat" | "work" | "artifacts";
type ConnectionStatus = "loading" | "retrying" | "ready" | "offline";

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

export default function App() {
  const bootstrap = useBootstrap();
  const [view, setView] = useState<View>("chat");
  const [navigationOpen, setNavigationOpen] = useState(
    () => window.innerWidth >= 1280,
  );
  const [contextOpen, setContextOpen] = useState(false);
  const [compactContextOpen, setCompactContextOpen] = useState(false);
  const empty = viewCopy[view];

  return (
    <main
      className={`app-shell ${navigationOpen ? "" : "navigation-collapsed"} ${contextOpen ? "" : "context-collapsed"} ${compactContextOpen ? "compact-context-open" : ""}`}
    >
      <nav className="icon-rail" aria-label="Primary">
        <div className="brand-mark" aria-label="Sumi">
          S
        </div>
        <div className="rail-actions">
          <button
            className="icon-button rail-button active"
            type="button"
            aria-label="Chat"
            title="Chat"
            onClick={() => setNavigationOpen((open) => !open)}
          >
            <MessageSquare size={20} strokeWidth={1.8} />
          </button>
        </div>
      </nav>

      <aside className="secondary-nav" aria-label="Conversation navigation">
        <header className="nav-header">
          <div>
            <span className="eyebrow">Workspace</span>
            <strong>Sumi</strong>
          </div>
          <button
            className="icon-button"
            type="button"
            aria-label="Collapse navigation"
            title="Collapse navigation"
            onClick={() => setNavigationOpen(false)}
          >
            <PanelLeftClose size={18} />
          </button>
        </header>
        <nav className="nav-groups">
          <NavGroup icon={<Inbox size={15} />} label="Inbox" value="Empty" />
          <NavGroup icon={<Archive size={15} />} label="Saved" value="None" />
          <NavGroup
            icon={<FileStack size={15} />}
            label="Pinned"
            value="None"
          />
          <NavSection label="Spaces" empty="No spaces yet" />
          <NavSection label="Direct messages" empty="No direct messages" />
          <NavSection label="Work" empty="No active work" />
        </nav>
        <div className="nav-runtime">
          <span className={`status-dot ${bootstrap.status}`} />
          <div>
            <strong>Local workspace</strong>
            <span>{serverStatus(bootstrap.status)}</span>
          </div>
        </div>
      </aside>

      <section className="conversation" aria-label="Conversation">
        <header className="topbar">
          <div className="topbar-leading">
            {!navigationOpen && (
              <button
                className="icon-button"
                type="button"
                aria-label="Open navigation"
                title="Open navigation"
                onClick={() => setNavigationOpen(true)}
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
            <button
              className="icon-button compact-context-trigger"
              type="button"
              aria-label="Open context"
              title="Open context"
              onClick={() => setCompactContextOpen(true)}
            >
              <PanelRightOpen size={18} />
            </button>
            {!contextOpen && (
              <button
                className="icon-button desktop-context-trigger"
                type="button"
                aria-label="Open context"
                title="Open context"
                onClick={() => setContextOpen(true)}
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

        <div
          className="tab-strip"
          role="tablist"
          aria-label="Conversation views"
        >
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

        <footer className="statusbar">
          <span>
            <CircleUserRound size={14} /> Local Human
          </span>
          <span className="status-separator" />
          <span className={`server-state ${bootstrap.status}`}>
            <span className={`status-dot ${bootstrap.status}`} />
            {bootstrapLabel(bootstrap.status, bootstrap.value?.serverId)}
          </span>
          {bootstrap.status === "ready" && (
            <span>v{bootstrap.value.version}</span>
          )}
        </footer>
      </section>

      <aside className="context-pane" aria-label="Context">
        <header className="context-header">
          <button
            className="icon-button compact-context-back"
            type="button"
            aria-label="Back to conversation"
            title="Back to conversation"
            onClick={() => setCompactContextOpen(false)}
          >
            <ArrowLeft size={18} />
          </button>
          <div>
            <span className="eyebrow">Current space</span>
            <strong>Context</strong>
          </div>
          <button
            className="icon-button desktop-context-close"
            type="button"
            aria-label="Close context"
            title="Close context"
            onClick={() => setContextOpen(false)}
          >
            <PanelRightClose size={18} />
          </button>
        </header>
        <div className="context-content context-empty">
          <p>No context selected</p>
        </div>
      </aside>
    </main>
  );
}

function NavGroup({
  icon,
  label,
  value,
}: {
  icon: ReactNode;
  label: string;
  value: string;
}) {
  return (
    <button className="nav-row" type="button" disabled>
      <span>{icon}</span>
      <strong>{label}</strong>
      <small>{value}</small>
    </button>
  );
}

function NavSection({ label, empty }: { label: string; empty: string }) {
  return (
    <section className="nav-section">
      <header>
        <strong>{label}</strong>
        <ChevronDown size={14} />
      </header>
      <p>{empty}</p>
    </section>
  );
}

function capitalize(value: string) {
  return value.charAt(0).toUpperCase() + value.slice(1);
}

function emptyIcon(view: View) {
  if (view === "work") return <Archive size={30} strokeWidth={1.6} />;
  if (view === "artifacts") return <FileStack size={30} strokeWidth={1.6} />;
  return <MessageSquare size={30} strokeWidth={1.6} />;
}

function serverStatus(status: ConnectionStatus) {
  if (status === "ready") return "Server connected";
  if (status === "offline") return "Server offline";
  if (status === "retrying") return "Retrying connection";
  return "Connecting to server";
}

function bootstrapLabel(status: ConnectionStatus, id?: string) {
  if (status === "offline") return "Server offline";
  if (status === "retrying") return "Retrying";
  if (status === "loading") return "Connecting";
  return `Server ${id?.slice(0, 8)}`;
}
