import { Bot } from "lucide-react";
import type { useBootstrap } from "../hooks/useBootstrap";
import type { useFacts } from "../hooks/useFacts";
import { driverLabel } from "../lib/format";
import { AgentDetail } from "./AgentDetail";
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
}: {
  selected?: string;
  facts: Facts;
  bootstrap: Bootstrap;
  navigationOpen: boolean;
  onOpenNavigation: () => void;
  onSelectAgent: (id: string) => void;
}) {
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
        />
      ) : facts.status === "loading" || facts.status === "retrying" ? (
        <ManagementLoading label="Loading Agents" />
      ) : facts.status === "error" ? (
        <ManagementError message={facts.error} onRetry={facts.retry} />
      ) : (
        <ManagementEmpty
          icon={<Bot size={30} strokeWidth={1.6} />}
          title="Select an Agent"
          detail="Choose a durable Agent identity from the directory."
        />
      )}
    </ManagementWorkspace>
  );
}
