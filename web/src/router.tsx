import {
  createRootRoute,
  createRoute,
  createRouter,
  redirect,
  type RouterHistory,
} from "@tanstack/react-router";

import { RouteChrome } from "./RouteChrome";
import { ChannelPage } from "./screens/ChannelPage";
import { AgentDetailPage } from "./screens/AgentDetailPage";
import { ComputersPage } from "./screens/ComputersPage";
import { InvitationPage } from "./screens/InvitationPage";
import { InboxPage } from "./screens/InboxPage";
import { DirectMessagePage } from "./screens/DirectMessagePage";
import { LoginPage } from "./screens/LoginPage";
import { MembersPage } from "./screens/MembersPage";
import { RegisterPage } from "./screens/RegisterPage";
import { PairComputerPage } from "./screens/PairComputerPage";
import { SpaceCreatePage } from "./screens/SpaceCreatePage";
import { TaskDetailPage, TasksPage } from "./screens/TasksPage";

const rootRoute = createRootRoute({ component: RouteChrome });
const registerRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: "/",
  validateSearch: (search: Record<string, unknown>) => ({
    redirect: typeof search.redirect === "string" ? search.redirect : undefined,
  }),
  component: RegisterPage,
});
const loginRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: "/login",
  validateSearch: (search: Record<string, unknown>) => ({
    redirect: typeof search.redirect === "string" ? search.redirect : undefined,
  }),
  component: LoginPage,
});
const createSpaceRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: "/spaces/new",
  component: SpaceCreatePage,
});
const spaceEntryRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: "/s/$spaceSlug",
  beforeLoad: ({ params }) => {
    throw redirect({
      to: "/s/$spaceSlug/channels/$channelSlug",
      params: { spaceSlug: params.spaceSlug, channelSlug: "general" },
    });
  },
});
const channelRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: "/s/$spaceSlug/channels/$channelSlug",
  component: ChannelPage,
});
const membersRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: "/s/$spaceSlug/members",
  component: MembersPage,
});
const agentDetailRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: "/s/$spaceSlug/agents/$agentId",
  component: AgentDetailPage,
});
const inboxRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: "/s/$spaceSlug/inbox",
  component: InboxPage,
});
const tasksRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: "/s/$spaceSlug/tasks",
  component: TasksPage,
});
const taskDetailRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: "/s/$spaceSlug/tasks/$taskId",
  component: TaskDetailPage,
});
const computersRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: "/s/$spaceSlug/computers",
  component: ComputersPage,
});
const pairComputerRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: "/pair-computer/$pairingId",
  validateSearch: (search: Record<string, unknown>) => ({
    code: typeof search.code === "string" ? search.code : typeof search.code === "number" ? String(search.code) : "",
  }),
  component: PairComputerPage,
});
const invitationRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: "/invite/$inviteToken",
  component: InvitationPage,
});
const directMessageRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: "/s/$spaceSlug/dm/$memberId",
  component: DirectMessagePage,
});

const routeTree = rootRoute.addChildren([
  registerRoute,
  loginRoute,
  createSpaceRoute,
  spaceEntryRoute,
  channelRoute,
  membersRoute,
  agentDetailRoute,
  inboxRoute,
  tasksRoute,
  taskDetailRoute,
  computersRoute,
  pairComputerRoute,
  invitationRoute,
  directMessageRoute,
]);

export function createAppRouter(history?: RouterHistory) {
  return createRouter({
    routeTree,
    history,
    parseSearch: (searchStr) => Object.fromEntries(new URLSearchParams(searchStr)),
  });
}

export const router = createAppRouter();

declare module "@tanstack/react-router" {
  interface Register {
    router: typeof router;
  }
}
