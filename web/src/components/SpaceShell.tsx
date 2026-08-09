import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Link, useLocation, useNavigate } from "@tanstack/react-router";
import {
  Hash,
  Building2,
  Inbox,
  LockKeyhole,
  ListTodo,
  LoaderCircle,
  MessageCircle,
  Monitor,
  Network,
  Palette,
  Plus,
  Settings,
  Users,
  X,
  type LucideIcon,
} from "lucide-react";
import type { CSSProperties, FormEvent, ReactNode } from "react";
import { useEffect, useRef, useState } from "react";

import {
  ApiRequestError,
  createChannel,
  createDirectMessage,
  currentUser,
  getSpaceBySlug,
  joinChannel,
  listChannels,
  listTasks,
  listAgents,
  listComputers,
  listDirectMessages,
  listMembers,
  type Channel,
  type Agent,
  type Computer,
  type DirectMessage,
  type Member,
  type Space,
  type Task,
  type User,
} from "../api/client";
import { activityLabel } from "../agentActivity";
import { designLabEnabled } from "../featureFlags";
import { AGENT_GRAPH_FEATURE_ID, useFeatureEnabled } from "../featureRegistry";
import { useSpaceEvents } from "../hooks/useSpaceEvents";
import { DialogFrame } from "./DialogFrame";
import { PixelIdentity } from "./PixelIdentity";
import { PixelWord } from "./PixelWord";
import { SettingsNavigation } from "./SettingsNavigation";
import { SumiMark } from "./SumiMark";

export { PixelIdentity } from "./PixelIdentity";

export interface SpaceShellContext {
  space: Space;
  user: User;
  navigationOpen: boolean;
  channels: Channel[];
  directMessages: DirectMessage[];
  members: Member[];
  currentMember: Member;
}

