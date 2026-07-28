import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Link, useLocation, useNavigate } from "@tanstack/react-router";
import {
  ChevronDown,
  Hash,
  Inbox,
  LockKeyhole,
  ListTodo,
  MessageCircle,
  Monitor,
  Plus,
  Users,
  X,
  type LucideIcon,
} from "lucide-react";
import type { CSSProperties, FormEvent, ReactNode } from "react";
import { useEffect, useRef, useState } from "react";

import {
  ApiRequestError,
  createChannel,
  currentUser,
  getSpaceBySlug,
  joinChannel,
  listChannels,
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
  type User,
} from "../api/client";
import { activityLabel } from "../agentActivity";
import { useSpaceEvents } from "../hooks/useSpaceEvents";
import { DialogFrame } from "./DialogFrame";

export interface SpaceShellContext {
  space: Space;
  user: User;
  navigationOpen: boolean;
  channels: Channel[];
  directMessages: DirectMessage[];
  members: Member[];
  currentMember: Member;
  openNavigation: () => void;
}

export function SpaceShell({
  spaceSlug,
  active,
  children,
}: {
  spaceSlug: string;
  active: "channel" | "dm" | "members" | "inbox" | "tasks" | "computers";
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
  useSpaceEvents(space.data?.id);
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

  return (
    <main
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
          <span className="space-brand-mark" aria-hidden="true">S</span>
        </Link>
        <nav className="rail-tools" aria-label="Space management">
          <RailItem
            icon={MessageCircle}
            label="Conversation"
            active={active === "channel" || active === "dm"}
            href={`/s/${space.data.slug}/channels/general`}
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
            active={active === "members"}
            href={`/s/${space.data.slug}/members`}
          />
          <RailItem
            icon={Monitor}
            label="Computers"
            active={active === "computers"}
            href={`/s/${space.data.slug}/computers`}
          />
        </nav>
        <button
          ref={railNavigationTrigger}
          className="rail-spacer"
          type="button"
          aria-label="Open navigation"
          title="Open navigation"
          onClick={openNavigation}
        />
        <PixelIdentity name={user.data.display_name} kind="human" seed={currentMember.id} />
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
          <div>
            <span className="space-name-eyebrow" title={space.data.name}>{space.data.name}</span>
            <h2>{active === "channel" || active === "dm" ? "Conversations" : capitalize(active)}</h2>
          </div>
          <ChevronDown className="desktop-only" aria-hidden="true" />
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
              icon={Users}
              label="Members"
              active={active === "members"}
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
            {active !== "channel" && active !== "dm" ? (
              <NavigationItem
                icon={Inbox}
                label="Inbox"
                active={active === "inbox"}
                href={`/s/${space.data.slug}/inbox`}
              />
            ) : null}
          </div>
          {active === "members" ? (
            <MembersNavigation members={members.data} activityByMemberId={activityByMemberId} spaceSlug={space.data.slug} locationPath={location.pathname} />
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
            <TasksNavigation />
          ) : (
            <>
          <NavigationItem
            icon={Inbox}
            label="Inbox"
            href={`/s/${space.data.slug}/inbox`}
          />
          <div className="nav-section-heading">
            <p className="nav-label"><ChevronDown aria-hidden="true" /> CHANNELS <span>{channels.data.channels.filter((channel) => channel.joined).length}</span></p>
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
                    <span title={channel.name}>{channel.slug}</span>
                    <button
                      type="button"
                      disabled={channelJoin.isPending}
                      aria-label={`Join ${channel.name}`}
                      title={`Join ${channel.name}`}
                      onClick={() => channelJoin.mutate(channel.id)}
                    >
                      JOIN
                    </button>
                  </div>
                ))}
            </>
          ) : null}
          <p className="nav-label nav-label--section"><ChevronDown aria-hidden="true" /> DMS <span>{availableDirectMessages.length}</span></p>
          {directMessages.error ? (
            <span className="nav-empty">DMs unavailable</span>
          ) : availableDirectMessages.length === 0 ? (
            <span className="nav-empty">Start from Members</span>
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

      {children({
        space: space.data,
        user: user.data,
        channels: channels.data.channels,
        directMessages: availableDirectMessages,
        members: members.data,
        currentMember,
        navigationOpen,
        openNavigation,
      })}
    </main>
  );
}

function RouteFailure({ error, retry }: { error: unknown; retry: () => void }) {
  const message = error instanceof ApiRequestError ? error.message : "The Server did not return a usable response.";
  return <main className="route-status route-status--error"><section className="route-status-panel" role="alert"><p className="section-kicker">COULD NOT OPEN SPACE</p><h1>Something interrupted this view.</h1><p>{message}</p><button className="command-button command-button--accent" type="button" onClick={retry}>Retry</button></section></main>;
}

