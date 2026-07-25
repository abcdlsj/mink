import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useNavigate } from "@tanstack/react-router";
import {
  Asterisk,
  ChevronDown,
  Hash,
  Inbox,
  LockKeyhole,
  MessageCircle,
  Monitor,
  Plus,
  Settings,
  Users,
  X,
  type LucideIcon,
} from "lucide-react";
import type { CSSProperties, FormEvent, ReactNode } from "react";
import { useEffect, useState } from "react";

import {
  ApiRequestError,
  createChannel,
  currentUser,
  getSpaceBySlug,
  joinChannel,
  listChannels,
  listDirectMessages,
  listMembers,
  type Channel,
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
  const queryClient = useQueryClient();
  const [navigationOpen, setNavigationOpen] = useState(false);
  const [channelFormOpen, setChannelFormOpen] = useState(false);
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
    space.error ?? user.error ?? channels.error ?? directMessages.error ?? members.error;

  useEffect(() => {
    if (routeError instanceof ApiRequestError && routeError.status === 401) {
      void navigate({
        to: "/login",
        search: { redirect: window.location.pathname },
        replace: true,
      });
    }
  }, [navigate, routeError]);

  if (
    space.isPending ||
    user.isPending ||
    channels.isPending ||
    directMessages.isPending ||
    members.isPending
  ) {
    return <div className="route-status">Opening Space...</div>;
  }
  if (routeError) {
    return <div className="route-status route-status--error">Space unavailable.</div>;
  }
  if (!space.data || !user.data || !channels.data || !directMessages.data || !members.data) {
    return <div className="route-status">Opening Space...</div>;
  }
  const currentMember = members.data.find((member) => member.id === space.data.current_member_id);
  if (!currentMember) {
    return <div className="route-status route-status--error">Member identity unavailable.</div>;
  }

  return (
    <main
      className="space-shell"
      style={{ "--space-accent": space.data.accent } as CSSProperties}
    >
      <aside className="space-rail" aria-label="Space switcher">
        <a className="space-badge" href={`/s/${space.data.slug}`} aria-label={space.data.name}>
          <Asterisk strokeWidth={3} />
        </a>
        <div className="rail-spacer" />
        <PixelIdentity name={user.data.display_name} />
      </aside>

      {navigationOpen ? (
        <button
          className="navigation-scrim"
          type="button"
          aria-label="Close navigation"
          onClick={() => setNavigationOpen(false)}
        />
      ) : null}
      <aside
        className={`space-navigation${navigationOpen ? " space-navigation--open" : ""}`}
        aria-label="Space navigation"
      >
        <header className="space-name-row">
          <strong title={space.data.name}>{space.data.name}</strong>
          <ChevronDown className="desktop-only" aria-hidden="true" />
          <button
            className="navigation-close icon-button"
            type="button"
            aria-label="Close navigation"
            title="Close navigation"
            onClick={() => setNavigationOpen(false)}
          >
            <X />
          </button>
        </header>
        <nav>
          <NavigationItem
            icon={Inbox}
            label="Inbox"
            active={active === "inbox"}
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
                  setChannelFormOpen((open) => !open);
                  channelCreation.reset();
                }}
              >
                {channelFormOpen ? <X /> : <Plus />}
              </button>
            ) : null}
          </div>
          {channelFormOpen ? (
            <ChannelForm
              pending={channelCreation.isPending}
              error={channelCreation.error?.message}
              onSubmit={(input) => channelCreation.mutate(input)}
            />
          ) : null}
          {channels.data.channels
            .filter((channel) => channel.joined)
            .map((channel) => (
              <NavigationItem
                key={channel.id}
                icon={channel.kind === "private" ? LockKeyhole : Hash}
                label={channel.slug}
                active={
                  active === "channel" &&
                  window.location.pathname.endsWith(`/channels/${channel.slug}`)
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
          {directMessages.data.length === 0 ? (
            <span className="nav-empty">Start from Members</span>
          ) : null}
          {directMessages.data.map((dm) => (
            <NavigationItem
              key={dm.channel_id}
              icon={MessageCircle}
              label={dm.other_member.display_name}
              active={
                active === "dm" &&
                window.location.pathname.endsWith(`/dm/${dm.other_member.id}`)
              }
              href={`/s/${space.data.slug}/dm/${dm.other_member.id}`}
            />
          ))}
          <p className="nav-label">SPACE</p>
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
          <NavigationItem icon={Settings} label="Settings" />
        </nav>
      </aside>

      {children({
        space: space.data,
        user: user.data,
        channels: channels.data.channels,
        directMessages: directMessages.data,
        currentMember,
        navigationOpen,
        openNavigation: () => setNavigationOpen(true),
      })}
    </main>
  );
}

function ChannelForm({
  pending,
  error,
  onSubmit,
}: {
  pending: boolean;
  error?: string;
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
    });
  }

  return (
    <form className="channel-create-form" onSubmit={submit}>
      <label>
        Name
        <input name="name" required maxLength={80} />
      </label>
      <label>
        Slug
        <input
          name="slug"
          required
          maxLength={32}
          pattern="[a-z0-9]+(?:-[a-z0-9]+)*"
        />
      </label>
      <label>
        Visibility
        <select name="kind" defaultValue="public">
          <option value="public">Public</option>
          <option value="private">Private</option>
        </select>
      </label>
      <label>
        Topic
        <input name="topic" maxLength={200} />
      </label>
      {error ? <p role="alert">{error}</p> : null}
      <button type="submit" disabled={pending}>
        {pending ? "CREATING" : "CREATE CHANNEL"}
      </button>
    </form>
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
    <a className={className} href={href} aria-current={active ? "page" : undefined}>
      {content}
    </a>
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
