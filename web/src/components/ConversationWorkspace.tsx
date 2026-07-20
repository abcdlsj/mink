import { useState } from "react";
import {
  Archive,
  FileStack,
  LogOut,
  MessageSquare,
  PanelLeftOpen,
  PanelRightOpen,
  RotateCw,
  Search,
  Server,
  ShieldCheck,
  UserRound,
  UsersRound,
  WifiOff,
} from "lucide-react";
import type { useBootstrap } from "../hooks/useBootstrap";
import type { useConversation } from "../hooks/useConversation";
import type { useSession } from "../hooks/useSession";
import type { useSpaces } from "../hooks/useSpaces";
import type { Message, Space } from "../gen/sumi/space/v1/space_pb";
import { ConversationTimeline } from "./ConversationTimeline";
import { MessageComposer } from "./MessageComposer";

type View = "chat" | "work" | "artifacts";
type Bootstrap = ReturnType<typeof useBootstrap>;
type Session = ReturnType<typeof useSession>;
type Spaces = ReturnType<typeof useSpaces>;
type Conversation = ReturnType<typeof useConversation>;

const viewCopy: Record<
  Exclude<View, "chat">,
  { title: string; detail: string }
> = {
  work: {
    title: "No work in this Space",
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
  spaces,
  selectedSpace,
  conversation,
  navigationOpen,
  contextOpen,
  onOpenNavigation,
  onOpenContext,
  onOpenThread,
}: {
  bootstrap: Bootstrap;
  session: Session;
  spaces: Spaces;
  selectedSpace?: Space;
  conversation: Conversation;
  navigationOpen: boolean;
  contextOpen: boolean;
  onOpenNavigation: () => void;
  onOpenContext: () => void;
  onOpenThread: (message: Message) => void;
}) {
  const [view, setView] = useState<View>("chat");
  const snapshot = conversation.conversation.data;
  const visibleSpace = snapshot?.space || selectedSpace;
  const authenticated =
    session.status === "authenticated" || session.status === "logging-out";

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
            <input
              type="search"
              placeholder="Search is not available yet"
              disabled
            />
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
            disabled={!snapshot}
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
              disabled={!snapshot}
            >
              <PanelRightOpen size={18} />
            </button>
          )}
        </div>
      </header>

      <header className="space-header">
        <div>
          <span className="eyebrow">
            {visibleSpace ? "Conversation" : "Collaboration"}
          </span>
          <h1>
            {visibleSpace?.name ||
              (visibleSpace ? "Direct message" : "Sumi workspace")}
          </h1>
        </div>
        <div className="space-header-actions">
          {snapshot && (
            <span className="space-presence">
              <UsersRound size={17} />
              {snapshot.memberships.length} members
            </span>
          )}
          {visibleSpace && authenticated && (
            <button
              className="icon-button compact"
              type="button"
              aria-label="Refresh conversation"
              title="Refresh conversation"
              onClick={() => void conversation.refresh()}
              disabled={conversation.conversation.status === "refreshing"}
            >
              <RotateCw size={16} />
            </button>
          )}
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

      {renderContent({
        bootstrap,
        session,
        spaces,
        selectedSpace,
        conversation,
        view,
        onOpenThread,
      })}

      {view === "chat" && snapshot ? (
        <MessageComposer
          key={snapshot.space.id}
          targetKey={`space:${snapshot.space.id}`}
          label="main"
          placeholder="Message this Space"
          disabledReason={messageDisabledReason(
            snapshot.space.archivedAt !== undefined,
            snapshot.permissions.send,
          )}
          onSend={conversation.sendMain}
        />
      ) : (
        <footer
          className="composer disabled-composer"
          data-testid="main-composer"
        >
          <textarea
            aria-label="Message"
            placeholder={
              authenticated
                ? "Select an active Space to send a message"
                : "Authenticate to begin a conversation"
            }
            disabled
            rows={1}
          />
        </footer>
      )}
    </section>
  );
}