function MembersNavigation({ members, activityByMemberId, spaceSlug, locationPath }: { members: Member[]; activityByMemberId: Map<string, Agent["activity_status"]>; spaceSlug: string; locationPath: string }) {
  const agents = members.filter((member) => member.kind === "agent");
  const humans = members.filter((member) => member.kind === "human");
  return (
    <div className="members-navigation">
      <MemberNavigationGroup
        label="Agents"
        members={agents}
        activityByMemberId={activityByMemberId}
        spaceSlug={spaceSlug}
        locationPath={locationPath}
      />
      <MemberNavigationGroup
        label="Humans"
        members={humans}
        activityByMemberId={activityByMemberId}
        spaceSlug={spaceSlug}
        locationPath={locationPath}
      />
    </div>
  );
}

function MemberNavigationGroup({ label, members, activityByMemberId, spaceSlug, locationPath }: { label: string; members: Member[]; activityByMemberId: Map<string, Agent["activity_status"]>; spaceSlug: string; locationPath: string }) {
  return (
    <section className="member-navigation-group" aria-labelledby={`member-navigation-${label.toLowerCase()}`}>
      <h3 className="nav-label" id={`member-navigation-${label.toLowerCase()}`}>
        {label}<span>{members.length}</span>
      </h3>
      {members.length ? members.map((member) => member.kind === "agent" ? (
        <Link key={member.id} className={`context-entity-row${locationPath.endsWith(`/agents/${member.id}`) ? " context-entity-row--active" : ""}`} to="/s/$spaceSlug/agents/$agentId" params={{ spaceSlug, agentId: member.id }}>
          <PresenceIdentity name={member.display_name} kind="agent" seed={member.id} activityStatus={activityByMemberId.get(member.id)} />
          <span><strong title={member.display_name}>{member.display_name}</strong><small title={member.display_name}>@{member.handle} · {activityLabel(activityByMemberId.get(member.id))}</small></span>
        </Link>
      ) : (
        <div className="context-entity-row context-entity-row--static" key={member.id}>
          <PixelIdentity name={member.display_name} kind="human" seed={member.id} />
          <span><strong title={member.display_name}>{member.display_name}</strong><small>@{member.handle}</small></span>
        </div>
      )) : <p className="nav-empty">No {label.toLowerCase()}</p>}
    </section>
  );
}

function ComputersNavigation({ computers, spaceSlug, activeHash, canManage }: { computers: Computer[]; spaceSlug: string; activeHash: string; canManage: boolean }) {
  const normalizedHash = activeHash.replace(/^#/, "");
  return <><div className="nav-section-heading computer-nav-heading"><p className="nav-label">COMPUTERS · {computers.length}</p>{canManage ? <Link to="/s/$spaceSlug/computers" params={{ spaceSlug }} hash="pair-computer" aria-label="Pair Computer" title="Pair Computer"><Plus /></Link> : null}</div>{computers.length ? computers.map((computer, index) => { const hash = `computer-${computer.id}`; const selected = normalizedHash === hash || (!normalizedHash && index === 0); return <Link key={computer.id} className={`context-entity-row computer-context-row${selected ? " context-entity-row--active" : ""}`} to="/s/$spaceSlug/computers" params={{ spaceSlug }} hash={hash}><Monitor aria-hidden="true" /><span><strong title={computer.name}>{computer.name}</strong><small><i className={`computer-presence computer-presence--${computer.status}`} />{computer.status} · v{computer.daemon_version}</small></span></Link>; }) : <p className="nav-empty">No paired Computers</p>}</>;
}

function InboxNavigation() {
  return <div className="context-groups" aria-label="Inbox groups"><span><strong>01</strong> Approvals</span><span><strong>02</strong> DM &amp; mentions</span><span><strong>03</strong> Replies</span><span><strong>04</strong> Channel activity</span></div>;
}

function TasksNavigation() {
  return <div className="context-groups" aria-label="Task status groups"><span><strong>01</strong> Open</span><span><strong>02</strong> In progress</span><span><strong>03</strong> Done</span><span><strong>04</strong> Canceled</span></div>;
}

function capitalize(value: string): string { return value.charAt(0).toUpperCase() + value.slice(1); }

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
      name: String(form.get("name") ?? ""),
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
          <label>Name<input name="name" required maxLength={80} data-dialog-initial-focus /></label>
          <label>Slug<input name="slug" required maxLength={32} pattern="[a-z0-9]+(?:-[a-z0-9]+)*" /></label>
          <label>Visibility<select name="kind" defaultValue="public"><option value="public">Public</option><option value="private">Private</option></select></label>
          <label>Topic<input name="topic" maxLength={200} /></label>
          <fieldset className="channel-agent-picker">
            <legend>Initial Agents</legend>
            {agents.length ? agents.map((agent) => (
              <label key={agent.id}>
                <input type="checkbox" name="agent_member_ids" value={agent.id} />
                <PixelIdentity name={agent.display_name} kind="agent" seed={agent.id} />
                <span><strong>{agent.display_name}</strong><small>@{agent.handle}</small></span>
              </label>
            )) : <p>No active Agents are available.</p>}
          </fieldset>
          {error ? <p className="form-error" role="alert">{error}</p> : null}
          <footer>
            <button className="command-button" type="button" onClick={close}>Cancel</button>
            <button className="command-button command-button--accent" type="submit" disabled={pending}>{pending ? "Creating…" : "Create Channel"}</button>
          </footer>
        </form>
    </DialogFrame>
  );
}

