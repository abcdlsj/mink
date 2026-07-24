import { useState } from "react";
import { Bot, Check } from "lucide-react";
import type { Agent } from "../gen/sumi/agent/v1/agent_pb";
import { createAgent, factErrorMessage } from "../lib/facts";

export function CreateAgentForm({
  onAgentCreated,
  onFinished,
  onCancel,
}: {
  onAgentCreated: (agent: Agent) => void;
  onFinished: (agentId: string) => void;
  onCancel: () => void;
}) {
  const [handle, setHandle] = useState("");
  const [displayName, setDisplayName] = useState("");
  const [role, setRole] = useState("");
  const [mission, setMission] = useState("");
  const [instructions, setInstructions] = useState("");
  const [requestId] = useState(() => crypto.randomUUID());
  const [pending, setPending] = useState(false);
  const [error, setError] = useState<string>();

  const submit = async (event: React.FormEvent) => {
    event.preventDefault();
    if (pending) return;
    setPending(true);
    setError(undefined);
    try {
      const agent = await createAgent({
        requestId,
        handle,
        displayName,
        role,
        mission,
        instructions,
      });
      onAgentCreated(agent);
      onFinished(agent.id);
    } catch (reason) {
      setError(factErrorMessage(reason, "create Agent"));
    } finally {
      setPending(false);
    }
  };

  return (
    <form className="form-sheet" onSubmit={submit}>
      <div className="form-intro">
        <span className="form-icon">
          <Bot size={22} />
        </span>
        <div>
          <h2>New Agent identity</h2>
          <p>
            Create the durable identity and first profile. Runtime and placement
            are configured separately.
          </p>
        </div>
      </div>
      <div className="form-grid">
        <label>
          <span>Handle</span>
          <input
            name="handle"
            value={handle}
            onChange={(event) => setHandle(event.target.value)}
            placeholder="release-coordinator"
            pattern="[a-z][a-z0-9]*(?:[-_][a-z0-9]+)*"
            maxLength={32}
            required
            disabled={pending}
          />
          <small>Stable lowercase handle, up to 32 characters.</small>
        </label>
        <label>
          <span>Display name</span>
          <input
            name="displayName"
            value={displayName}
            onChange={(event) => setDisplayName(event.target.value)}
            maxLength={100}
            required
            disabled={pending}
          />
        </label>
        <label className="full-field">
          <span>Role</span>
          <input
            name="role"
            value={role}
            onChange={(event) => setRole(event.target.value)}
            maxLength={200}
            required
            disabled={pending}
          />
          <small>
            Role describes responsibility; it never grants authority.
          </small>
        </label>
        <label className="full-field">
          <span>Mission</span>
          <textarea
            name="mission"
            value={mission}
            onChange={(event) => setMission(event.target.value)}
            maxLength={2000}
            rows={4}
            required
            disabled={pending}
          />
        </label>
        <label className="full-field">
          <span>Instructions</span>
          <textarea
            name="instructions"
            value={instructions}
            onChange={(event) => setInstructions(event.target.value)}
            maxLength={20000}
            rows={6}
            disabled={pending}
          />
        </label>
      </div>
      {error && (
        <p className="field-error" role="alert">
          {error}
        </p>
      )}
      <div className="form-actions">
        <button
          className="secondary-action"
          type="button"
          onClick={onCancel}
          disabled={pending}
        >
          Cancel
        </button>
        <button className="primary-action" type="submit" disabled={pending}>
          <Check size={15} />
          {pending ? "Creating Agent" : "Create Agent"}
        </button>
      </div>
    </form>
  );
}