function renderContent({
  bootstrap,
  session,
  spaces,
  selectedSpace,
  conversation,
  view,
  onOpenThread,
}: {
  bootstrap: Bootstrap;
  session: Session;
  spaces: Spaces;
  selectedSpace?: Space;
  conversation: Conversation;
  view: View;
  onOpenThread: (message: Message) => void;
}) {
  if (bootstrap.status === "loading") {
    return (
      <StatePanel
        kind="loading"
        title="Connecting to Sumi"
        detail="Waiting for the local Server."
      />
    );
  }
  if (bootstrap.status === "offline" || bootstrap.status === "retrying") {
    return (
      <StatePanel
        kind="error"
        title="Server unavailable"
        detail="The conversation UI is offline. Existing local files are unchanged."
        action={bootstrap.retry}
        actionLabel={bootstrap.status === "retrying" ? "Retrying" : "Retry"}
        pending={bootstrap.status === "retrying"}
      />
    );
  }
  if (session.status === "loading" || session.status === "retrying") {
    return (
      <StatePanel
        kind="loading"
        title="Checking Human session"
        detail="Confirming browser access with the local Server."
      />
    );
  }
  if (session.status === "error") {
    return (
      <StatePanel
        kind="error"
        title="Authentication unavailable"
        detail="The Server is online, but session status could not be read."
        action={session.retry}
        actionLabel="Retry"
      />
    );
  }
  if (session.status === "unauthenticated") {
    return (
      <div className="timeline">
        <div className="authentication-state" role="status">
          <span className="authentication-mark" aria-hidden="true">
            <ShieldCheck size={30} strokeWidth={1.7} />
          </span>
          <div>
            <h2>Authentication required</h2>
            <p>This browser has no active Human session.</p>
          </div>
        </div>
      </div>
    );
  }
  if (
    !spaces.data &&
    (spaces.status === "loading" ||
      spaces.status === "retrying" ||
      spaces.status === "idle")
  ) {
    return (
      <StatePanel
        kind="loading"
        title="Loading collaboration"
        detail="Reading Spaces and authenticated directory facts."
      />
    );
  }
  if (!spaces.data && spaces.status === "error") {
    return (
      <StatePanel
        kind="error"
        title="Collaboration unavailable"
        detail={spaces.error || "Could not load Spaces."}
        action={spaces.retry}
        actionLabel="Retry"
      />
    );
  }
  if (!selectedSpace) {
    return (
      <div className="timeline">
        <div className="empty-state">
          <span className="empty-icon">
            <MessageSquare size={30} />
          </span>
          <h2>No conversation selected</h2>
          <p>Choose a DM or Space from navigation, or create one.</p>
        </div>
      </div>
    );
  }
  if (view !== "chat") {
    const empty = viewCopy[view];
    return (
      <div className="timeline">
        <div className="empty-state">
          <span className="empty-icon">
            {view === "work" ? <Archive size={30} /> : <FileStack size={30} />}
          </span>
          <h2>{empty.title}</h2>
          <p>{empty.detail}</p>
        </div>
      </div>
    );
  }
  const state = conversation.conversation;
  if (!state.data && state.status === "loading") {
    return (
      <StatePanel
        kind="loading"
        title="Loading conversation"
        detail="Reading Space, members, messages, and permission hints."
      />
    );
  }
  if (!state.data) {
    return (
      <StatePanel
        kind="error"
        title="Conversation unavailable"
        detail={state.error || "The selected Space could not be read."}
        action={conversation.refresh}
        actionLabel="Retry"
      />
    );
  }
  return (
    <ConversationTimeline
      snapshot={state.data}
      humans={spaces.data!.humans}
      agents={spaces.data!.agents}
      status={state.status as "ready" | "refreshing" | "stale"}
      error={state.error}
      onRefresh={() => void conversation.refresh()}
      onLoadMore={() => void conversation.loadMore()}
      onOpenThread={onOpenThread}
    />
  );
}

function StatePanel({
  kind,
  title,
  detail,
  action,
  actionLabel,
  pending,
}: {
  kind: "loading" | "error";
  title: string;
  detail: string;
  action?: () => void;
  actionLabel?: string;
  pending?: boolean;
}) {
  if (kind === "loading") {
    return (
      <div className="timeline">
        <div className="loading-state" role="status">
          <span className="status-dot loading" />
          <div>
            <h2>{title}</h2>
            <p>{detail}</p>
          </div>
        </div>
      </div>
    );
  }
  return (
    <div className="timeline">
      <div className="offline-state" role="alert">
        <span className="offline-mark">!</span>
        <div>
          <h2>{title}</h2>
          <p>{detail}</p>
        </div>
        {action && (
          <button
            className="command-button"
            type="button"
            onClick={action}
            disabled={pending}
          >
            <RotateCw size={16} />
            {actionLabel}
          </button>
        )}
      </div>
    </div>
  );
}

function SessionIndicator({ session }: { session: Session }) {
  if (session.status !== "authenticated" && session.status !== "logging-out") {
    const label = sessionLabel(session.status);
    return (
      <div
        className={`session-indicator ${session.status}`}
        aria-label={label}
        title={label}
      >
        <UserRound size={15} aria-hidden="true" />
        <span>{label}</span>
      </div>
    );
  }
  return (
    <div className="session-control">
      <UserRound size={15} aria-hidden="true" />
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
  const label = bootstrapLabel(bootstrap.status, bootstrap.value?.serverId);
  return (
    <div
      className={`server-indicator ${bootstrap.status}`}
      title={label}
      aria-label={label}
    >
      {bootstrap.status === "offline" ? (
        <WifiOff size={15} aria-hidden="true" />
      ) : (
        <Server size={15} aria-hidden="true" />
      )}
      <span>{label}</span>
      {bootstrap.status === "ready" && (
        <small>v{bootstrap.value.version}</small>
      )}
    </div>
  );
}

function messageDisabledReason(
  archived: boolean,
  permission: { status: "allowed" | "denied" | "unknown"; error?: string },
) {
  if (archived) return "Archived Spaces are read-only";
  if (permission.status === "denied") return "Message permission denied";
  if (permission.status === "unknown") {
    return `Message permission unknown${permission.error ? `: ${permission.error}` : ""}`;
  }
  return undefined;
}

function capitalize(value: string) {
  return value.charAt(0).toUpperCase() + value.slice(1);
}

function bootstrapLabel(status: Bootstrap["status"], id?: string) {
  if (status === "offline") return "Server offline";
  if (status === "retrying") return "Retrying";
  if (status === "loading") return "Connecting";
  return `Server ${id?.slice(0, 8)}`;
}
