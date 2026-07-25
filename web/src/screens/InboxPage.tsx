import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useNavigate, useParams } from "@tanstack/react-router";
import { Check, Clock3, Inbox, Menu } from "lucide-react";

import { ackInboxItem, deferInboxItem, listInbox, type InboxItem } from "../api/client";
import { SpaceShell } from "../components/SpaceShell";

export function InboxPage() {
  const { spaceSlug } = useParams({ from: "/s/$spaceSlug/inbox" });
  return (
    <SpaceShell spaceSlug={spaceSlug} active="inbox">
      {({ space, currentMember, openNavigation }) => (
        <InboxWorkspace
          spaceSlug={space.slug}
          spaceId={space.id}
          memberId={currentMember.id}
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
  openNavigation,
}: {
  spaceSlug: string;
  spaceId: string;
  memberId: string;
  openNavigation: () => void;
}) {
  const queryClient = useQueryClient();
  const navigate = useNavigate();
  const inbox = useQuery({
    queryKey: ["inbox", spaceId, memberId],
    queryFn: () => listInbox(memberId),
  });
  const refresh = () => queryClient.invalidateQueries({ queryKey: ["inbox", spaceId] });
  const ack = useMutation({ mutationFn: ackInboxItem, onSuccess: refresh });
  const defer = useMutation({
    mutationFn: (itemId: string) =>
      deferInboxItem(itemId, new Date(Date.now() + 60 * 60 * 1000).toISOString()),
    onSuccess: refresh,
  });

  function open(item: InboxItem) {
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
          <p>Attention that waits until you explicitly finish or defer it.</p>
        </div>
      </header>
      <div className="inbox-list">
        {inbox.isPending ? <p className="timeline-status">Loading Inbox...</p> : null}
        {inbox.error ? <p className="timeline-status timeline-status--error">{inbox.error.message}</p> : null}
        {inbox.data?.length === 0 ? (
          <div className="empty-channel"><Inbox aria-hidden="true" /><h2>Inbox is clear.</h2></div>
        ) : null}
        {inbox.data?.map((item) => (
          <article className={`inbox-item inbox-item--${item.priority}`} key={item.id}>
            <button className="inbox-open" type="button" onClick={() => open(item)}>
              <span className="inbox-kind">{item.kind.replace("_", " ")}</span>
              <strong>{item.sender_display_name ?? "Sumi"}</strong>
              <span className="inbox-address">{item.channel_slug ? `#${item.channel_slug}${item.thread_id ? `:${item.thread_id}` : ""}` : "SYSTEM"}</span>
              <p>{item.summary ?? "Attention required"}</p>
            </button>
            <div className="inbox-actions">
              <button type="button" aria-label="Complete Inbox Item" onClick={() => ack.mutate(item.id)}><Check />DONE</button>
              <button type="button" aria-label="Defer Inbox Item one hour" onClick={() => defer.mutate(item.id)}><Clock3 />LATER</button>
            </div>
          </article>
        ))}
      </div>
    </section>
  );
}
