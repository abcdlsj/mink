import { Code, ConnectError, createClient } from "@connectrpc/connect";
import { createConnectTransport } from "@connectrpc/connect-web";
import {
  AgentService,
  type Agent,
  type Driver,
} from "../gen/sumi/agent/v1/agent_pb";
import {
  ComputerService,
  type Computer,
} from "../gen/sumi/computer/v1/computer_pb";
import {
  PlacementService,
  type AgentPlacement,
} from "../gen/sumi/placement/v1/placement_pb";

const transport = createConnectTransport({ baseUrl: window.location.origin });
const agents = createClient(AgentService, transport);
const computers = createClient(ComputerService, transport);
const placements = createClient(PlacementService, transport);

export type FactsSnapshot = {
  agents: Agent[];
  computers: Computer[];
  placements: AgentPlacement[];
};

export type AgentDetailSnapshot = {
  agent: Agent;
  placement?: AgentPlacement;
};

export async function loadFacts(): Promise<FactsSnapshot> {
  const [agentResponse, computerResponse, placementResponse] =
    await Promise.all([
      agents.listAgents({}),
      computers.listComputers({}),
      placements.listAgentPlacements({}),
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
    agents.getAgent({ agentId }),
    getPlacement(agentId),
  ]);
  if (!agentResponse.agent) throw new Error("Agent response was empty");
  return { agent: agentResponse.agent, placement };
}

export async function getComputer(computerId: string): Promise<Computer> {
  const response = await computers.getComputer({ computerId });
  if (!response.computer) throw new Error("Computer response was empty");
  return response.computer;
}

export async function createAgent(input: {
  requestId: string;
  name: string;
  description: string;
  driver: Driver;
}): Promise<Agent> {
  const response = await agents.createAgent(input);
  if (!response.agent) throw new Error("Create Agent response was empty");
  return response.agent;
}

export async function setAgentPlacement(
  agentId: string,
  computerId: string,
): Promise<AgentPlacement> {
  const response = await placements.setAgentPlacement({ agentId, computerId });
  if (!response.placement) throw new Error("Placement response was empty");
  return response.placement;
}

async function getPlacement(
  agentId: string,
): Promise<AgentPlacement | undefined> {
  try {
    const response = await placements.getAgentPlacement({ agentId });
    return response.placement;
  } catch (error) {
    const connectError = ConnectError.from(error);
    if (connectError.code === Code.NotFound) return undefined;
    throw error;
  }
}

export function factErrorMessage(error: unknown, action: string) {
  const connectError = ConnectError.from(error);
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
