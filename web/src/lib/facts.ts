import { Code, ConnectError } from "@connectrpc/connect";
import { timestampFromDate } from "@bufbuild/protobuf/wkt";
import {
  type Agent,
  type AgentRuntimeSpec,
  type EngineKind,
  type ProviderProtocol,
  type RuntimeSandboxProvider,
} from "../gen/sumi/agent/v1/agent_pb";
import {
  CredentialDeliveryAlgorithm,
  CredentialDeliveryState,
  type Computer,
  type CredentialDelivery,
  type CredentialKind,
} from "../gen/sumi/computer/v1/computer_pb";
import { type AgentPlacement } from "../gen/sumi/placement/v1/placement_pb";
import { agentClient, computerClient, placementClient } from "../api/clients";
import type { BrowserSealedCredential } from "./credentialDelivery";

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

export async function createComputerPairing(input: {
  requestId: string;
  pairingToken: string;
  expiresAt: Date;
}): Promise<void> {
  await computerClient.createComputerPairing({
    requestId: input.requestId,
    pairingToken: input.pairingToken,
    expiresAt: timestampFromDate(input.expiresAt),
  });
}

export async function createAgent(input: {
  requestId: string;
  handle: string;
  displayName: string;
  role: string;
  mission: string;
  instructions: string;
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

export async function getAgentRuntimeSpec(
  agentId: string,
): Promise<AgentRuntimeSpec | undefined> {
  try {
    const response = await agentClient.getAgentRuntimeSpec({ agentId });
    if (!response.runtimeSpec) {
      throw new Error("Runtime spec response was empty");
    }
    return response.runtimeSpec;
  } catch (error) {
    if (ConnectError.from(error).code === Code.FailedPrecondition) {
      return undefined;
    }
    throw error;
  }
}

export async function updateAgentRuntimeSpec(input: {
  requestId: string;
  agentId: string;
  expectedRevision: bigint;
  engine: EngineKind;
  providerProtocol: ProviderProtocol;
  providerEndpoint: string;
  model: string;
  credentialBindingHandle: string;
  sandboxProvider: RuntimeSandboxProvider;
  maxRunDurationSeconds: number;
  maxOutputBytes: bigint;
  toolPolicy: {
    message: boolean;
    work: boolean;
    artifact: boolean;
    knowledge: boolean;
  };
}): Promise<AgentRuntimeSpec> {
  const response = await agentClient.updateAgentRuntimeSpec(input);
  if (!response.runtimeSpec) {
    throw new Error("Update runtime spec response was empty");
  }
  return response.runtimeSpec;
}

export async function enqueueCredentialDelivery(input: {
  requestId: string;
  computerId: string;
  agentId: string;
  credentialKind: CredentialKind;
  sealedCredential: BrowserSealedCredential;
  expiresAt: Date;
}): Promise<CredentialDelivery> {
  const response = await computerClient.enqueueCredentialDelivery({
    requestId: input.requestId,
    computerId: input.computerId,
    agentId: input.agentId,
    credentialKind: input.credentialKind,
    sealedCredential: {
      algorithm: CredentialDeliveryAlgorithm.X25519_XCHACHA20_POLY1305,
      keyId: input.sealedCredential.keyId,
      ephemeralPublicKey: input.sealedCredential.ephemeralPublicKey,
      nonce: input.sealedCredential.nonce,
      ciphertext: input.sealedCredential.ciphertext,
    },
    expiresAt: timestampFromDate(input.expiresAt),
  });
  if (!response.delivery) {
    throw new Error("Credential delivery response was empty");
  }
  return response.delivery;
}

export async function listCredentialDeliveries(input: {
  computerId: string;
  agentId: string;
}): Promise<CredentialDelivery[]> {
  const response = await computerClient.listCredentialDeliveries(input);
  return response.deliveries;
}

export async function waitForCredentialDelivery(input: {
  deliveryId: string;
  computerId: string;
  agentId: string;
  signal?: AbortSignal;
  timeoutMs?: number;
  intervalMs?: number;
}): Promise<CredentialDelivery> {
  const timeoutMs = input.timeoutMs ?? 60_000;
  const intervalMs = input.intervalMs ?? 750;
  const deadline = Date.now() + timeoutMs;
  for (;;) {
    input.signal?.throwIfAborted();
    const deliveries = await listCredentialDeliveries({
      computerId: input.computerId,
      agentId: input.agentId,
    });
    const delivery = deliveries.find((item) => item.id === input.deliveryId);
    if (delivery?.state === CredentialDeliveryState.SUCCEEDED) return delivery;
    if (
      delivery?.state === CredentialDeliveryState.FAILED ||
      delivery?.state === CredentialDeliveryState.EXPIRED
    ) {
      throw new Error(
        delivery.errorCode ||
          (delivery.state === CredentialDeliveryState.EXPIRED
            ? "Credential delivery expired."
            : "Credential delivery failed."),
      );
    }
    if (Date.now() >= deadline) {
      throw new Error("Credential delivery did not finish before the timeout.");
    }
    await delay(
      Math.min(intervalMs, Math.max(0, deadline - Date.now())),
      input.signal,
    );
  }
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
    return "An Agent with this handle already exists.";
  }
  if (connectError.code === Code.InvalidArgument) {
    return connectError.rawMessage || `The ${action} request is invalid.`;
  }
  if (connectError.code === Code.NotFound) {
    return "The selected Agent or Computer no longer exists. Refresh and try again.";
  }
  if (connectError.code === Code.Aborted) {
    return `The ${action} facts changed. Refresh and retry.`;
  }
  if (connectError.code === Code.FailedPrecondition) {
    return (
      connectError.rawMessage || `The ${action} preconditions are not met.`
    );
  }
  if (connectError.code === Code.Unavailable) {
    return "Server unavailable. Check the connection and retry.";
  }
  return `Could not ${action}. Retry when the Server is available.`;
}

function delay(milliseconds: number, signal?: AbortSignal) {
  return new Promise<void>((resolve, reject) => {
    if (signal?.aborted) {
      reject(signal.reason);
      return;
    }
    const timeout = window.setTimeout(resolve, milliseconds);
    signal?.addEventListener(
      "abort",
      () => {
        window.clearTimeout(timeout);
        reject(signal.reason);
      },
      { once: true },
    );
  });
}
