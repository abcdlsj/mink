import { MonitorCog } from "lucide-react";
import type { useBootstrap } from "../hooks/useBootstrap";
import { useComputerDetail } from "../hooks/useDetails";
import type { useFacts } from "../hooks/useFacts";
import type { useSession } from "../hooks/useSession";
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
  ManagementError,
  ManagementLoading,
} from "./ManagementFeedback";
import { ManagementWorkspace } from "./ManagementWorkspace";
import { ComputerOnboarding } from "./ComputerOnboarding";

type Bootstrap = ReturnType<typeof useBootstrap>;
type Facts = ReturnType<typeof useFacts>;
type Session = ReturnType<typeof useSession>;

export function ComputerWorkspace({
  selected,
  facts,
  bootstrap,
  session,
  navigationOpen,
  onOpenNavigation,
  onSignIn,
}: {
  selected?: string;
  facts: Facts;
  bootstrap: Bootstrap;
  session: Session;
  navigationOpen: boolean;
  onOpenNavigation: () => void;
  onSignIn: () => void;
}) {
  const computer = facts.data?.computers.find((item) => item.id === selected);
  return (
    <ManagementWorkspace
      label="Computers"
      title={
        computer
          ? computer.name
          : facts.data?.computers.length === 0
            ? "Connect a Computer"
            : "Computer details"
      }
      summary={
        computer
          ? `${operatingSystemLabel(computer.os)} · ${architectureLabel(computer.arch)}`
          : facts.data?.computers.length === 0
            ? "Pair a Mac or Linux execution node with this Sumi."
            : "Select a registered Computer from the directory."
      }
      bootstrap={bootstrap}
      navigationOpen={navigationOpen}
      onOpenNavigation={onOpenNavigation}
    >
      {computer ? (
        <div className="computer-workspace-content">
          <ComputerOnboarding
            authenticated={
              session.status === "authenticated" ||
              session.status === "logging-out"
            }
            compact
            onSignIn={onSignIn}
            onRefresh={facts.refresh}
          />
          <ComputerDetail computer={computer} facts={facts} />
        </div>
      ) : facts.status === "loading" || facts.status === "retrying" ? (
        <ManagementLoading label="Loading Computers" />
      ) : facts.status === "error" ? (
        <ManagementError message={facts.error} onRetry={facts.retry} />
      ) : (
        <ComputerOnboarding
          authenticated={
            session.status === "authenticated" ||
            session.status === "logging-out"
          }
          onSignIn={onSignIn}
          onRefresh={facts.refresh}
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