export function SpaceShell({
  spaceSlug,
  active,
  children,
}: {
  spaceSlug: string;
  // "design" is the demo-only candidate board; it has no rail entry.
  active: "channel" | "dm" | "company" | "members" | "agents" | "inbox" | "tasks" | "computers" | "graph" | "settings" | "design";
  children: (context: SpaceShellContext) => ReactNode;
}) {
  const navigate = useNavigate();
  const location = useLocation();
  const authenticationRedirect = useRef(location.href);
  const queryClient = useQueryClient();
  const [navigationOpen, setNavigationOpen] = useState(false);
  const [navigationCollapsed, setNavigationCollapsed] = useState(false);
  const [navigationTrigger, setNavigationTrigger] = useState<HTMLElement | null>(null);
  const [channelFormOpen, setChannelFormOpen] = useState(false);
  const [directMessageFormOpen, setDirectMessageFormOpen] = useState(false);
  const agentGraphEnabled = useFeatureEnabled(AGENT_GRAPH_FEATURE_ID);
  const [unreadChannelIds, setUnreadChannelIds] = useState<ReadonlySet<string>>(() => new Set());
  const navigationPanel = useRef<HTMLElement>(null);
  const railNavigationTrigger = useRef<HTMLButtonElement>(null);
  function closeNavigation() {
    setNavigationOpen(false);
    setNavigationCollapsed(true);
    window.requestAnimationFrame(() => (navigationTrigger ?? railNavigationTrigger.current)?.focus());
  }
  function dismissNavigationDrawer() {
    setNavigationOpen(false);
  }
  function openNavigation() {
    setNavigationTrigger(document.activeElement as HTMLElement);
    setNavigationCollapsed(false);
    setNavigationOpen(true);
  }
  const space = useQuery({
    queryKey: ["space", spaceSlug],
    queryFn: () => getSpaceBySlug(spaceSlug),
  });
  const user = useQuery({ queryKey: ["current-user"], queryFn: currentUser });
  const channels = useQuery({
    queryKey: ["channels", space.data?.id],
    queryFn: () => listChannels(space.data!.id),
    enabled: Boolean(space.data),
  });
  const directMessages = useQuery({
    queryKey: ["direct-messages", space.data?.id],
    queryFn: () => listDirectMessages(space.data!.id),
    enabled: Boolean(space.data),
  });
  const members = useQuery({
    queryKey: ["members", space.data?.id],
    queryFn: () => listMembers(space.data!.id),
    enabled: Boolean(space.data),
  });
  const agents = useQuery({
    queryKey: ["agents", space.data?.id],
    queryFn: () => listAgents(space.data!.id),
    enabled: Boolean(space.data),
    retry: false,
  });
  const computers = useQuery({
    queryKey: ["computers", space.data?.id],
    queryFn: () => listComputers(space.data!.id),
    enabled: active === "computers" && Boolean(space.data),
  });
  const navTasks = useQuery({
    queryKey: ["nav-tasks", space.data?.id],
    queryFn: () => listTasks(space.data!.id),
    enabled: active === "tasks" && Boolean(space.data),
  });
  useSpaceEvents(space.data?.id, ({ channelId }) => {
    if (channelId === viewingChannelId(location.pathname, channels.data?.channels, directMessages.data)) {
      return;
    }
    setUnreadChannelIds((current) =>
      current.has(channelId) ? current : new Set(current).add(channelId),
    );
  });
  const channelCreation = useMutation({
    mutationFn: (input: Parameters<typeof createChannel>[1]) => createChannel(space.data!.id, input),
    onSuccess: (channel) => {
      void queryClient.invalidateQueries({ queryKey: ["channels", space.data?.id] });
      setChannelFormOpen(false);
      setNavigationOpen(false);
      void navigate({
        to: "/s/$spaceSlug/channels/$channelSlug",
        params: { spaceSlug: space.data!.slug, channelSlug: channel.slug },
      });
    },
  });
  const channelJoin = useMutation({
    mutationFn: joinChannel,
    onSuccess: (channel) => {
      void queryClient.invalidateQueries({ queryKey: ["channels", space.data?.id] });
      setNavigationOpen(false);
      void navigate({
        to: "/s/$spaceSlug/channels/$channelSlug",
        params: { spaceSlug: space.data!.slug, channelSlug: channel.slug },
      });
    },
  });
  const directMessageCreation = useMutation({
    mutationFn: (memberId: string) => createDirectMessage(space.data!.id, memberId),
    onSuccess: (directMessage) => {
      queryClient.setQueryData<DirectMessage[]>(
        ["direct-messages", space.data!.id],
        (current = []) => [
          ...current.filter((candidate) => candidate.channel_id !== directMessage.channel_id),
          directMessage,
        ],
      );
      setDirectMessageFormOpen(false);
      setNavigationOpen(false);
      void navigate({
        to: "/s/$spaceSlug/dm/$memberId",
        params: {
          spaceSlug: space.data!.slug,
          memberId: directMessage.other_member.id,
        },
      });
    },
  });
  const routeError =
    space.error ??
    user.error ??
    channels.error ??
    members.error ??
    (active === "computers" ? computers.error : undefined);

  useEffect(() => {
    if (routeError instanceof ApiRequestError && routeError.status === 401) {
      void navigate({
        to: "/login",
        search: { redirect: authenticationRedirect.current },
        replace: true,
      });
    }
  }, [navigate, routeError]);

  useEffect(() => {
    if (!navigationOpen) return;
    const panel = navigationPanel.current;
    const closeButton = panel?.querySelector<HTMLButtonElement>(".navigation-close");
    closeButton?.focus();
    const handleKey = (event: KeyboardEvent) => {
      if (document.querySelector('[role="dialog"][aria-modal="true"]')) return;
      if (event.key === "Escape") {
        setNavigationOpen(false);
        setNavigationCollapsed(true);
        window.requestAnimationFrame(() => navigationTrigger?.focus());
        return;
      }
      const drawerMode = window.matchMedia?.("(max-width: 1099px)").matches ?? false;
      if (event.key !== "Tab" || !panel || !drawerMode) return;
      const focusable = [...panel.querySelectorAll<HTMLElement>('a[href], button:not([disabled]), input, select, textarea')];
      if (!focusable.length) return;
      const first = focusable[0];
      const last = focusable[focusable.length - 1];
      if (event.shiftKey && document.activeElement === first) { event.preventDefault(); last.focus(); }
      else if (!event.shiftKey && document.activeElement === last) { event.preventDefault(); first.focus(); }
    };
    document.addEventListener("keydown", handleKey);
    return () => document.removeEventListener("keydown", handleKey);
  }, [navigationOpen, navigationTrigger]);

  // SPA navigation does not move focus on its own. After a route change,
  // focus the new view's h1 so keyboard users land at the content start.
  useEffect(() => {
    const heading = document.querySelector<HTMLElement>(".space-shell h1");
    if (!heading) return;
    heading.tabIndex = -1;
    heading.focus({ preventScroll: true });
  }, [location.pathname]);

  // Visiting a conversation clears its unread dot; everything else keeps it.
  useEffect(() => {
    const channelId = viewingChannelId(location.pathname, channels.data?.channels, directMessages.data);
    if (!channelId || !unreadChannelIds.has(channelId)) return;
    window.requestAnimationFrame(() => {
      setUnreadChannelIds((current) => {
        if (!current.has(channelId)) return current;
        const next = new Set(current);
        next.delete(channelId);
        return next;
      });
    });
  }, [location.pathname, channels.data, directMessages.data, unreadChannelIds]);

  if (
    space.isPending ||
    user.isPending ||
    channels.isPending ||
    members.isPending || (active === "computers" && computers.isPending)
  ) {
    return <div className="route-status">Opening Space...</div>;
  }
  if (routeError) {
    return <RouteFailure error={routeError} retry={() => {
      void queryClient.invalidateQueries();
    }} />;
  }
  if (!space.data || !user.data || !channels.data || !members.data) {
    return <div className="route-status">Opening Space...</div>;
  }
  const currentMember = members.data.find((member) => member.id === space.data.current_member_id);
  if (!currentMember) {
    return <div className="route-status route-status--error">Member identity unavailable.</div>;
  }
  const availableDirectMessages = directMessages.data ?? [];
  const activityByMemberId = new Map(
    (agents.data ?? []).map((agent) => [agent.member_id, agent.activity_status] as const),
  );
  const roleByMemberId = new Map(
    (agents.data ?? []).map((agent) => [agent.member_id, agent.role_text] as const),
  );

  return (
    <>
      <a className="skip-link" href="#main-content">Skip to content</a>
      <main
        id="main-content"
        className={`space-shell${navigationCollapsed ? " space-shell--navigation-collapsed" : ""}`}
        style={{ "--space-accent": space.data.accent } as CSSProperties}
      >
      <aside
        className="space-rail"
        aria-label="Space tools"
        onClick={(event) => {
          if (!(event.target as HTMLElement).closest("a, button")) openNavigation();
        }}
      >
        <Link
          className="space-badge"
          to="/s/$spaceSlug"
          params={{ spaceSlug: space.data.slug }}
          aria-label="Sumi home"
          title="Sumi home"
        >
          <SumiMark className="space-brand-mark" />
        </Link>
        <nav className="rail-tools" aria-label="Space management">
          <RailItem
            icon={MessageCircle}
            label="Conversation"
            active={active === "channel" || active === "dm"}
            href={`/s/${space.data.slug}/channels/general`}
          />
          <RailItem
            icon={Building2}
            label="Company"
            active={active === "company"}
            href={`/s/${space.data.slug}/company`}
          />
          <RailItem
            icon={Inbox}
            label="Inbox"
            active={active === "inbox"}
            href={`/s/${space.data.slug}/inbox`}
          />
          <RailItem
            icon={ListTodo}
            label="Tasks"
            active={active === "tasks"}
            href={`/s/${space.data.slug}/tasks`}
          />
          <RailItem
            icon={Users}
            label="Members"
            active={active === "members" || active === "agents"}
            href={`/s/${space.data.slug}/members`}
          />
          <RailItem
            icon={Monitor}
            label="Computers"
            active={active === "computers"}
            href={`/s/${space.data.slug}/computers`}
          />
          {agentGraphEnabled ? (
            <RailItem
              icon={Network}
              label="Agent graph"
              active={active === "graph"}
              href={`/s/${space.data.slug}/graph`}
            />
          ) : null}
          {designLabEnabled() ? (
            <RailItem
              icon={Palette}
              label="Design lab"
              active={active === "design"}
              href={`/s/${space.data.slug}/design-lab`}
            />
          ) : null}
        </nav>
        <button
          ref={railNavigationTrigger}
          className="rail-spacer"
          type="button"
          aria-label="Open navigation"
          title="Open navigation"
          onClick={openNavigation}
        />
        <RailItem
          icon={Settings}
          label="Settings"
          active={active === "settings"}
          href={`/s/${space.data.slug}/settings`}
        />
      </aside>

      {navigationOpen ? (
        <button
          className="navigation-scrim"
          type="button"
          aria-label="Close navigation"
          onClick={closeNavigation}
        />
      ) : null}
      <aside
        ref={navigationPanel}
        className={`space-navigation${navigationOpen ? " space-navigation--open" : ""}`}
        aria-label="Space navigation"
        onClick={(event) => {
          if ((event.target as HTMLElement).closest("a")) dismissNavigationDrawer();
        }}
      >
        <header className="space-name-row">
          <PixelWord text={space.data.name} className="space-name-pixel" variant="bold" />
          <button
            className="navigation-close icon-button"
            type="button"
            aria-label="Close navigation"
            title="Close navigation"
            onClick={closeNavigation}
          >
            <X />
          </button>
        </header>
        <nav>
          <div className="mobile-space-tools" aria-label="Space tools">
            <NavigationItem
              icon={MessageCircle}
              label="Conversation"
              active={active === "channel" || active === "dm"}
              href={`/s/${space.data.slug}/channels/general`}
            />
            <NavigationItem
              icon={Building2}
              label="Company"
              active={active === "company"}
              href={`/s/${space.data.slug}/company`}
            />
            <NavigationItem
              icon={Inbox}
              label="Inbox"
              active={active === "inbox"}
              href={`/s/${space.data.slug}/inbox`}
            />
            <NavigationItem
              icon={Users}
              label="Members"
              active={active === "members" || active === "agents"}
              href={`/s/${space.data.slug}/members`}
            />
            <NavigationItem
              icon={ListTodo}
              label="Tasks"
              active={active === "tasks"}
              href={`/s/${space.data.slug}/tasks`}
            />
            <NavigationItem
              icon={Monitor}
              label="Computers"
              active={active === "computers"}
              href={`/s/${space.data.slug}/computers`}
            />
            {agentGraphEnabled ? (
              <NavigationItem
                icon={Network}
                label="Agent graph"
                active={active === "graph"}
                href={`/s/${space.data.slug}/graph`}
              />
            ) : null}
            <NavigationItem
              icon={Settings}
              label="Settings"
              active={active === "settings"}
              href={`/s/${space.data.slug}/settings`}
            />
          </div>
          {active === "members" || active === "agents" ? (
            <MembersNavigation members={members.data} activityByMemberId={activityByMemberId} roleByMemberId={roleByMemberId} spaceSlug={space.data.slug} locationPath={location.pathname} />
          ) : active === "computers" ? (
            <ComputersNavigation
              computers={computers.data ?? []}
              spaceSlug={space.data.slug}
              activeHash={location.hash}
              canManage={["owner", "admin"].includes(currentMember.access_level)}
            />
          ) : active === "inbox" ? (
            <InboxNavigation />
          ) : active === "tasks" ? (
            <TasksNavigation tasks={navTasks.data ?? []} spaceSlug={space.data.slug} pending={navTasks.isPending} />
          ) : active === "company" ? (
            <CompanyNavigation
              spaceSlug={space.data.slug}
              locationPath={location.pathname}
            />
          ) : active === "settings" ? (
            <SettingsNavigation spaceSlug={space.data.slug} />
          ) : (
            <>
          <div className="nav-section-heading">
            <p className="nav-label">CHANNELS <span>{channels.data.channels.filter((channel) => channel.joined).length}</span></p>
            {channels.data.can_create ? (
              <button
                type="button"
                aria-label="Create Channel"
                title="Create Channel"
                onClick={() => {
                  setChannelFormOpen(true);
                  channelCreation.reset();
                }}
              >
                <Plus />
              </button>
            ) : null}
          </div>
          {channels.data.channels
            .filter((channel) => channel.joined)
            .map((channel) => (
              <NavigationItem
                key={channel.id}
                icon={channel.kind === "private" ? LockKeyhole : Hash}
                label={channel.slug}
                active={
                  active === "channel" &&
                  location.pathname.endsWith(`/channels/${channel.slug}`)
                }
                href={`/s/${space.data.slug}/channels/${channel.slug}`}
                unread={unreadChannelIds.has(channel.id)}
              />
            ))}
          {channels.data.channels.some((channel) => !channel.joined) ? (
            <>
              <p className="nav-label">DISCOVER</p>
              {channels.data.channels
                .filter((channel) => !channel.joined)
                .map((channel) => (
                  <div className="discover-channel" key={channel.id}>
                    <Hash aria-hidden="true" />
                    <span title={channel.topic ?? channel.slug}>{channel.slug}</span>
                    <button
                      type="button"
                      disabled={channelJoin.isPending}
                      aria-label={`Join #${channel.slug}`}
                      title={`Join #${channel.slug}`}
                      onClick={() => channelJoin.mutate(channel.id)}
                    >
                      JOIN
                    </button>
                  </div>
                ))}
            </>
          ) : null}
          <div className="nav-section-heading nav-section-heading--dms">
            <p className="nav-label nav-label--section">DMS <span>{availableDirectMessages.length}</span></p>
            <button
              type="button"
              aria-label="Start DM"
              title="Start DM"
              onClick={() => {
                setDirectMessageFormOpen(true);
                directMessageCreation.reset();
              }}
            >
              <Plus />
            </button>
          </div>
          {directMessages.error ? (
            <span className="nav-empty nav-empty--centered">DMs unavailable</span>
          ) : availableDirectMessages.length === 0 ? (
            <span className="nav-empty nav-empty--centered">No DMs yet</span>
          ) : null}
          {availableDirectMessages.map((dm) => (
            <DirectMessageNavigationItem
              key={dm.channel_id}
              member={dm.other_member}
              activityStatus={activityByMemberId.get(dm.other_member.id)}
              active={
                active === "dm" &&
                location.pathname.endsWith(`/dm/${dm.other_member.id}`)
              }
              href={`/s/${space.data.slug}/dm/${dm.other_member.id}`}
              unread={unreadChannelIds.has(dm.channel_id)}
            />
          ))}
            </>
          )}
        </nav>
      </aside>

      {channelFormOpen ? (
        <ChannelDialog
          agents={members.data.filter((member) => member.kind === "agent")}
          pending={channelCreation.isPending}
          error={channelCreation.error?.message}
          close={() => setChannelFormOpen(false)}
          onSubmit={(input) => channelCreation.mutate(input)}
        />
      ) : null}

      {directMessageFormOpen ? (
        <DirectMessageDialog
          members={members.data.filter((member) => member.id !== currentMember.id)}
          activityByMemberId={activityByMemberId}
          existingMemberIds={new Set(availableDirectMessages.map((dm) => dm.other_member.id))}
          pendingMemberId={directMessageCreation.isPending ? directMessageCreation.variables : undefined}
          error={directMessageCreation.error?.message}
          close={() => setDirectMessageFormOpen(false)}
          onSelect={(memberId) => directMessageCreation.mutate(memberId)}
        />
      ) : null}

        {children({
          space: space.data,
          user: user.data,
          channels: channels.data.channels,
          directMessages: availableDirectMessages,
          members: members.data,
          currentMember,
          navigationOpen,
        })}
      </main>
    </>
  );
}

