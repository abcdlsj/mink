import type { Agent } from "../gen/sumi/agent/v1/agent_pb";
import type { Computer } from "../gen/sumi/computer/v1/computer_pb";
import {
  PlacementState,
  type AgentPlacement,
} from "../gen/sumi/placement/v1/placement_pb";
import { useAgentDetail } from "../hooks/useDetails";
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
}: {
  agent: Agent;
  placement?: AgentPlacement;
  computers: Computer[];
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
        <p className="readonly-note">
          Placement changes are not available in this read-only view.
        </p>
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
