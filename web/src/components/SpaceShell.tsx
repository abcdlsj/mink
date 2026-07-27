import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Link, useLocation, useNavigate } from "@tanstack/react-router";
import {
  Asterisk,
  ChevronDown,
  Hash,
  Inbox,
  LockKeyhole,
  MessageCircle,
  Monitor,
  Plus,
  Users,
  X,
  type LucideIcon,
} from "lucide-react";
import type { FormEvent, ReactNode } from "react";
import { useEffect, useRef, useState } from "react";

import {
  ApiRequestError,
  createChannel,
  currentUser,
  getSpaceBySlug,
  joinChannel,
  listChannels,
  listComputers,
  listDirectMessages,
  listMembers,
  type Channel,
  type Computer,
  type DirectMessage,
  type Member,
  type Space,
  type User,
} from "../api/client";
import { useSpaceEvents } from "../hooks/useSpaceEvents";

export interface SpaceShellContext {
  space: Space;
  user: User;
  navigationOpen: boolean;
  channels: Channel[];
  directMessages: DirectMessage[];
  currentMember: Member;
  openNavigation: () => void;
}

export function SpaceShell({
  spaceSlug,
  active,
  children,
}: {
  spaceSlug: string;
  active: "channel" | "dm" | "members" | "inbox" | "computers";
  children: (context: SpaceShellContext) => ReactNode;
}) {
  const navigate = useNavigate();
  const location = useLocation();
  const authenticationRedirect = useRef(location.href);
  const queryClient = useQueryClient();
  const [navigationOpen, setNavigationOpen] = useState(false);
  const [navigationTrigger, setNavigationTrigger] = useState<HTMLElement | null>(null);
  const [channelFormOpen, setChannelFormOpen] = useState(false);
  const navigationPanel = useRef<HTMLElement>(null);
  function closeNavigation() {
    setNavigationOpen(false);
    window.requestAnimationFrame(() => navigationTrigger?.focus());
  }
  function openNavigation() {
    setNavigationTrigger(document.activeElement as HTMLElement);
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
  const routeError = space.error ?? user.error ?? channels.error ?? members.error ?? computers.error;

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
      if (event.key === "Escape") {
        setNavigationOpen(false);
        window.requestAnimationFrame(() => navigationTrigger?.focus());
        return;
      }
      if (event.key !== "Tab" || !panel) return;
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

  return (
    <main className="space-shell">
      <aside className="space-rail" aria-label="Space tools">
        <Link
          className="space-badge"
          to="/s/$spaceSlug"
          params={{ spaceSlug: space.data.slug }}
          aria-label={space.data.name}
        >
          <Asterisk strokeWidth={3} />
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
        <div className="rail-spacer" />
        <PixelIdentity name={user.data.display_name} />
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
          if ((event.target as HTMLElement).closest("a")) closeNavigation();
        }}
      >
        <header className="space-name-row">
          <h2 title={space.data.name}>{active === "channel" || active === "dm" ? space.data.name : capitalize(active)}</h2>
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
            <MembersNavigation members={members.data} spaceSlug={space.data.slug} locationPath={location.pathname} />
          ) : active === "computers" ? (
            <ComputersNavigation computers={computers.data ?? []} spaceSlug={space.data.slug} activeHash={location.hash} />
          ) : active === "inbox" ? (
            <InboxNavigation />
          ) : (
            <>
          <NavigationItem
            icon={Inbox}
            label="Inbox"
            href={`/s/${space.data.slug}/inbox`}
          />
          <div className="nav-section-heading">
            <p className="nav-label">CHANNELS</p>
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
          <p className="nav-label">DMS</p>
          {directMessages.error ? (
            <span className="nav-empty">DMs unavailable</span>
          ) : availableDirectMessages.length === 0 ? (
            <span className="nav-empty">Start from Members</span>
          ) : null}
          {availableDirectMessages.map((dm) => (
            <NavigationItem
              key={dm.channel_id}
              icon={MessageCircle}
              label={dm.other_member.display_name}
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

function MembersNavigation({ members, spaceSlug, locationPath }: { members: Member[]; spaceSlug: string; locationPath: string }) {
  return <><p className="nav-label">MEMBERS · {members.length}</p>{members.map((member) => member.kind === "agent" ? <Link key={member.id} className={`context-entity-row${locationPath.endsWith(`/agents/${member.id}`) ? " context-entity-row--active" : ""}`} to="/s/$spaceSlug/agents/$agentId" params={{ spaceSlug, agentId: member.id }}><PixelIdentity name={member.display_name} /><span><strong title={member.display_name}>{member.display_name}</strong><small>@{member.handle} · Agent</small></span></Link> : <div className="context-entity-row context-entity-row--static" key={member.id}><PixelIdentity name={member.display_name} /><span><strong title={member.display_name}>{member.display_name}</strong><small>@{member.handle} · Human</small></span></div>)}</>;
}

function ComputersNavigation({ computers, spaceSlug, activeHash }: { computers: Computer[]; spaceSlug: string; activeHash: string }) {
  const normalizedHash = activeHash.replace(/^#/, "");
  return <><div className="nav-section-heading computer-nav-heading"><p className="nav-label">COMPUTERS · {computers.length}</p><Link to="/s/$spaceSlug/computers" params={{ spaceSlug }} hash="pair-computer" aria-label="Pair Computer" title="Pair Computer"><Plus /></Link></div>{computers.length ? computers.map((computer, index) => { const hash = `computer-${computer.id}`; const selected = normalizedHash === hash || (!normalizedHash && index === 0); return <Link key={computer.id} className={`context-entity-row${selected ? " context-entity-row--active" : ""}`} to="/s/$spaceSlug/computers" params={{ spaceSlug }} hash={hash}><Monitor aria-hidden="true" /><span><strong title={computer.name}>{computer.name}</strong><small>{computer.status} · {computer.hostname}</small></span></Link>; }) : <p className="nav-empty">No paired Computers</p>}</>;
}

function InboxNavigation() {
  return <div className="context-groups" aria-label="Inbox groups"><span><strong>01</strong> Approvals</span><span><strong>02</strong> DM &amp; mentions</span><span><strong>03</strong> Replies</span><span><strong>04</strong> Channel activity</span></div>;
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
    <div className="dialog-backdrop" role="presentation">
      <section className="channel-dialog" role="dialog" aria-modal="true" aria-labelledby="create-channel-title">
        <header>
          <div><p className="section-kicker">NEW CONVERSATION</p><h2 id="create-channel-title">Create Channel</h2></div>
          <button className="icon-button" type="button" aria-label="Close Create Channel" onClick={close}><X /></button>
        </header>
        <form className="channel-create-form" onSubmit={submit}>
          <label>Name<input name="name" required maxLength={80} autoFocus /></label>
          <label>Slug<input name="slug" required maxLength={32} pattern="[a-z0-9]+(?:-[a-z0-9]+)*" /></label>
          <label>Visibility<select name="kind" defaultValue="public"><option value="public">Public</option><option value="private">Private</option></select></label>
          <label>Topic<input name="topic" maxLength={200} /></label>
          <fieldset className="channel-agent-picker">
            <legend>Initial Agents</legend>
            {agents.length ? agents.map((agent) => (
              <label key={agent.id}>
                <input type="checkbox" name="agent_member_ids" value={agent.id} />
                <PixelIdentity name={agent.display_name} />
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
      </section>
    </div>
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

export function PixelIdentity({ name }: { name: string }) {
  return <span className="pixel-identity">{initials(name)}</span>;
}

function initials(name: string): string {
  return name
    .split(/\s+/)
    .slice(0, 2)
    .map((part) => part[0])
    .join("")
    .toUpperCase();
}