function RouteFailure({ error, retry }: { error: unknown; retry: () => void }) {
  const message = error instanceof ApiRequestError ? error.message : "The Server did not return a usable response.";
  return <main className="route-status route-status--error"><section className="route-status-panel" role="alert"><p className="section-kicker">COULD NOT OPEN SPACE</p><h1>Something interrupted this view.</h1><p>{message}</p><button className="command-button command-button--accent" type="button" onClick={retry}>Retry</button></section></main>;
}

function MembersNavigation({ members, activityByMemberId, roleByMemberId, spaceSlug, locationPath }: { members: Member[]; activityByMemberId: Map<string, Agent["activity_status"]>; roleByMemberId: Map<string, string>; spaceSlug: string; locationPath: string }) {
  const agents = members.filter((member) => member.kind === "agent");
  const humans = members.filter((member) => member.kind === "human");
  return (
    <div className="members-navigation">
      <MemberNavigationGroup
        label="Agents"
        members={agents}
        activityByMemberId={activityByMemberId}
        roleByMemberId={roleByMemberId}
        spaceSlug={spaceSlug}
        locationPath={locationPath}
      />
      <MemberNavigationGroup
        label="Humans"
        members={humans}
        activityByMemberId={activityByMemberId}
        roleByMemberId={roleByMemberId}
        spaceSlug={spaceSlug}
        locationPath={locationPath}
      />
    </div>
  );
}

