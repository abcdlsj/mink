import { MonitorCog } from "lucide-react";
import type { useBootstrap } from "../hooks/useBootstrap";
import { useComputerDetail } from "../hooks/useDetails";
import type { useFacts } from "../hooks/useFacts";
import type { Computer } from "../gen/sumi/computer/v1/computer_pb";
import {
  architectureLabel,
  formatTimestamp,
  operatingSystemLabel,
  placementStateLabel,
  shortId,
} from "../lib/format";
import {
  Fact,
  InlineNotice,
  ManagementEmpty,
  ManagementError,
  ManagementLoading,
} from "./ManagementFeedback";
import { ManagementWorkspace } from "./ManagementWorkspace";

type Bootstrap = ReturnType<typeof useBootstrap>;
type Facts = ReturnType<typeof useFacts>;

export function ComputerWorkspace({
  selected,
  facts,
  bootstrap,
  navigationOpen,
  onOpenNavigation,
}: {
  selected?: string;
  facts: Facts;
  bootstrap: Bootstrap;
  navigationOpen: boolean;
  onOpenNavigation: () => void;
}) {
  const computer = facts.data?.computers.find((item) => item.id === selected);
  return (
    <ManagementWorkspace
      label="Computers"
      title={computer?.name ?? "Computer details"}
      summary={
        computer
          ? `${operatingSystemLabel(computer.os)} · ${architectureLabel(computer.arch)}`
          : "Select a registered Computer from the directory."
      }
      bootstrap={bootstrap}
      navigationOpen={navigationOpen}
      onOpenNavigation={onOpenNavigation}
    >
      {computer ? (
        <ComputerDetail computer={computer} facts={facts} />
      ) : facts.status === "loading" || facts.status === "retrying" ? (
        <ManagementLoading label="Loading Computers" />
      ) : facts.status === "error" ? (
        <ManagementError message={facts.error} onRetry={facts.retry} />
      ) : (
        <ManagementEmpty
          icon={<MonitorCog size={30} strokeWidth={1.6} />}
          title="Select a Computer"
          detail="Choose a Computer to inspect its real capabilities and placements."
        />
      )}
    </ManagementWorkspace>
  );
}

function ComputerDetail({
  computer,
  facts,
}: {
  computer: Computer;
  facts: Facts;
}) {
  const detail = useComputerDetail(computer.id, computer);
  const current = detail.data;
  if (!current) {
    if (detail.status === "error") {
      return <ManagementError message={detail.error} onRetry={detail.reload} />;
    }
    return <ManagementLoading label="Loading Computer details" />;
  }
  const related = (facts.data?.placements ?? []).filter(
    (placement) => placement.computerId === current.id,
  );
  const agents = new Map(
    (facts.data?.agents ?? []).map((agent) => [agent.id, agent]),
  );
  return (
    <div className="detail-sheet">
      {detail.status === "stale" && (
        <InlineNotice
          tone="warning"
          title="Showing saved facts"
          detail={detail.error ?? "Computer refresh failed."}
          action="Retry"
          onAction={detail.reload}
        />
      )}
      <section className="identity-section computer-identity">
        <div className="identity-mark computer">
          <MonitorCog size={24} />
        </div>
        <div>
          <span className="eyebrow">Registered Computer</span>
          <h2>{current.name}</h2>
          <p>
            {operatingSystemLabel(current.os)} on{" "}
            {architectureLabel(current.arch)}
          </p>
        </div>
        <span className="capability-label">trusted local</span>
      </section>
      <section className="detail-section">
        <header>
          <div>
            <span className="eyebrow">Execution facts</span>
            <h3>Computer</h3>
          </div>
        </header>
        <dl className="fact-grid">
          <Fact
            label="Operating system"
            value={operatingSystemLabel(current.os)}
          />
          <Fact label="Architecture" value={architectureLabel(current.arch)} />
          <Fact label="Last seen" value={formatTimestamp(current.lastSeenAt)} />
          <Fact label="Registered" value={formatTimestamp(current.createdAt)} />
        </dl>
      </section>
      <section className="detail-section">
        <header>
          <div>
            <span className="eyebrow">Current facts</span>
            <h3>Agent placements</h3>
          </div>
          <span className="generation-label">{related.length} total</span>
        </header>
        {related.length === 0 ? (
          <p className="section-empty">
            No Agents are currently placed on this Computer.
          </p>
        ) : (
          <div className="placement-list">
            {related.map((placement) => {
              const agent = agents.get(placement.agentId);
              const state = placementStateLabel(placement.state);
              return (
                <div className="placement-row" key={placement.agentId}>
                  <span className="agent-monogram">
                    {agent?.name.slice(0, 1).toUpperCase() ?? "A"}
                  </span>
                  <div>
                    <strong>
                      {agent?.name ?? `Agent ${shortId(placement.agentId)}`}
                    </strong>
                    <small>
                      Generation {placement.generation.toString()} · updated{" "}
                      {formatTimestamp(placement.updatedAt)}
                    </small>
                    {placement.errorCode && (
                      <span className="placement-error-code">
                        {placement.errorCode}
                      </span>
                    )}
                  </div>
                  <span className={`state-chip ${state}`}>{state}</span>
                </div>
              );
            })}
          </div>
        )}
      </section>
      <section className="detail-section diagnostics-section">
        <header>
          <div>
            <span className="eyebrow">Diagnostics</span>
            <h3>Computer identity</h3>
          </div>
        </header>
        <dl className="diagnostic-list">
          <Fact label="Computer ID" value={current.id} mono />
        </dl>
      </section>
    </div>
  );
}
