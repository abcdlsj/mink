import type { Agent } from "../gen/sumi/agent/v1/agent_pb";
import type { Computer } from "../gen/sumi/computer/v1/computer_pb";
import {
  PlacementState,
  type AgentPlacement,
} from "../gen/sumi/placement/v1/placement_pb";
import { useAgentDetail } from "../hooks/useDetails";
import {
  agentDisplayName,
  engineKindLabel,
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
import { PixelAvatar } from "./PixelAvatar";
import { RuntimeConfiguration } from "./RuntimeConfiguration";

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
  const name = agentDisplayName(current.agent);
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
        <PixelAvatar
          className="identity-avatar"
          seed={current.agent.id}
          kind="agent"
          size="lg"
        />
        <div>
          <span className="eyebrow">Durable identity</span>
          <h2>{name}</h2>
          <p>{current.agent.profile?.mission}</p>
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
            <span className="revision-label">
              Desired revision {current.placement.desiredRevision.toString()}
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
          <Fact label="Handle" value={`@${current.agent.handle}`} />
          <Fact label="Role" value={current.agent.profile?.role ?? "Unknown"} />
          <Fact
            label="Profile revision"
            value={current.agent.profile?.revision.toString() ?? "Unknown"}
          />
          <Fact
            label="Engine"
            value={engineKindLabel(current.placement?.runtimeSpec?.engine)}
          />
          <Fact
            label="Runtime spec revision"
            value={
              current.placement?.runtimeSpec?.revision.toString() ??
              "Not configured"
            }
          />
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
      </section>
      <RuntimeConfiguration
        agent={current.agent}
        placement={current.placement}
        computers={computers}
        onConfigured={detail.reload}
      />
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
            label="Profile created"
            value={formatTimestamp(current.agent.profile?.createdAt)}
          />
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