function MemberNavigationGroup({ label, members, activityByMemberId, roleByMemberId, spaceSlug, locationPath }: { label: string; members: Member[]; activityByMemberId: Map<string, Agent["activity_status"]>; roleByMemberId: Map<string, string>; spaceSlug: string; locationPath: string }) {
  return (
    <section className="member-navigation-group" aria-labelledby={`member-navigation-${label.toLowerCase()}`}>
      <h3 className="nav-label" id={`member-navigation-${label.toLowerCase()}`}>
        {label === "Agents" ? <Link className="member-navigation-heading-link" to="/s/$spaceSlug/agents" params={{ spaceSlug }}>{label}</Link> : label}<span>{members.length}</span>
      </h3>
      {members.length ? members.map((member) => member.kind === "agent" ? (
        <Link key={member.id} className={`context-entity-row${locationPath.endsWith(`/agents/${member.id}`) ? " context-entity-row--active" : ""}`} to="/s/$spaceSlug/agents/$agentId" params={{ spaceSlug, agentId: member.id }}>
          <PresenceIdentity name={member.display_name} kind="agent" seed={member.id} activityStatus={activityByMemberId.get(member.id)} />
          <span><strong title={member.display_name}>{member.display_name}</strong><small title={roleByMemberId.get(member.id)}>{roleByMemberId.get(member.id) ?? "Agent"}</small></span>
        </Link>
      ) : (
        <div className="context-entity-row context-entity-row--static" key={member.id}>
          <PixelIdentity name={member.display_name} kind="human" seed={member.id} />
          <span><strong title={member.display_name}>{member.display_name}</strong></span>
        </div>
      )) : <p className="nav-empty">No {label.toLowerCase()}</p>}
    </section>
  );
}

