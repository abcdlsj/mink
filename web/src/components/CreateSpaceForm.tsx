import { useMemo, useRef, useState } from "react";
import { MessageCircle, UsersRound, X } from "lucide-react";
import type { Space } from "../gen/sumi/space/v1/space_pb";
import {
  PayloadRequestLifecycle,
  activeHumans,
  collaborationErrorMessage,
  principalForAgent,
  principalForHuman,
  type DirectorySnapshot,
} from "../lib/collaboration";
import { IconButton } from "./IconButton";

type Mode = "dm" | "group";

export function CreateSpaceForm({
  directory,
  currentHumanId,
  onCreateDM,
  onCreateGroup,
  onCreated,
  onClose,
}: {
  directory: DirectorySnapshot;
  currentHumanId: string;
  onCreateDM: Parameters<typeof submitDM>[1];
  onCreateGroup: Parameters<typeof submitGroup>[1];
  onCreated: (space: Space) => void;
  onClose: () => void;
}) {
  const [mode, setMode] = useState<Mode>("dm");
  const [peer, setPeer] = useState("");
  const [name, setName] = useState("");
  const [pending, setPending] = useState(false);
  const [error, setError] = useState<string>();
  const dmLifecycle = useRef(
    new PayloadRequestLifecycle<{ peer: string }>(),
  ).current;
  const groupLifecycle = useRef(
    new PayloadRequestLifecycle<{ name: string }>(),
  ).current;
  const candidates = useMemo(
    () => [
      ...activeHumans(directory.humans)
        .filter((human) => human.id !== currentHumanId)
        .map((human) => ({
          value: `human:${human.id}`,
          label: human.name,
          detail: "Human",
        })),
      ...directory.agents.map((agent) => ({
        value: `agent:${agent.id}`,
        label: agent.name,
        detail: "Agent",
      })),
    ],
    [currentHumanId, directory.agents, directory.humans],
  );

  const create = async () => {
    setPending(true);
    setError(undefined);
    try {
      const space =
        mode === "dm"
          ? await submitDM(peer, onCreateDM, dmLifecycle)
          : await submitGroup(name, onCreateGroup, groupLifecycle);
      if (mode === "dm") dmLifecycle.complete();
      else groupLifecycle.complete();
      onCreated(space);
      onClose();
    } catch (cause) {
      setError(collaborationErrorMessage(cause, `create ${mode}`));
    } finally {
      setPending(false);
    }
  };

  return (
    <section className="create-space-panel" aria-label="Create conversation">
      <header>
        <strong>New conversation</strong>
        <IconButton
          className="compact"
          label="Close create conversation"
          tooltipPlacement="left"
          onClick={onClose}
          disabled={pending}
        >
          <X size={16} />
        </IconButton>
      </header>
      <div
        className="create-space-modes"
        role="tablist"
        aria-label="Space type"
      >
        <button
          type="button"
          role="tab"
          aria-selected={mode === "dm"}
          className={mode === "dm" ? "selected" : ""}
          onClick={() => {
            setMode("dm");
            setError(undefined);
          }}
          disabled={pending}
        >
          <MessageCircle size={14} /> DM
        </button>
        <button
          type="button"
          role="tab"
          aria-selected={mode === "group"}
          className={mode === "group" ? "selected" : ""}
          onClick={() => {
            setMode("group");
            setError(undefined);
          }}
          disabled={pending}
        >
          <UsersRound size={14} /> Group
        </button>
      </div>
      {mode === "dm" ? (
        <label>
          <span>Peer</span>
          <select
            value={peer}
            onChange={(event) => {
              const value = event.target.value;
              setPeer(value);
              dmLifecycle.sync({ peer: value });
              setError(undefined);
            }}
            disabled={pending}
          >
            <option value="">Choose a Human or Agent</option>
            {candidates.map((candidate) => (
              <option value={candidate.value} key={candidate.value}>
                {candidate.label} · {candidate.detail}
              </option>
            ))}
          </select>
        </label>
      ) : (
        <label>
          <span>Group name</span>
          <input
            value={name}
            onChange={(event) => {
              const value = event.target.value;
              setName(value);
              groupLifecycle.sync({ name: value.trim() });
              setError(undefined);
            }}
            placeholder="Release coordination"
            disabled={pending}
          />
        </label>
      )}
      {error && <p className="field-error">{error}</p>}
      <button
        className="primary-action"
        type="button"
        onClick={() => void create()}
        disabled={
          pending ||
          (mode === "dm" ? peer.length === 0 : name.trim().length === 0)
        }
      >
        {pending
          ? "Creating"
          : error
            ? "Retry"
            : `Create ${mode === "dm" ? "DM" : "Group"}`}
      </button>
    </section>
  );
}

async function submitDM(
  peer: string,
  create: (
    requestId: string,
    peer: ReturnType<typeof principalForHuman>,
  ) => Promise<Space>,
  lifecycle: PayloadRequestLifecycle<{ peer: string }>,
) {
  const [kind, id] = peer.split(":", 2);
  if (!id || (kind !== "human" && kind !== "agent")) {
    throw new Error("Choose a valid DM peer");
  }
  const requestId = lifecycle.sync({ peer });
  return create(
    requestId,
    kind === "human" ? principalForHuman(id) : principalForAgent(id),
  );
}

async function submitGroup(
  name: string,
  create: (requestId: string, name: string) => Promise<Space>,
  lifecycle: PayloadRequestLifecycle<{ name: string }>,
) {
  const trimmed = name.trim();
  if (!trimmed) throw new Error("Enter a Group name");
  return create(lifecycle.sync({ name: trimmed }), trimmed);
}
