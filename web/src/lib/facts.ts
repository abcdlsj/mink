import { Code, ConnectError } from "@connectrpc/connect";
import {
  AgentService,
  type Agent,
  type Driver,
} from "../gen/sumi/agent/v1/agent_pb";
import { type Computer } from "../gen/sumi/computer/v1/computer_pb";
import { type AgentPlacement } from "../gen/sumi/placement/v1/placement_pb";
import { agentClient, computerClient, placementClient } from "../api/clients";

export type FactsSnapshot = {
  agents: Agent[];
  computers: Computer[];
  placements: AgentPlacement[];
};

export type AgentDetailSnapshot = {
  agent: Agent;
  placement?: AgentPlacement;
};

export async function loadFacts(
  options: { signal?: AbortSignal } = {},
): Promise<FactsSnapshot> {
  const [agentResponse, computerResponse, placementResponse] =
    await Promise.all([
      agentClient.listAgents({}, options),
      computerClient.listComputers({}, options),
      placementClient.listAgentPlacements({}, options),
    ]);
  return {
    agents: agentResponse.agents,
    computers: computerResponse.computers,
    placements: placementResponse.placements,
  };
}

export async function getAgentDetail(
  agentId: string,
): Promise<AgentDetailSnapshot> {
  const [agentResponse, placement] = await Promise.all([
    agentClient.getAgent({ agentId }),
    getPlacement(agentId),
  ]);
  if (!agentResponse.agent) throw new Error("Agent response was empty");
  return { agent: agentResponse.agent, placement };
}

export async function getComputer(computerId: string): Promise<Computer> {
  const response = await computerClient.getComputer({ computerId });
  if (!response.computer) throw new Error("Computer response was empty");
  return response.computer;
}

export async function createAgent(input: {
  requestId: string;
  name: string;
  description: string;
  driver: Driver;
}): Promise<Agent> {
  const response = await agentClient.createAgent(input);
  if (!response.agent) throw new Error("Create Agent response was empty");
  return response.agent;
}

export async function setAgentPlacement(input: {
  requestId: string;
  agentId: string;
  computerId: string;
}): Promise<AgentPlacement> {
  const response = await placementClient.setAgentPlacement(input);
  if (!response.placement) throw new Error("Placement response was empty");
  return response.placement;
}

async function getPlacement(
  agentId: string,
): Promise<AgentPlacement | undefined> {
  try {
    const response = await placementClient.getAgentPlacement({ agentId });
    return response.placement;
  } catch (error) {
    const connectError = ConnectError.from(error);
    if (connectError.code === Code.NotFound) return undefined;
    throw error;
  }
}

export function factErrorMessage(error: unknown, action: string) {
  const connectError = ConnectError.from(error);
  if (
    connectError.code === Code.Unauthenticated ||
    connectError.code === Code.PermissionDenied
  ) {
    return `Authorization required to ${action}. Use an authenticated Human management client.`;
  }
  if (connectError.code === Code.AlreadyExists) {
    return "An Agent with this name already exists.";
  }
  if (connectError.code === Code.InvalidArgument) {
    return connectError.rawMessage || `The ${action} request is invalid.`;
  }
  if (connectError.code === Code.NotFound) {
    return "The selected Agent or Computer no longer exists. Refresh and try again.";
  }
  if (connectError.code === Code.Unavailable) {
    return "Server unavailable. Check the connection and retry.";
  }
  return `Could not ${action}. Retry when the Server is available.`;
}