function ComputersNavigation({ computers, spaceSlug, activeHash, canManage }: { computers: Computer[]; spaceSlug: string; activeHash: string; canManage: boolean }) {
  const normalizedHash = activeHash.replace(/^#/, "");
  return <>
    <div className="nav-section-heading computer-nav-heading">
      <p className="nav-label">COMPUTERS <span>{computers.length}</span></p>
      {canManage ? <Link to="/s/$spaceSlug/computers" params={{ spaceSlug }} hash="pair-computer" aria-label="Pair Computer" title="Pair Computer"><Plus /></Link> : null}
    </div>
    {computers.length ? computers.map((computer) => {
      const hash = `computer-${computer.id}`;
      const selected = normalizedHash === hash;
      return <Link key={computer.id} className={`context-entity-row computer-context-row${selected ? " context-entity-row--active" : ""}`} to="/s/$spaceSlug/computers" params={{ spaceSlug }} hash={hash}>
        <Monitor aria-hidden="true" />
        <span><strong title={computer.name}>{computer.name}</strong><small title={`${computer.status} · daemon v${computer.daemon_version}`}><i className={`computer-presence computer-presence--${computer.status}`} aria-hidden="true" />{computer.status} <em>v{computer.daemon_version}</em></small></span>
      </Link>;
    }) : <div className="computer-nav-empty"><Monitor aria-hidden="true" /><p>No paired Computers</p>{canManage ? <Link to="/s/$spaceSlug/computers" params={{ spaceSlug }} hash="pair-computer">Pair Computer</Link> : null}</div>}
  </>;
}

function InboxNavigation() {
  return <div className="context-groups" aria-label="Inbox groups"><span><strong>01</strong> DM &amp; mentions</span><span><strong>02</strong> Replies</span><span><strong>03</strong> Channel activity</span></div>;
}

function TasksNavigation({ tasks, spaceSlug, pending }: { tasks: Task[]; spaceSlug: string; pending: boolean }) {
  if (pending) return <div className="nav-empty">Loading tasks…</div>;
  if (!tasks.length) return <div className="nav-empty">No tasks yet</div>;
  return (
    <div className="nav-task-list">
      <p className="nav-label">RECENT TASKS</p>
      {tasks.slice(0, 6).map((task) => (
        <Link key={task.id} className="nav-task-item" to="/s/$spaceSlug/tasks/$taskId" params={{ spaceSlug, taskId: task.id }}>
          <span className={`nav-task-status nav-task-status--${task.status}`}>{task.status.replace("_", " ")}</span>
          <span title={task.title}>{task.title}</span>
        </Link>
      ))}
    </div>
  );
}

function CompanyNavigation({
  spaceSlug,
  locationPath,
}: {
  spaceSlug: string;
  locationPath: string;
}) {
  const officeActive = locationPath.endsWith("/company/office") || locationPath.endsWith("/company");
  return (
    <div className="company-navigation">
      <p className="nav-label">COMPANY</p>
      <NavigationItem
        icon={Building2}
        label="Office"
        active={officeActive}
        href={`/s/${spaceSlug}/company/office`}
      />
    </div>
  );
}

function viewingChannelId(
  pathname: string,
  channels: Channel[] | undefined,
  directMessages: DirectMessage[] | undefined,
): string | undefined {
  const channelMatch = pathname.match(/\/channels\/([^/]+)$/);
  if (channelMatch && channels) {
    return channels.find((candidate) => candidate.slug === channelMatch[1])?.id;
  }
  const dmMatch = pathname.match(/\/dm\/([^/]+)$/);
  if (dmMatch && directMessages) {
    return directMessages.find(
      (candidate) => candidate.other_member.id === dmMatch[1],
    )?.channel_id;
  }
  return undefined;
}

function RailItem({
  icon: Icon,
  label,
  active = false,
  href,
  disabled = false,
}: {
  icon: LucideIcon;
  label: string;
  active?: boolean;
  href?: string;
  disabled?: boolean;
}) {
  const className = `rail-tool${active ? " rail-tool--active" : ""}${disabled ? " rail-tool--disabled" : ""}`;
  const content = <Icon aria-hidden="true" />;
  return href ? (
    <Link className={className} to={href} aria-label={label} title={label} aria-current={active ? "page" : undefined}>
      {content}
    </Link>
  ) : (
    <button className={className} type="button" aria-label={label} title={label} disabled>
      {content}
    </button>
  );
}

function ChannelDialog({
  agents,
  pending,
  error,
  close,
  onSubmit,
}: {
  agents: Member[];
  pending: boolean;
  error?: string;
  close: () => void;
  onSubmit: (input: Parameters<typeof createChannel>[1]) => void;
}) {
  function submit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    const form = new FormData(event.currentTarget);
    onSubmit({
      slug: String(form.get("slug") ?? ""),
      kind: String(form.get("kind") ?? "public") as "public" | "private",
      topic: String(form.get("topic") ?? ""),
      agent_member_ids: form.getAll("agent_member_ids").map(String),
    });
  }

  return (
    <DialogFrame className="channel-dialog" close={close} labelId="create-channel-title">
        <header>
          <div><p className="section-kicker">NEW CONVERSATION</p><h2 id="create-channel-title">Create Channel</h2></div>
          <button className="icon-button" type="button" aria-label="Close Create Channel" onClick={close}><X /></button>
        </header>
        <form className="channel-create-form" onSubmit={submit}>
          <label>Slug<input name="slug" required maxLength={32} pattern="[a-z0-9]+(?:-[a-z0-9]+)*" data-dialog-initial-focus /></label>
          <label>Visibility<select name="kind" defaultValue="public"><option value="public">Public</option><option value="private">Private</option></select></label>
          <label>Topic<input name="topic" maxLength={200} /></label>
          <fieldset className="channel-agent-picker">
            <legend>Initial Agents</legend>
            {agents.length ? agents.map((agent) => (
              <label key={agent.id}>
                <input type="checkbox" name="agent_member_ids" value={agent.id} />
                <PixelIdentity name={agent.display_name} kind="agent" seed={agent.id} />
                <span><strong>{agent.display_name}</strong><small>Agent</small></span>
              </label>
            )) : <p>No active Agents are available.</p>}
          </fieldset>
          {error ? <p className="form-error" role="alert">{error}</p> : null}
          <footer>
            <button className="command-button" type="button" onClick={close}>Cancel</button>
            <button className="command-button command-button--accent" type="submit" disabled={pending}>
              {pending ? <LoaderCircle className="spin" aria-hidden="true" /> : null}
              Create Channel
            </button>
          </footer>
        </form>
    </DialogFrame>
  );
}

