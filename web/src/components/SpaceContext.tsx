import { useMemo, useRef, useState } from "react";
import {
  Archive,
  ArchiveRestore,
  ArrowLeft,
  UserMinus,
  UserPlus,
  X,
} from "lucide-react";
import {
  PrincipalKind,
  SpaceKind,
  type Principal,
} from "../gen/sumi/space/v1/space_pb";
import {
  PayloadRequestLifecycle,
  activeHumans,
  collaborationErrorMessage,
  principalForAgent,
  principalForHuman,
  type ConversationSnapshot,
  type DirectorySnapshot,
  type PermissionState,
} from "../lib/collaboration";

export function SpaceContext({
  conversation,
  directory,
  currentHumanId,
  onClose,
  onAdd,
  onRemove,
  onSetArchived,
}: {
  conversation: ConversationSnapshot;
  directory: DirectorySnapshot;
  currentHumanId: string;
  onClose: () => void;
  onAdd: (requestId: string, principal: Principal) => Promise<void>;
  onRemove: (requestId: string, principal: Principal) => Promise<void>;
  onSetArchived: (requestId: string, archived: boolean) => Promise<void>;
}) {
  const group = conversation.space.kind === SpaceKind.GROUP;
  const [candidate, setCandidate] = useState("");
  const [pending, setPending] = useState<"add" | "remove" | "archive">();
  const [error, setError] = useState<string>();
  const addLifecycle = useRef(
    new PayloadRequestLifecycle<{ spaceId: string; member: string }>(),
  ).current;
  const removeLifecycle = useRef(
    new PayloadRequestLifecycle<{ spaceId: string; member: string }>(),
  ).current;
  const archiveLifecycle = useRef(
    new PayloadRequestLifecycle<{ spaceId: string; archived: boolean }>(),
  ).current;
  const existing = useMemo(
    () =>
      new Set(
        conversation.memberships
          .map((membership) => membership.principal)
          .filter(Boolean)
          .map((principal) => `${principal!.kind}:${principal!.id}`),
      ),
    [conversation.memberships],
  );
  const candidates = useMemo(
    () =>
      [
        ...activeHumans(directory.humans)
          .filter((human) => human.id !== currentHumanId)
          .map((human) => ({
            value: `${PrincipalKind.HUMAN}:${human.id}`,
            label: human.name,
            detail: "Human",
          })),
        ...directory.agents.map((agent) => ({
          value: `${PrincipalKind.AGENT}:${agent.id}`,
          label: agent.name,
          detail: "Agent",
        })),
      ].filter((candidate) => !existing.has(candidate.value)),
    [currentHumanId, directory.agents, directory.humans, existing],
  );

  const add = async () => {
    const principal = parsePrincipal(candidate);
    if (!principal) return;
    setPending("add");
    setError(undefined);
    try {
      await onAdd(
        addLifecycle.sync({
          spaceId: conversation.space.id,
          member: candidate,
        }),
        principal,
      );
      addLifecycle.complete();
      setCandidate("");
    } catch (cause) {
      setError(collaborationErrorMessage(cause, "add member"));
    } finally {
      setPending(undefined);
    }
  };

  const remove = async (principal: Principal) => {
    const member = `${principal.kind}:${principal.id}`;
    setPending("remove");
    setError(undefined);
    try {
      await onRemove(
        removeLifecycle.sync({ spaceId: conversation.space.id, member }),
        principal,
      );
      removeLifecycle.complete();
    } catch (cause) {
      setError(collaborationErrorMessage(cause, "remove member"));
    } finally {
      setPending(undefined);
    }
  };

  const setArchived = async () => {
    const archived = !conversation.space.archivedAt;
    setPending("archive");
    setError(undefined);
    try {
      await onSetArchived(
        archiveLifecycle.sync({ spaceId: conversation.space.id, archived }),
        archived,
      );
      archiveLifecycle.complete();
    } catch (cause) {
      setError(
        collaborationErrorMessage(
          cause,
          archived ? "archive Space" : "unarchive Space",
        ),
      );
    } finally {
      setPending(undefined);
    }
  };

  return (
    <>
      <header className="context-header">
        <button
          className="icon-button compact-context-back"
          type="button"
          aria-label="Back to conversation"
          onClick={onClose}
        >
          <ArrowLeft size={18} />
        </button>
        <div>
          <span className="eyebrow">Current Space</span>
          <strong>
            {conversation.space.name || (group ? "Group" : "Direct message")}
          </strong>
        </div>
        <button
          className="icon-button desktop-context-close"
          type="button"
          aria-label="Close context"
          onClick={onClose}
        >
          <X size={18} />
        </button>
      </header>
      <div className="context-content space-context">
        <section>
          <header>
            <strong>Members</strong>
            <span>{conversation.memberships.length}</span>
          </header>
          <ul className="member-list">
            {conversation.memberships.map((membership) => {
              const principal = membership.principal;
              if (!principal) return null;
              return (
                <li key={`${principal.kind}:${principal.id}`}>
                  <div>
                    <strong>{principalName(principal, directory)}</strong>
                    <span>
                      {principal.kind === PrincipalKind.HUMAN
                        ? "Human"
                        : "Agent"}
                    </span>
                  </div>
                  {group && principal.id !== currentHumanId && (
                    <button
                      className="icon-button compact"
                      type="button"
                      aria-label={`Remove ${principalName(principal, directory)}`}
                      title="Server will enforce owner and last-Human rules"
                      onClick={() => void remove(principal)}
                      disabled={
                        pending !== undefined ||
                        conversation.permissions.members.status !== "allowed"
                      }
                    >
                      <UserMinus size={15} />
                    </button>
                  )}
                </li>
              );
            })}
          </ul>
        </section>
        {group && (
          <>
            <section>
              <header>
                <strong>Add member</strong>
              </header>
              <PermissionNotice
                permission={conversation.permissions.members}
                action="manage members"
              />
              <div className="context-form-row">
                <select
                  aria-label="Member to add"
                  value={candidate}
                  onChange={(event) => {
                    const value = event.target.value;
                    setCandidate(value);
                    addLifecycle.sync({
                      spaceId: conversation.space.id,
                      member: value,
                    });
                    setError(undefined);
                  }}
                  disabled={
                    pending !== undefined ||
                    conversation.permissions.members.status !== "allowed"
                  }
                >
                  <option value="">Choose active Human or Agent</option>
                  {candidates.map((item) => (
                    <option value={item.value} key={item.value}>
                      {item.label} · {item.detail}
                    </option>
                  ))}
                </select>
                <button
                  className="secondary-action"
                  type="button"
                  onClick={() => void add()}
                  disabled={
                    !candidate ||
                    pending !== undefined ||
                    conversation.permissions.members.status !== "allowed"
                  }
                >
                  <UserPlus size={15} /> {pending === "add" ? "Adding" : "Add"}
                </button>
              </div>
            </section>
            <section>
              <header>
                <strong>Space state</strong>
              </header>
              <PermissionNotice
                permission={conversation.permissions.archive}
                action="change archive state"
              />
              <button
                className="secondary-action"
                type="button"
                onClick={() => void setArchived()}
                disabled={
                  pending !== undefined ||
                  conversation.permissions.archive.status !== "allowed"
                }
              >
                {conversation.space.archivedAt ? (
                  <ArchiveRestore size={15} />
                ) : (
                  <Archive size={15} />
                )}
                {pending === "archive"
                  ? "Saving"
                  : conversation.space.archivedAt
                    ? "Unarchive Space"
                    : "Archive Space"}
              </button>
            </section>
          </>
        )}
        {error && (
          <p className="field-error" role="alert">
            {error}
          </p>
        )}
      </div>
    </>
  );
}

function PermissionNotice({
  permission,
  action,
}: {
  permission: PermissionState;
  action: string;
}) {
  if (permission.status === "allowed") return null;
  return (
    <p className={`permission-notice ${permission.status}`}>
      {permission.status === "denied"
        ? `You do not appear to have permission to ${action}.`
        : `Permission to ${action} is unknown. ${permission.error}`}
    </p>
  );
}

function parsePrincipal(value: string): Principal | undefined {
  const [kindValue, id] = value.split(":", 2);
  const kind = Number(kindValue);
  if (!id || (kind !== PrincipalKind.HUMAN && kind !== PrincipalKind.AGENT))
    return undefined;
  return kind === PrincipalKind.HUMAN
    ? principalForHuman(id)
    : principalForAgent(id);
}

function principalName(principal: Principal, directory: DirectorySnapshot) {
  if (principal.kind === PrincipalKind.HUMAN) {
    return (
      directory.humans.find((human) => human.id === principal.id)?.name ||
      "Unknown Human"
    );
  }
  return (
    directory.agents.find((agent) => agent.id === principal.id)?.name ||
    "Unknown Agent"
  );
}
