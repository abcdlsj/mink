import {
  Archive,
  ChevronDown,
  FileStack,
  Inbox,
  PanelLeftClose,
} from "lucide-react";
import type { ReactNode } from "react";
import type { useBootstrap } from "../hooks/useBootstrap";

type Bootstrap = ReturnType<typeof useBootstrap>;

export function ConversationNavigation({
  bootstrap,
  onClose,
}: {
  bootstrap: Bootstrap;
  onClose: () => void;
}) {
  return (
    <>
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
          onClick={onClose}
        >
          <PanelLeftClose size={18} />
        </button>
      </header>
      <nav className="nav-groups">
        <NavGroup icon={<Inbox size={15} />} label="Inbox" value="Empty" />
        <NavGroup icon={<Archive size={15} />} label="Saved" value="None" />
        <NavGroup icon={<FileStack size={15} />} label="Pinned" value="None" />
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
    </>
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
    <div className="nav-row">
      <span>{icon}</span>
      <strong>{label}</strong>
      <small>{value}</small>
    </div>
  );
}

function NavSection({ label, empty }: { label: string; empty: string }) {
  return (
    <details className="nav-section" open>
      <summary>
        <strong>{label}</strong>
        <ChevronDown size={14} />
      </summary>
      <p>{empty}</p>
    </details>
  );
}

function serverStatus(status: Bootstrap["status"]) {
  if (status === "ready") return "Server connected";
  if (status === "offline") return "Server offline";
  if (status === "retrying") return "Retrying connection";
  return "Connecting to server";
}
