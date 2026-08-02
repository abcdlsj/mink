import { useQuery } from "@tanstack/react-query";
import { useNavigate, useParams } from "@tanstack/react-router";
import { Inbox, Menu, type LucideIcon } from "lucide-react";
import type { ReactNode } from "react";

import { listInbox, markInboxItemRead, type InboxItem, type Member } from "../api/client";
import { PixelIdentity, SpaceShell } from "../components/SpaceShell";

const inboxGroups: Array<{
  id: "direct" | "reply" | "activity";
  label: string;
  description: string;
  kinds: InboxItem["kind"][];
}> = [
  {
    id: "direct",
    label: "DM & mentions",
    description: "Direct requests and explicit mentions",
    kinds: ["direct", "mention"],
  },
  {
    id: "reply",
    label: "Replies",
    description: "Thread and Message responses to revisit",
    kinds: ["reply", "thread_activity"],
  },
  {
    id: "activity",
    label: "Channel activity",
    description: "Ambient collaboration worth reviewing",
    kinds: ["channel_activity", "system"],
  },
];

export function InboxPage() {
  const { spaceSlug } = useParams({ from: "/s/$spaceSlug/inbox" });
  return (
    <SpaceShell spaceSlug={spaceSlug} active="inbox">
      {({ space, members, currentMember, openNavigation }) => (
        <InboxWorkspace
          spaceSlug={space.slug}
          spaceId={space.id}
          memberId={currentMember.id}
          members={members}
          openNavigation={openNavigation}
        />
      )}
    </SpaceShell>
  );
}

function InboxWorkspace({
  spaceSlug,
  spaceId,
  memberId,
  members,
  openNavigation,
}: {
  spaceSlug: string;
  spaceId: string;
  memberId: string;
  members: Member[];
  openNavigation: () => void;
}) {
  const navigate = useNavigate();
  const inbox = useQuery({
    queryKey: ["inbox", spaceId, memberId],
    queryFn: () => listInbox(memberId),
  });
  // Only Items a group renders count as attention, so the empty state matches the visible list.
  const groupedKinds = new Set<InboxItem["kind"]>(inboxGroups.flatMap((group) => group.kinds));
  const attentionItems = inbox.data?.filter((item) => groupedKinds.has(item.kind)) ?? [];
  const groupedItems = inboxGroups.map((group) => ({
    ...group,
    items: attentionItems.filter((item) => group.kinds.includes(item.kind)),
  }));

  // Opening the source is the Human's read: the Item leaves the queue, so a reply is seen once.
  function open(item: InboxItem) {
    void markInboxItemRead(item.id).catch(() => {
      // Reading is best-effort; navigation still opens the source even if the projection stalls.
    });
    if (item.kind === "direct" && item.sender_member_id) {
      void navigate({
        to: "/s/$spaceSlug/dm/$memberId",
        params: { spaceSlug, memberId: item.sender_member_id },
      });
      return;
    }
    if (item.channel_slug) {
      void navigate({
        to: "/s/$spaceSlug/channels/$channelSlug",
        params: { spaceSlug, channelSlug: item.channel_slug },
      });
    }
  }

  return (
    <section className="inbox-workspace">
      <header className="channel-header">
        <button className="mobile-menu icon-button" type="button" aria-label="Open navigation" onClick={openNavigation}>
          <Menu />
        </button>
        <div className="channel-title">
          <h1>Inbox</h1>
          <p>Attention routed to you. Open the source to respond.</p>
        </div>
      </header>
      <div className="inbox-list">
        <div className="inbox-list-inner">
          {inbox.isPending ? <p className="timeline-status">Loading Inbox...</p> : null}
          {inbox.error ? <p className="timeline-status timeline-status--error">{inbox.error.message}</p> : null}
          {!inbox.isPending && !inbox.error && attentionItems.length === 0 ? (
          <section className="inbox-empty" aria-labelledby="inbox-empty-title">
            <div className="inbox-empty-intro">
              <span className="inbox-empty-icon" aria-hidden="true"><Inbox /></span>
              <div>
                <p className="section-kicker">ATTENTION QUEUE</p>
                <h2 id="inbox-empty-title">Nothing needs your attention</h2>
                <p>Inbox lists collaboration routed to you. It is not your Message history.</p>
              </div>
            </div>
            <ul className="inbox-empty-groups" aria-label="Empty Inbox groups">
              <li><span><strong>DM &amp; mentions</strong><small>Direct requests and explicit mentions</small></span><b>0</b></li>
              <li><span><strong>Replies</strong><small>Thread and Message responses to revisit</small></span><b>0</b></li>
              <li><span><strong>Channel activity</strong><small>Ambient collaboration worth reviewing</small></span><b>0</b></li>
            </ul>
          </section>
          ) : null}
          {groupedItems.map((group) => group.items.length > 0 ? (
          <InboxGroup
            key={group.id}
            id={group.id}
            label={group.label}
            description={group.description}
            count={group.items.length}
          >
            {group.items.map((item) => {
              const sender = members.find((member) => member.id === item.sender_member_id);
              const senderName = item.sender_display_name ?? "Sumi";
              const address = item.channel_slug
                ? `#${item.channel_slug}${item.thread_id ? `:${item.thread_id}` : ""}`
                : "SYSTEM";
              return (
                <article className={`inbox-item inbox-item--${item.priority}`} key={item.id}>
                  <button className="inbox-open" type="button" onClick={() => open(item)} aria-label={`Open ${address} from ${senderName}`}>
                    <PixelIdentity name={senderName} kind={sender?.kind ?? "human"} seed={item.sender_member_id ?? item.id} />
                    <span className="inbox-item-copy">
                      <span className="inbox-item-meta">
                        <strong>{senderName}</strong>
                        <span className="inbox-address">{address}</span>
                        <time dateTime={item.created_at}>{formatInboxTime(item.created_at)}</time>
                      </span>
                      <span className="inbox-summary">{item.summary ?? "Attention required"}</span>
                      <span className="inbox-kind">{formatInboxKind(item.kind)}</span>
                    </span>
                  </button>
                </article>
              );
            })}
          </InboxGroup>
          ) : null)}
        </div>
      </div>
    </section>
  );
}

function InboxGroup({ id, label, description, count, icon: Icon = Inbox, children }: {
  id: string;
  label: string;
  description: string;
  count: number;
  icon?: LucideIcon;
  children: ReactNode;
}) {
  return (
    <section className="inbox-group" aria-labelledby={`inbox-group-${id}`}>
      <header className="inbox-group-heading">
        <Icon aria-hidden="true" />
        <span>
          <h2 id={`inbox-group-${id}`}>{label}</h2>
          <p>{description}</p>
        </span>
        <b aria-label={`${count} items`}>{String(count).padStart(2, "0")}</b>
      </header>
      <div className="inbox-group-items">{children}</div>
    </section>
  );
}

function formatInboxKind(kind: InboxItem["kind"]): string {
  if (kind === "thread_activity") return "thread activity";
  if (kind === "channel_activity") return "channel activity";
  return kind;
}

function formatInboxTime(value: string): string {
  return new Intl.DateTimeFormat(undefined, {
    month: "short",
    day: "numeric",
    hour: "2-digit",
    minute: "2-digit",
  }).format(new Date(value));
}