function NavigationItem({
  icon: Icon,
  label,
  active = false,
  href,
}: {
  icon: LucideIcon;
  label: string;
  active?: boolean;
  href?: string;
}) {
  const content = (
    <>
      <Icon aria-hidden="true" />
      <span>{label}</span>
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

function DirectMessageNavigationItem({ member, activityStatus, active, href }: { member: Member; activityStatus?: Agent["activity_status"]; active: boolean; href: string }) {
  return (
    <Link className={`nav-item dm-nav-item${active ? " nav-item--active" : ""}`} to={href} aria-current={active ? "page" : undefined}>
      <PresenceIdentity name={member.display_name} kind={member.kind} seed={member.id} activityStatus={activityStatus} />
      <span>
        <strong title={member.display_name}>{member.display_name}</strong>
        <small>@{member.handle}{member.kind === "agent" ? ` · ${activityLabel(activityStatus)}` : ""}</small>
      </span>
    </Link>
  );
}

export function PresenceIdentity({ name, kind = "human", seed, activityStatus }: { name: string; kind?: "human" | "agent"; seed?: string; activityStatus?: Agent["activity_status"] }) {
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

export function PixelIdentity({ name, kind = "human", seed }: { name: string; kind?: "human" | "agent"; seed?: string }) {
  const variant = pixelVariant(seed ?? name);
  const palettes = [
    { background: "#C9E7E7", foreground: "#173F46", accent: "#FE7DA8" },
    { background: "#DFE3FF", foreground: "#2E377A", accent: "#D95C55" },
    { background: "#F5E2A8", foreground: "#5A4312", accent: "#FE7DA8" },
    { background: "#F2D3BD", foreground: "#63392D", accent: "#315B55" },
  ] as const;
  const palette = palettes[variant % palettes.length];
  const marks = [
    "M3 0h2v1h2v2h1v2H7v2H5v1H3V7H1V5H0V3h1V1h2zm0 2H2v1H1v2h1v1h1v1h2V6h1V5h1V3H6V2H5V1H3z",
    "M0 1h3v2H2v2h1v2H0zm5 0h3v6H5V5h1V3H5z",
    "M3 0h2v2h2v1H5v2h2v2H5v1H3V6H1V5h2V3H1V1h2z",
    "M1 0h2v2h2V0h2v3H6v2h1v3H5V6H3v2H1V5h1V3H1z",
  ] as const;
  const initial = [...name.trim()][0]?.toLocaleUpperCase() ?? "?";
  return (
    <span
      className={`pixel-identity pixel-identity--${kind}`}
      role="img"
      aria-label={`${name} avatar`}
      title={name}
      style={{ background: palette.background, color: palette.foreground }}
    >
      {kind === "human" ? <span aria-hidden="true">{initial}</span> : (
        <svg viewBox="0 0 8 8" aria-hidden="true" shapeRendering="crispEdges">
          <rect width="8" height="8" fill={palette.background} />
          <path d={marks[variant % marks.length]} fill={palette.foreground} />
          <rect x="3" y="3" width="2" height="2" fill={palette.accent} />
        </svg>
      )}
      <span className="visually-hidden">{initials(name)}</span>
    </span>
  );
}

function pixelVariant(name: string): number {
  return [...name].reduce((hash, character) => ((hash * 31) + character.codePointAt(0)!) >>> 0, 7);
}

function initials(name: string): string {
  return name
    .split(/\s+/)
    .slice(0, 2)
    .map((part) => part[0])
    .join("")
    .toUpperCase();
}
