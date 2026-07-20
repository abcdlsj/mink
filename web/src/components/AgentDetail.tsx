import { useEffect, useState } from "react";
import type { Agent } from "../gen/sumi/agent/v1/agent_pb";
import type { Computer } from "../gen/sumi/computer/v1/computer_pb";
import {
  PlacementState,
  type AgentPlacement,
} from "../gen/sumi/placement/v1/placement_pb";
import { useAgentDetail } from "../hooks/useDetails";
import { factErrorMessage, setAgentPlacement } from "../lib/facts";
import {
  driverLabel,
  formatTimestamp,
  placementStateLabel,
  shortId,
} from "../lib/format";
import {
  Fact,
  InlineNotice,
  ManagementError,
  ManagementLoading,
} from "./ManagementFeedback";

export function AgentDetail({
  agent,
  placement,
  computers,
  onPlacementChanged,
}: {
  agent: Agent;
  placement?: AgentPlacement;
  computers: Computer[];
  onPlacementChanged: (placement: AgentPlacement) => void;
}) {
  const detail = useAgentDetail(agent.id, agent, placement);
  const current = detail.data;
  if (!current) {
    if (detail.status === "error") {
      return <ManagementError message={detail.error} onRetry={detail.reload} />;
    }
    return <ManagementLoading label="Loading Agent details" />;
  }
  const state = placementStateLabel(current.placement?.state);
  const computer = computers.find(
    (item) => item.id === current.placement?.computerId,
  );
  return (
    <div className="detail-sheet">
      {detail.status === "stale" && (
        <InlineNotice
          tone="warning"
          title="Showing saved facts"
          detail={detail.error ?? "Agent refresh failed."}
          action="Retry"
          onAction={detail.reload}
        />
      )}
      <section className="identity-section">
        <div className="identity-mark">
          {current.agent.name.slice(0, 1).toUpperCase()}
        </div>
        <div>
          <span className="eyebrow">Durable identity</span>
          <h2>{current.agent.name}</h2>
          <p>{current.agent.description || "No description provided."}</p>
        </div>
        <span className={`state-chip ${state}`}>{state}</span>
      </section>
      <section className="detail-section">
        <header>
          <div>
            <span className="eyebrow">Execution</span>
            <h3>Placement</h3>
          </div>
          {current.placement && (
            <span className="generation-label">
              Generation {current.placement.generation.toString()}
            </span>
          )}
        </header>
        <dl className="fact-grid">
          <Fact label="State" value={state} />
          <Fact
            label="Computer"
            value={
              computer?.name ??
              (current.placement
                ? `Computer ${shortId(current.placement.computerId)}`
                : "Not placed")
            }
          />
          <Fact label="Driver" value={driverLabel(current.agent.driver)} />
          <Fact
            label="Updated"
            value={formatTimestamp(current.placement?.updatedAt)}
          />
        </dl>
        {current.placement?.state === PlacementState.FAILED && (
          <InlineNotice
            tone="danger"
            title="Workspace provision failed"
            detail={current.placement.errorCode}
          />
        )}
        <PlacementEditor
          agentId={current.agent.id}
          placement={current.placement}
          computers={computers}
          onChanged={(next) => {
            onPlacementChanged(next);
            detail.reload();
          }}
        />
      </section>
      <section className="detail-section diagnostics-section">
        <header>
          <div>
            <span className="eyebrow">Diagnostics</span>
            <h3>Identity facts</h3>
          </div>
        </header>
        <dl className="diagnostic-list">
          <Fact label="Agent ID" value={current.agent.id} mono />
          <Fact
            label="Created"
            value={formatTimestamp(current.agent.createdAt)}
          />
          <Fact
            label="Updated"
            value={formatTimestamp(current.agent.updatedAt)}
          />
          {current.placement && (
            <Fact
              label="Placement created"
              value={formatTimestamp(current.placement.createdAt)}
            />
          )}
        </dl>
      </section>
    </div>
  );
}

function PlacementEditor({
  agentId,
  placement,
  computers,
  onChanged,
}: {
  agentId: string;
  placement?: AgentPlacement;
  computers: Computer[];
  onChanged: (placement: AgentPlacement) => void;
}) {
  const [computerId, setComputerId] = useState(
    placement?.computerId ?? computers[0]?.id ?? "",
  );
  const [pending, setPending] = useState(false);
  const [error, setError] = useState<string>();

  useEffect(() => {
    if (placement?.computerId) setComputerId(placement.computerId);
    else if (!computerId && computers[0]) setComputerId(computers[0].id);
  }, [computerId, computers, placement?.computerId]);

  if (placement?.state === PlacementState.ACTIVE) {
    return (
      <p className="readonly-note">
        This Workspace is active. Placement changes are read-only in this slice.
      </p>
    );
  }
  if (computers.length === 0) {
    return (
      <p className="readonly-note">
        Register a Computer before setting placement.
      </p>
    );
  }

  const submit = async () => {
    if (!computerId || pending) return;
    setPending(true);
    setError(undefined);
    try {
      onChanged(await setAgentPlacement(agentId, computerId));
    } catch (reason) {
      setError(factErrorMessage(reason, "set placement"));
    } finally {
      setPending(false);
    }
  };

  return (
    <div className="placement-editor">
      <label>
        <span>Target Computer</span>
        <select
          value={computerId}
          onChange={(event) => setComputerId(event.target.value)}
          disabled={pending}
        >
          {computers.map((computer) => (
            <option value={computer.id} key={computer.id}>
              {computer.name}
            </option>
          ))}
        </select>
      </label>
      <button
        className="primary-action"
        type="button"
        onClick={submit}
        disabled={pending || !computerId}
      >
        {pending
          ? "Setting placement"
          : placement?.state === PlacementState.FAILED
            ? "Retry placement"
            : "Set placement"}
      </button>
      {error && (
        <p className="field-error" role="alert">
          {error}
        </p>
      )}
    </div>
  );
}
