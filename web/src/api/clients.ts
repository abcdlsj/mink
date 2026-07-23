import { createClient } from "@connectrpc/connect";
import { createConnectTransport } from "@connectrpc/connect-web";
import { AgentService } from "../gen/sumi/agent/v1/agent_pb";
import { ArtifactService } from "../gen/sumi/artifact/v1/artifact_pb";
import { ComputerService } from "../gen/sumi/computer/v1/computer_pb";
import { GrantService } from "../gen/sumi/grant/v1/grant_pb";
import { OrganizationService } from "../gen/sumi/organization/v1/organization_pb";
import { PlacementService } from "../gen/sumi/placement/v1/placement_pb";
import { CollaborationService } from "../gen/sumi/space/v1/space_pb";
import { SystemService } from "../gen/sumi/system/v1/system_pb";
import { WorkService } from "../gen/sumi/work/v1/work_pb";
import {
  InboxService,
  WorkAttentionService,
} from "../gen/sumi/inbox/v1/inbox_pb";

const transport = createConnectTransport({ baseUrl: window.location.origin });

export const systemClient = createClient(SystemService, transport);
export const agentClient = createClient(AgentService, transport);
export const computerClient = createClient(ComputerService, transport);
export const grantClient = createClient(GrantService, transport);
export const organizationClient = createClient(OrganizationService, transport);
export const placementClient = createClient(PlacementService, transport);
export const collaborationClient = createClient(
  CollaborationService,
  transport,
);
export const artifactClient = createClient(ArtifactService, transport);
export const workClient = createClient(WorkService, transport);
export const inboxClient = createClient(InboxService, transport);
export const workAttentionClient = createClient(
  WorkAttentionService,
  transport,
);
