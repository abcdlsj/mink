import {
  PrincipalKind,
  type Message,
  type Principal,
  type Space,
} from "../../gen/sumi/space/v1/space_pb";
import { collaborationClient } from "../../api/clients";
import { required } from "./required";

export function principalForHuman(id: string): Principal {
  return { kind: PrincipalKind.HUMAN, id } as Principal;
}

export function principalForAgent(id: string): Principal {
  return { kind: PrincipalKind.AGENT, id } as Principal;
}

export async function createDM(input: {
  requestId: string;
  peer: Principal;
}): Promise<Space> {
  const response = await collaborationClient.createDM(input);
  return required(response.space, "Create DM response was empty");
}

export async function createGroup(input: {
  requestId: string;
  name: string;
}): Promise<Space> {
  const response = await collaborationClient.createGroup(input);
  return required(response.space, "Create Group response was empty");
}

export async function addSpaceMember(input: {
  requestId: string;
  spaceId: string;
  member: Principal;
}): Promise<void> {
  await collaborationClient.addMember(input);
}

export async function removeSpaceMember(input: {
  requestId: string;
  spaceId: string;
  member: Principal;
}): Promise<void> {
  await collaborationClient.removeMember(input);
}

export async function setSpaceArchived(input: {
  requestId: string;
  spaceId: string;
  archived: boolean;
}): Promise<void> {
  if (input.archived) {
    await collaborationClient.archiveSpace(input);
  } else {
    await collaborationClient.unarchiveSpace(input);
  }
}

export async function sendSpaceMessage(input: {
  requestId: string;
  spaceId: string;
  body: string;
}): Promise<Message> {
  const response = await collaborationClient.sendMessage({
    requestId: input.requestId,
    target: { target: { case: "spaceId", value: input.spaceId } },
    body: input.body,
  });
  return required(response.message, "Send Message response was empty");
}

export async function sendThreadMessage(input: {
  requestId: string;
  threadRootMessageId: string;
  body: string;
}): Promise<Message> {
  const response = await collaborationClient.sendMessage({
    requestId: input.requestId,
    target: {
      target: {
        case: "threadRootMessageId",
        value: input.threadRootMessageId,
      },
    },
    body: input.body,
  });
  return required(response.message, "Send Thread reply response was empty");
}
