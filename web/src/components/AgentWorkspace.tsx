import { Bot } from "lucide-react";
import type { useBootstrap } from "../hooks/useBootstrap";
import type { useFacts } from "../hooks/useFacts";
import type { Agent } from "../gen/sumi/agent/v1/agent_pb";
import type { AgentPlacement } from "../gen/sumi/placement/v1/placement_pb";
import type { FactsSnapshot } from "../lib/facts";
import { driverLabel } from "../lib/format";
import { AgentDetail } from "./AgentDetail";
import { CreateAgentForm } from "./CreateAgentForm";
import {
  ManagementEmpty,
  ManagementError,
  ManagementLoading,
} from "./ManagementFeedback";
import { ManagementWorkspace } from "./ManagementWorkspace";

type Bootstrap = ReturnType<typeof useBootstrap>;
type Facts = ReturnType<typeof useFacts>;

export function AgentWorkspace({
  selected,
  facts,
  bootstrap,
  navigationOpen,
  onOpenNavigation,
  onSelectAgent,
  onCancelCreate,
}: {
  selected?: string;
  facts: Facts;
  bootstrap: Bootstrap;
  navigationOpen: boolean;
  onOpenNavigation: () => void;
  onSelectAgent: (id: string) => void;
  onCancelCreate: () => void;
}) {
  if (selected === "create") {
    return (
      <ManagementWorkspace
        label="Agents"
        title="Create Agent"
        summary="Create a durable identity, then optionally place it on a Computer."
        bootstrap={bootstrap}
        navigationOpen={navigationOpen}
        onOpenNavigation={onOpenNavigation}
      >
        <CreateAgentForm
          computers={facts.data?.computers ?? []}
          onAgentCreated={(agent) =>
            facts.mutate((current) => upsertAgent(current, agent))
          }
          onPlacementChanged={(placement) =>
            facts.mutate((current) => upsertPlacement(current, placement))
          }
          onFinished={onSelectAgent}
          onCancel={onCancelCreate}
        />
      </ManagementWorkspace>
    );
  }

  const agent = facts.data?.agents.find((item) => item.id === selected);
  const placement = facts.data?.placements.find(
    (item) => item.agentId === selected,
  );
  return (
    <ManagementWorkspace
      label="Agents"
      title={agent?.name ?? "Agent details"}
      summary={
        agent
          ? `${driverLabel(agent.driver)} identity and current placement.`
          : "Select an Agent from the directory."
      }
      bootstrap={bootstrap}
      navigationOpen={navigationOpen}
      onOpenNavigation={onOpenNavigation}
    >
      {agent ? (
        <AgentDetail
          agent={agent}
          placement={placement}
          computers={facts.data?.computers ?? []}
          onPlacementChanged={(next) =>
            facts.mutate((current) => upsertPlacement(current, next))
          }
        />
      ) : facts.status === "loading" || facts.status === "retrying" ? (
        <ManagementLoading label="Loading Agents" />
      ) : facts.status === "error" ? (
        <ManagementError message={facts.error} onRetry={facts.retry} />
      ) : (
        <ManagementEmpty
          icon={<Bot size={30} strokeWidth={1.6} />}
          title="Select an Agent"
          detail="Choose an Agent from the list or create a durable identity."
        />
      )}
    </ManagementWorkspace>
  );
}

function upsertAgent(current: FactsSnapshot, agent: Agent): FactsSnapshot {
  return {
    ...current,
    agents: [
      ...current.agents.filter((item) => item.id !== agent.id),
      agent,
    ].sort((left, right) => left.name.localeCompare(right.name)),
  };
}

function upsertPlacement(
  current: FactsSnapshot,
  placement: AgentPlacement,
): FactsSnapshot {
  return {
    ...current,
    placements: [
      ...current.placements.filter(
        (item) => item.agentId !== placement.agentId,
      ),
      placement,
    ],
  };
}