function DirectMessageDialog({
  members,
  activityByMemberId,
  existingMemberIds,
  pendingMemberId,
  error,
  close,
  onSelect,
}: {
  members: Member[];
  activityByMemberId: Map<string, Agent["activity_status"]>;
  existingMemberIds: Set<string>;
  pendingMemberId?: string;
  error?: string;
  close: () => void;
  onSelect: (memberId: string) => void;
}) {
  const [query, setQuery] = useState("");
  const normalizedQuery = query.trim().toLocaleLowerCase();
  const filteredMembers = members.filter((member) =>
    !normalizedQuery ||
    member.display_name.toLocaleLowerCase().includes(normalizedQuery),
  );

  return (
    <DialogFrame className="dm-dialog" close={close} labelId="start-dm-title">
      <header>
        <div><p className="section-kicker">DIRECT MESSAGE</p><h2 id="start-dm-title">Start DM</h2></div>
        <button className="icon-button" type="button" aria-label="Close Start DM" onClick={close}><X /></button>
      </header>
      <div className="dm-picker">
        <label className="dm-search">Find a Member<input data-dialog-initial-focus value={query} onChange={(event) => setQuery(event.target.value)} placeholder="Name" /></label>
        <ul className="dm-member-list" aria-label="Members available for DM">
          {filteredMembers.length ? filteredMembers.map((member) => {
            const pending = pendingMemberId === member.id;
            const activityStatus = activityByMemberId.get(member.id);
            return (
              <li key={member.id}>
                <button type="button" disabled={Boolean(pendingMemberId)} onClick={() => onSelect(member.id)}>
                  <PresenceIdentity name={member.display_name} kind={member.kind} seed={member.id} activityStatus={activityStatus} />
                  <span><strong>{member.display_name}</strong><small>{member.kind === "agent" ? activityLabel(activityStatus) : "Human"}</small></span>
                  <b>{pending ? "Opening…" : existingMemberIds.has(member.id) ? "Open" : "Start"}</b>
                </button>
              </li>
            );
          }) : <li className="nav-empty">No matching Members</li>}
        </ul>
        {error ? <p className="form-error" role="alert">{error}</p> : null}
      </div>
    </DialogFrame>
  );
}

