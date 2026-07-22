import type { Agent } from "../../gen/sumi/agent/v1/agent_pb";
import type {
  Human,
  Organization,
} from "../../gen/sumi/organization/v1/organization_pb";
import type {
  Membership,
  Message,
  Space,
} from "../../gen/sumi/space/v1/space_pb";

export type PermissionState =
  | { status: "allowed" }
  | { status: "denied" }
  | { status: "unknown"; error: string };

export type DirectorySnapshot = {
  organization: Organization;
  humans: Human[];
  agents: Agent[];
  spaces: Space[];
  createSpace: PermissionState;
};

export type SpacePermissions = {
  members: PermissionState;
  archive: PermissionState;
  send: PermissionState;
};

export type ConversationSnapshot = {
  space: Space;
  memberships: Membership[];
  messages: Message[];
  permissions: SpacePermissions;
  hasMore: boolean;
  nextAfterSequence: bigint;
};

export type ThreadSnapshot = {
  root: Message;
  replies: Message[];
  hasMore: boolean;
  nextAfterSequence: bigint;
};

export type MessagePage = {
  messages: Message[];
  hasMore: boolean;
  nextAfterSequence: bigint;
};
