import {
  Archive,
  ChevronDown,
  MessageCircle,
  PanelLeftClose,
  Plus,
  RotateCw,
  UsersRound,
} from "lucide-react";
import type { useBootstrap } from "../hooks/useBootstrap";
import type { SpacesState, useSpaces } from "../hooks/useSpaces";
import { SpaceKind, type Space } from "../gen/sumi/space/v1/space_pb";
import { CreateSpaceForm } from "./CreateSpaceForm";
import { IconButton } from "./IconButton";
import { useState, type ReactNode } from "react";

type Bootstrap = ReturnType<typeof useBootstrap>;
type Spaces = ReturnType<typeof useSpaces>;

export function ConversationNavigation({
  bootstrap,
  spaces,
  currentHumanId,
  selected,
  onSelect,
  onClose,
}: {
  bootstrap: Bootstrap;
  spaces: Spaces;
  currentHumanId?: string;
  selected?: string;
  onSelect: (space: Space) => void;
  onClose: () => void;
}) {
  const [creating, setCreating] = useState(false);
  const active = spaces.data?.spaces.filter((space) => !space.archivedAt) || [];
  const dms = active.filter((space) => space.kind === SpaceKind.DM);
  const groups = active.filter((space) => space.kind === SpaceKind.GROUP);
  const archived =
    spaces.data?.spaces.filter((space) => !!space.archivedAt) || [];
  const canCreate = spaces.data?.createSpace.status === "allowed";

  return (
    <>
      <header className="nav-header">
        <div>
          <span className="eyebrow">Workspace</span>
          <strong>{spaces.data?.organization.name || "Sumi"}</strong>
        </div>
        <IconButton
          label="Collapse navigation"
          tooltipPlacement="left"
          onClick={onClose}
        >
          <PanelLeftClose size={18} />
        </IconButton>
      </header>
      <div className="conversation-nav-toolbar">
        <span>
          {spaces.data
            ? `${spaces.data.spaces.length} conversations`
            : "Collaboration"}
        </span>
        <div>
          <IconButton
            className="compact"
            label="Refresh conversations"
            onClick={() => void spaces.refresh()}
            disabled={
              spaces.status === "loading" || spaces.status === "refreshing"
            }
          >
            <RotateCw size={15} />
          </IconButton>
          <IconButton
            className="compact"
            label="Create conversation"
            tooltip={permissionTitle(spaces)}
            onClick={() => setCreating(true)}
            disabled={!canCreate || creating}
          >
            <Plus size={16} />
          </IconButton>
        </div>
      </div>
      {creating && spaces.data && currentHumanId && (
        <CreateSpaceForm
          directory={spaces.data}
          currentHumanId={currentHumanId}
          onCreateDM={spaces.createDirectMessage}
          onCreateGroup={spaces.createGroupSpace}
          onCreated={onSelect}
          onClose={() => setCreating(false)}
        />
      )}
      <nav
        className="nav-groups conversation-groups"
        aria-label="Conversations"
      >
        <SpacesFeedback state={spaces} />
        {spaces.data && (
          <>
            <SpaceSection
              icon={<MessageCircle size={14} />}
              label="Direct messages"
              spaces={dms}
              empty="No direct messages"
              selected={selected}
              onSelect={onSelect}
            />
            <SpaceSection
              icon={<UsersRound size={14} />}
              label="Spaces"
              spaces={groups}
              empty="No active Spaces"
              selected={selected}
              onSelect={onSelect}
            />
            <SpaceSection
              icon={<Archive size={14} />}
              label="Archived"
              spaces={archived}
              empty="No archived Spaces"
              selected={selected}
              onSelect={onSelect}
            />
            <section className="honest-empty-section">
              <strong>Work</strong>
              <p>No active work</p>
            </section>
          </>
        )}
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

function SpaceSection({
  icon,
  label,
  spaces,
  empty,
  selected,
  onSelect,
}: {
  icon: ReactNode;
  label: string;
  spaces: Space[];
  empty: string;
  selected?: string;
  onSelect: (space: Space) => void;
}) {
  return (
    <details className="nav-section" open>
      <summary>
        <span>
          {icon}
          <strong>{label}</strong>
        </span>
        <ChevronDown size={14} />
      </summary>
      {spaces.length === 0 ? (
        <p>{empty}</p>
      ) : (
        <div className="space-nav-list">
          {spaces.map((space) => (
            <button
              type="button"
              className={`space-nav-row ${selected === space.id ? "selected" : ""}`}
              onClick={() => onSelect(space)}
              key={space.id}
            >
              <span>
                {space.kind === SpaceKind.DM ? (
                  <MessageCircle size={14} />
                ) : (
                  <UsersRound size={14} />
                )}
              </span>
              <strong>
                {space.name ||
                  (space.kind === SpaceKind.DM
                    ? "Direct message"
                    : "Unnamed Space")}
              </strong>
              {space.archivedAt && <small>Archived</small>}
            </button>
          ))}
        </div>
      )}
    </details>
  );
}

function SpacesFeedback({ state }: { state: SpacesState }) {
  if (state.status === "loading" || state.status === "retrying") {
    return (
      <div className="navigation-feedback" role="status">
        <strong>Loading conversations</strong>
        <p>Reading authenticated Space facts.</p>
      </div>
    );
  }
  if (state.status === "error") {
    return (
      <div className="navigation-feedback error" role="alert">
        <strong>Collaboration unavailable</strong>
        <p>{state.error}</p>
      </div>
    );
  }
  if (state.status === "stale" || state.status === "refreshing") {
    return (
      <div className={`stale-notice ${state.status}`}>
        <span>{state.status === "stale" ? state.error : "Refreshing…"}</span>
      </div>
    );
  }
  return null;
}

function permissionTitle(spaces: Spaces) {
  const permission = spaces.data?.createSpace;
  if (!permission) return "Create permission is loading";
  if (permission.status === "allowed") return "Create DM or Group";
  if (permission.status === "denied") return "Create Space permission denied";
  return `Create Space permission unknown: ${permission.error}`;
}

function serverStatus(status: Bootstrap["status"]) {
  if (status === "ready") return "Server connected";
  if (status === "offline") return "Server offline";
  if (status === "retrying") return "Retrying connection";
  return "Connecting to server";
}
