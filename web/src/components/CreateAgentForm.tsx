import { useState } from "react";
import { Bot, Check, RotateCw } from "lucide-react";
import { Driver, type Agent } from "../gen/sumi/agent/v1/agent_pb";
import type { Computer } from "../gen/sumi/computer/v1/computer_pb";
import type { AgentPlacement } from "../gen/sumi/placement/v1/placement_pb";
import { createAgent, factErrorMessage, setAgentPlacement } from "../lib/facts";
import { shortId } from "../lib/format";
import { Fact, InlineNotice } from "./ManagementFeedback";

export function CreateAgentForm({
  computers,
  onAgentCreated,
  onPlacementChanged,
  onFinished,
  onCancel,
}: {
  computers: Computer[];
  onAgentCreated: (agent: Agent) => void;
  onPlacementChanged: (placement: AgentPlacement) => void;
  onFinished: (agentId: string) => void;
  onCancel: () => void;
}) {
  const [name, setName] = useState("");
  const [description, setDescription] = useState("");
  const [driver, setDriver] = useState(Driver.NATIVE);
  const [computerId, setComputerId] = useState("");
  const [requestId] = useState(() => crypto.randomUUID());
  const [placementRequestId] = useState(() => crypto.randomUUID());
  const [pending, setPending] = useState(false);
  const [error, setError] = useState<string>();
  const [partial, setPartial] = useState<{
    agent: Agent;
    computerId: string;
    error: string;
  }>();

  const submit = async (event: React.FormEvent) => {
    event.preventDefault();
    if (pending) return;
    setPending(true);
    setError(undefined);
    try {
      const agent = await createAgent({
        requestId,
        name,
        description,
        driver,
      });
      onAgentCreated(agent);
      if (!computerId) {
        onFinished(agent.id);
        return;
      }
      try {
        const placement = await setAgentPlacement({
          requestId: placementRequestId,
          agentId: agent.id,
          computerId,
        });
        onPlacementChanged(placement);
        onFinished(agent.id);
      } catch (reason) {
        setPartial({
          agent,
          computerId,
          error: factErrorMessage(reason, "set placement"),
        });
      }
    } catch (reason) {
      setError(factErrorMessage(reason, "create Agent"));
    } finally {
      setPending(false);
    }
  };

  const retryPlacement = async () => {
    if (!partial || pending) return;
    setPending(true);
    try {
      const placement = await setAgentPlacement({
        requestId: placementRequestId,
        agentId: partial.agent.id,
        computerId: partial.computerId,
      });
      onPlacementChanged(placement);
      onFinished(partial.agent.id);
    } catch (reason) {
      setPartial({
        ...partial,
        error: factErrorMessage(reason, "set placement"),
      });
    } finally {
      setPending(false);
    }
  };

  if (partial) {
    const computer = computers.find((item) => item.id === partial.computerId);
    return (
      <div className="form-sheet partial-success">
        <InlineNotice
          tone="warning"
          title="Agent created, placement failed"
          detail={`${partial.agent.name} is preserved. ${partial.error}`}
        />
        <dl className="fact-grid">
          <Fact label="Agent" value={partial.agent.name} />
          <Fact
            label="Target Computer"
            value={computer?.name ?? `Computer ${shortId(partial.computerId)}`}
          />
        </dl>
        <div className="form-actions">
          <button
            className="secondary-action"
            type="button"
            onClick={() => onFinished(partial.agent.id)}
            disabled={pending}
          >
            Open Agent
          </button>
          <button
            className="primary-action"
            type="button"
            onClick={retryPlacement}
            disabled={pending}
          >
            <RotateCw size={15} />
            {pending ? "Retrying placement" : "Retry placement"}
          </button>
        </div>
      </div>
    );
  }

  return (
    <form className="form-sheet" onSubmit={submit}>
      <div className="form-intro">
        <span className="form-icon">
          <Bot size={22} />
        </span>
        <div>
          <h2>New Agent identity</h2>
          <p>
            The identity is created first. Placement is a separate, optional
            Server fact.
          </p>
        </div>
      </div>
      <div className="form-grid">
        <label>
          <span>Name</span>
          <input
            name="name"
            value={name}
            onChange={(event) => setName(event.target.value)}
            placeholder="release-coordinator"
            pattern="[a-z][a-z0-9]*(?:[-_][a-z0-9]+)*"
            maxLength={32}
            required
            disabled={pending}
          />
          <small>Lowercase handle, up to 32 characters.</small>
        </label>
        <label>
          <span>Driver</span>
          <select
            value={driver}
            onChange={(event) =>
              setDriver(Number(event.target.value) as Driver)
            }
            disabled={pending}
          >
            <option value={Driver.NATIVE}>Native</option>
            <option value={Driver.CODEX}>Codex</option>
            <option value={Driver.CLAUDE}>Claude</option>
          </select>
        </label>
        <label className="full-field">
          <span>Description</span>
          <textarea
            name="description"
            value={description}
            onChange={(event) => setDescription(event.target.value)}
            placeholder="What this Agent is responsible for and especially good at."
            maxLength={1000}
            rows={4}
            disabled={pending}
          />
        </label>
        <label className="full-field">
          <span>Optional Computer</span>
          <select
            value={computerId}
            onChange={(event) => setComputerId(event.target.value)}
            disabled={pending}
          >
            <option value="">Create without placement</option>
            {computers.map((computer) => (
              <option value={computer.id} key={computer.id}>
                {computer.name}
              </option>
            ))}
          </select>
          <small>
            If placement fails, the Agent remains created and can be placed
            later.
          </small>
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