function NavigationItem({
  icon: Icon,
  label,
  active = false,
  href,
  unread = false,
}: {
  icon: LucideIcon;
  label: string;
  active?: boolean;
  href?: string;
  unread?: boolean;
}) {
  const content = (
    <>
      <Icon aria-hidden="true" />
      <span>{label}</span>
      {unread ? <span className="nav-unread-dot" aria-label="Unread messages" /> : null}
    </>
  );
  const className = `nav-item${active ? " nav-item--active" : ""}`;
  return href ? (
    <Link className={className} to={href} aria-current={active ? "page" : undefined}>
      {content}
    </Link>
  ) : (
    <span className={`${className} nav-item--disabled`}>{content}</span>
  );
}

function DirectMessageNavigationItem({ member, activityStatus, active, href, unread }: { member: Member; activityStatus?: Agent["activity_status"]; active: boolean; href: string; unread: boolean }) {
  return (
    <Link className={`nav-item dm-nav-item${active ? " nav-item--active" : ""}`} to={href} aria-current={active ? "page" : undefined}>
      <PresenceIdentity name={member.display_name} kind={member.kind} seed={member.id} activityStatus={activityStatus} />
      <span>
        <strong title={member.display_name}>{member.display_name}</strong>
        <small>{member.kind === "agent" ? activityLabel(activityStatus) : "Human"}</small>
      </span>
      {unread ? <span className="nav-unread-dot" aria-label="Unread messages" /> : null}
    </Link>
  );
}

export function PresenceIdentity({ name, kind = "human", seed, activityStatus }: { name: string; kind?: "human" | "agent"; seed: string; activityStatus?: Agent["activity_status"] }) {
  if (kind !== "agent" || !activityStatus) {
    return <PixelIdentity name={name} kind={kind} seed={seed} />;
  }
  const label = activityLabel(activityStatus);
  return (
    <span className="presence-identity">
      <PixelIdentity name={name} kind={kind} seed={seed} />
      <span className={`presence-dot presence-dot--${activityStatus}`} role="img" aria-label={`${name} is ${label}`} title={`${name} · ${label}`} />
    </span>
  );
}
