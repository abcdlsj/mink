import { PanelLeftClose, RotateCw } from "lucide-react";
import type { Agent } from "../gen/sumi/agent/v1/agent_pb";
import type { Computer } from "../gen/sumi/computer/v1/computer_pb";
import type { AgentPlacement } from "../gen/sumi/placement/v1/placement_pb";
import type { FactsState } from "../hooks/useFacts";
import {
  architectureLabel,
  driverLabel,
  operatingSystemLabel,
  placementStateLabel,
} from "../lib/format";

export function AgentsNavigation({
  state,
  selected,
  onSelect,
  onRefresh,
  onClose,
}: {
  state: FactsState;
  selected?: string;
  onSelect: (id: string) => void;
  onRefresh: () => void;
  onClose: () => void;
}) {
  const placements = placementMap(state.data?.placements ?? []);
  return (
    <DirectoryNavigation
      eyebrow="Directory"
      title="Agents"
      state={state}
      onRefresh={onRefresh}
      onClose={onClose}
    >
      {state.data && state.data.agents.length === 0 ? (
        <NavigationEmpty
          title="No agents yet"
          detail="Durable Agent identities created by an authorized client will appear here."
        />
      ) : (
        state.data?.agents.map((agent) => (
          <AgentRow
            agent={agent}
            placement={placements.get(agent.id)}
            selected={selected === agent.id}
            onSelect={() => onSelect(agent.id)}
            key={agent.id}
          />
        ))
      )}
    </DirectoryNavigation>
  );
}

export function ComputersNavigation({
  state,
  selected,
  onSelect,
  onRefresh,
  onClose,
}: {
  state: FactsState;
  selected?: string;
  onSelect: (id: string) => void;
  onRefresh: () => void;
  onClose: () => void;
}) {
  return (
    <DirectoryNavigation
      eyebrow="Execution"
      title="Computers"
      state={state}
      onRefresh={onRefresh}
      onClose={onClose}
    >
      {state.data && state.data.computers.length === 0 ? (
        <NavigationEmpty
          title="No computers registered"
          detail="Registered Computers will appear here."
        />
      ) : (
        state.data?.computers.map((computer) => (
          <ComputerRow
            computer={computer}
            selected={selected === computer.id}
            onSelect={() => onSelect(computer.id)}
            key={computer.id}
          />
        ))
      )}
    </DirectoryNavigation>
  );
}

function DirectoryNavigation({
  eyebrow,
  title,
  state,
  children,
  onRefresh,
  onClose,
}: {
  eyebrow: string;
  title: string;
  state: FactsState;
  children: React.ReactNode;
  onRefresh: () => void;
  onClose: () => void;
}) {
  const pending = state.status === "loading" || state.status === "retrying";
  return (
    <>
      <header className="nav-header directory-nav-header">
        <div>
          <span className="eyebrow">{eyebrow}</span>
          <strong>{title}</strong>
        </div>
        <button
          className="icon-button"
          type="button"
          aria-label="Collapse navigation"
          title="Collapse navigation"
          onClick={onClose}
        >
          <PanelLeftClose size={18} />
        </button>
      </header>
      <div className="directory-toolbar">
        <span className="directory-count">
          {title === "Agents"
            ? `${state.data?.agents.length ?? 0} identities`
            : `${state.data?.computers.length ?? 0} registered`}
        </span>
        <button
          className="icon-button compact"
          type="button"
          aria-label={`Refresh ${title}`}
          title={`Refresh ${title}`}
          disabled={state.status === "refreshing" || pending}
          onClick={onRefresh}
        >
          <RotateCw size={15} />
        </button>
      </div>
      <div className="directory-list">
        {(state.status === "loading" || state.status === "retrying") &&
        !state.data ? (
          <NavigationLoading label={`Loading ${title.toLowerCase()}`} />
        ) : state.status === "error" && !state.data ? (
          <NavigationError
            message={state.error ?? `Could not load ${title.toLowerCase()}.`}
            onRetry={onRefresh}
          />
        ) : (
          <>
            {(state.status === "stale" || state.status === "refreshing") && (
              <div
                className={`stale-notice ${state.status === "refreshing" ? "refreshing" : ""}`}
                role={state.status === "stale" ? "alert" : "status"}
              >
                <span>
                  {state.status === "refreshing"
                    ? "Refreshing facts"
                    : state.error}
                </span>
                {state.status === "stale" && (
                  <button type="button" onClick={onRefresh}>
                    Retry
                  </button>
                )}
              </div>
            )}
            {children}
          </>
        )}
      </div>
    </>
  );
}

function AgentRow({
  agent,
  placement,
  selected,
  onSelect,
}: {
  agent: Agent;
  placement?: AgentPlacement;
  selected: boolean;
  onSelect: () => void;
}) {
  const state = placementStateLabel(placement?.state);
  return (
    <button
      className={`directory-row ${selected ? "selected" : ""}`}
      type="button"
      aria-current={selected ? "true" : undefined}
      onClick={onSelect}
    >
      <span className="directory-row-leading agent-monogram">
        {agent.name.slice(0, 1).toUpperCase()}
      </span>
      <span className="directory-row-copy">
        <strong>{agent.name}</strong>
        <small>{driverLabel(agent.driver)}</small>
      </span>
      <span className={`state-chip ${state}`}>{state}</span>
    </button>
  );
}

function ComputerRow({
  computer,
  selected,
  onSelect,
}: {
  computer: Computer;
  selected: boolean;
  onSelect: () => void;
}) {
  return (
    <button
      className={`directory-row ${selected ? "selected" : ""}`}
      type="button"
      aria-current={selected ? "true" : undefined}
      onClick={onSelect}
    >
      <span className="directory-row-leading computer-mark" />
      <span className="directory-row-copy">
        <strong>{computer.name}</strong>
        <small>
          {operatingSystemLabel(computer.os)} ·{" "}
          {architectureLabel(computer.arch)}
        </small>
      </span>
    </button>
  );
}

function NavigationLoading({ label }: { label: string }) {
  return (
    <div className="navigation-feedback" role="status">
      <span className="status-dot loading" />
      <p>{label}</p>
    </div>
  );
}

function NavigationError({
  message,
  onRetry,
}: {
  message: string;
  onRetry: () => void;
}) {
  return (
    <div className="navigation-feedback error" role="alert">
      <strong>Facts unavailable</strong>
      <p>{message}</p>
      <button className="secondary-action" type="button" onClick={onRetry}>
        Retry
      </button>
    </div>
  );
}

function NavigationEmpty({ title, detail }: { title: string; detail: string }) {
  return (
    <div className="navigation-feedback empty">
      <strong>{title}</strong>
      <p>{detail}</p>
    </div>
  );
}

function placementMap(placements: AgentPlacement[]) {
  return new Map(placements.map((placement) => [placement.agentId, placement]));
}
