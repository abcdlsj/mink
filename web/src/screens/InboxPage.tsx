import { useQuery, useQueryClient } from "@tanstack/react-query";
import { useNavigate, useParams } from "@tanstack/react-router";
import { Inbox, type LucideIcon } from "lucide-react";
import type { ReactNode } from "react";

import { listInbox, markInboxItemRead, type InboxItem, type Member } from "../api/client";
import { PixelIdentity, SpaceShell } from "../components/SpaceShell";

const inboxGroups: Array<{
  id: "dm" | "thread";
  label: string;
  description: string;
  kind: "dm" | "thread";
}> = [
  {
    id: "dm",
    label: "Direct messages",
    description: "One row per direct conversation",
    kind: "dm",
  },
  {
    id: "thread",
    label: "Threads",
    description: "One row per Thread with new attention",
    kind: "thread",
  },
];

type InboxStack = {
  id: string;
  kind: "dm" | "thread";
  items: InboxItem[];
  latest: InboxItem;
};

const humanInboxKinds = new Set<InboxItem["kind"]>([
  "direct",
  "mention",
  "reply",
  "task_activity",
  "thread_activity",
]);

function groupInboxItems(items: InboxItem[]): InboxStack[] {
  const stacks = new Map<string, InboxStack>();
  for (const item of items) {
    if (!humanInboxKinds.has(item.kind)) continue;
    const kind = item.kind === "direct" ? "dm" : "thread";
    const targetId = kind === "dm" ? item.channel_id : item.thread_id;
    const id = `${item.space_id}:${kind}:${targetId ?? item.id}`;
    const stack = stacks.get(id);
    if (stack) {
      stack.items.push(item);
    } else {
      stacks.set(id, { id, kind, items: [item], latest: item });
    }
  }
  return [...stacks.values()]
    .map((stack) => {
      const sorted = [...stack.items].sort((left, right) =>
        Date.parse(right.created_at) - Date.parse(left.created_at),
      );
      return { ...stack, items: sorted, latest: sorted[0] };
    })
    .sort((left, right) => Date.parse(right.latest.created_at) - Date.parse(left.latest.created_at));
}

export function InboxPage() {
  const { spaceSlug } = useParams({ from: "/s/$spaceSlug/inbox" });
  return (
    <SpaceShell spaceSlug={spaceSlug} active="inbox">
      {({ space, members, currentMember }) => (
        <InboxWorkspace
          spaceSlug={space.slug}
          spaceId={space.id}
          memberId={currentMember.id}
          members={members}
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
}: {
  spaceSlug: string;
  spaceId: string;
  memberId: string;
  members: Member[];
}) {
  const navigate = useNavigate();
  const queryClient = useQueryClient();
  const inbox = useQuery({
    queryKey: ["inbox", spaceId, memberId],
    queryFn: () => listInbox(memberId),
  });
  // Only Items a group renders count as attention, so the empty state matches the visible list.
  const attentionItems = inbox.data ?? [];
  const stacks = groupInboxItems(attentionItems);
  const groupedItems = inboxGroups.map((group) => ({
    ...group,
    stacks: stacks.filter((stack) => stack.kind === group.kind),
  }));

  // Opening the source is the Human's read: every Item in the stack leaves the queue.
  function open(stack: InboxStack) {
    void Promise.allSettled(stack.items.map((item) => markInboxItemRead(item.id))).then((results) => {
      const failed = results.filter((result) => result.status === "rejected");
      if (failed.length > 0) {
        console.error("Inbox read failed", { failed: failed.length, total: stack.items.length });
      }
      void queryClient.invalidateQueries({ queryKey: ["inbox", spaceId, memberId] });
    });
    const item = stack.latest;
    if (stack.kind === "dm" && item.sender_member_id) {
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
        hash: item.thread_id ? `message-${item.thread_id}` : undefined,
      });
    }
  }

  return (
    <section className="inbox-workspace">
      <header className="channel-header">
        <div className="channel-title">
          <h1>Inbox</h1>
          <p>Attention routed to you. Open the source to respond.</p>
        </div>
      </header>
      <div className="inbox-list">
        <div className="inbox-list-inner">
          {inbox.isPending ? <p className="timeline-status">Loading Inbox...</p> : null}
          {inbox.error ? <p className="timeline-status timeline-status--error">{inbox.error.message}</p> : null}
          {!inbox.isPending && !inbox.error && stacks.length === 0 ? (
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
              <li><span><strong>Direct messages</strong><small>One row per direct conversation</small></span><b>0</b></li>
              <li><span><strong>Threads</strong><small>One row per Thread with new attention</small></span><b>0</b></li>
            </ul>
          </section>
          ) : null}
          {groupedItems.map((group) => group.stacks.length > 0 ? (
          <InboxGroup
            key={group.id}
            id={group.id}
            label={group.label}
            description={group.description}
            count={group.stacks.reduce((total, stack) => total + stack.items.length, 0)}
          >
            {group.stacks.map((stack) => <InboxStackRow key={stack.id} stack={stack} members={members} open={open} />)}
          </InboxGroup>
          ) : null)}
        </div>
      </div>
    </section>
  );
}

function InboxStackRow({ stack, members, open }: {
  stack: InboxStack;
  members: Member[];
  open: (stack: InboxStack) => void;
}) {
  const item = stack.latest;
  const sender = members.find((member) => member.id === item.sender_member_id);
  const senderName = item.sender_display_name ?? "Sumi";
  const address = stack.kind === "dm"
    ? "DM"
    : item.channel_slug
      ? `#${item.channel_slug}`
      : "Thread";
  const countLabel = `${stack.items.length} new ${stack.items.length === 1 ? "message" : "messages"}`;
  return (
    <article className={`inbox-item inbox-item--${item.priority} inbox-item--stacked`}>
      <button className="inbox-open" type="button" onClick={() => open(stack)} aria-label={`Open ${address} from ${senderName}; ${countLabel}`}>
        <PixelIdentity name={senderName} kind={sender?.kind ?? "human"} seed={item.sender_member_id ?? item.id} />
        <span className="inbox-item-copy">
          <span className="inbox-item-meta">
            <strong>{senderName}</strong>
            <span className="inbox-address">{address}</span>
            <time dateTime={item.created_at}>{formatInboxTime(item.created_at)}</time>
          </span>
          <span className="inbox-message-preview">
            {formatInboxMessagePreview(item.message_preview) || item.summary || "Attention required"}
          </span>
          <span className="inbox-kind" aria-label={countLabel}>{countLabel}</span>
        </span>
      </button>
    </article>
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
        <b aria-label={`${count} new messages`}>{String(count).padStart(2, "0")}</b>
      </header>
      <div className="inbox-group-items">{children}</div>
    </section>
  );
}

function formatInboxMessagePreview(value: string | null | undefined): string {
  return value?.replace(/\\n/g, "\n").replace(/\s+/g, " ").trim() ?? "";
}

function formatInboxTime(value: string): string {
  return new Intl.DateTimeFormat(undefined, {
    month: "short",
    day: "numeric",
    hour: "2-digit",
    minute: "2-digit",
  }).format(new Date(value));
}
