import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useNavigate, useParams } from "@tanstack/react-router";
import { Check, Clock3, Inbox, Menu, X } from "lucide-react";

import {
  ackInboxItem,
  deferInboxItem,
  listApprovals,
  listInbox,
  resolveApproval,
  type InboxItem,
  type Member,
} from "../api/client";
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
          currentMember={currentMember}
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
  currentMember,
  openNavigation,
}: {
  spaceSlug: string;
  spaceId: string;
  memberId: string;
  currentMember: Member;
  openNavigation: () => void;
}) {
  const queryClient = useQueryClient();
  const navigate = useNavigate();
  const inbox = useQuery({
    queryKey: ["inbox", spaceId, memberId],
    queryFn: () => listInbox(memberId),
  });
  const canGovern = currentMember.access_level === "owner" || currentMember.access_level === "admin";
  const approvals = useQuery({
    queryKey: ["approvals", spaceId],
    queryFn: () => listApprovals(spaceId),
    enabled: canGovern,
  });
  const refresh = () => queryClient.invalidateQueries({ queryKey: ["inbox", spaceId] });
  const ack = useMutation({ mutationFn: ackInboxItem, onSuccess: refresh });
  const defer = useMutation({
    mutationFn: (itemId: string) =>
      deferInboxItem(itemId, new Date(Date.now() + 60 * 60 * 1000).toISOString()),
    onSuccess: refresh,
  });
  const resolve = useMutation({
    mutationFn: ({ approvalId, decision }: { approvalId: string; decision: "approve" | "reject" }) =>
      resolveApproval(approvalId, decision),
    onSuccess: () => {
      void queryClient.invalidateQueries({ queryKey: ["approvals", spaceId] });
      void queryClient.invalidateQueries({ queryKey: ["inbox", spaceId] });
    },
  });
  const pendingApprovals = approvals.data?.filter((approval) => approval.status === "pending") ?? [];
  const attentionItems = [...(inbox.data?.filter((item) => item.kind !== "approval") ?? [])].sort(
    (left, right) => inboxRank(left.kind) - inboxRank(right.kind),
  );

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
        {canGovern && pendingApprovals.length > 0 ? (
          <section className="approval-list" aria-labelledby="approval-heading">
            <div className="approval-heading">
              <span>GOVERNANCE</span>
              <h2 id="approval-heading">Agent creation</h2>
            </div>
            {pendingApprovals.map((approval) => (
              <article className="approval-item" key={approval.id}>
                <div>
                  <span className="inbox-kind">approval</span>
                  <strong>{approval.payload.name}</strong>
                  <p>Requested by {approval.requester_name} for the {approval.payload.driver_kind} Driver.</p>
                </div>
                <div className="inbox-actions">
                  <button
                    type="button"
                    disabled={resolve.isPending}
                    aria-label={`Approve ${approval.payload.name}`}
                    onClick={() => resolve.mutate({ approvalId: approval.id, decision: "approve" })}
                  >
                    <Check />APPROVE
                  </button>
                  <button
                    type="button"
                    disabled={resolve.isPending}
                    aria-label={`Reject ${approval.payload.name}`}
                    onClick={() => resolve.mutate({ approvalId: approval.id, decision: "reject" })}
                  >
                    <X />REJECT
                  </button>
                </div>
              </article>
            ))}
            {resolve.error ? <p className="timeline-status timeline-status--error">{resolve.error.message}</p> : null}
          </section>
        ) : null}
        {inbox.isPending ? <p className="timeline-status">Loading Inbox...</p> : null}
        {inbox.error ? <p className="timeline-status timeline-status--error">{inbox.error.message}</p> : null}
        {!inbox.isPending && !inbox.error && (!canGovern || !approvals.isPending) && attentionItems.length === 0 && pendingApprovals.length === 0 ? (
          <section className="inbox-empty" aria-labelledby="inbox-empty-title">
            <div className="inbox-empty-intro">
              <span className="inbox-empty-icon" aria-hidden="true"><Inbox /></span>
              <div>
                <p className="section-kicker">ATTENTION QUEUE</p>
                <h2 id="inbox-empty-title">Nothing needs your attention</h2>
                <p>Inbox holds collaboration you must explicitly finish or defer. It is not your Message history.</p>
              </div>
            </div>
            <ul className="inbox-empty-groups" aria-label="Empty Inbox groups">
              <li><span><strong>Approvals</strong><small>Governance decisions waiting for you</small></span><b>0</b></li>
              <li><span><strong>DM &amp; mentions</strong><small>Direct requests and explicit mentions</small></span><b>0</b></li>
              <li><span><strong>Replies</strong><small>Thread and Message responses to revisit</small></span><b>0</b></li>
              <li><span><strong>Channel activity</strong><small>Ambient collaboration worth reviewing</small></span><b>0</b></li>
            </ul>
          </section>
        ) : null}
        {attentionItems.map((item) => (
          <article className={`inbox-item inbox-item--${item.priority}`} key={item.id}>
            <button className="inbox-open" type="button" onClick={() => open(item)}>
              <span className="inbox-kind">{item.kind.replace("_", " ")}</span>
              <strong>{item.sender_display_name ?? "Sumi"}</strong>
              <span className="inbox-address">{item.channel_slug ? `#${item.channel_slug}${item.thread_id ? `:${item.thread_id}` : ""}` : "SYSTEM"}</span>
              <p>{item.summary ?? "Attention required"}</p>
              <time dateTime={item.created_at}>{new Date(item.created_at).toLocaleString()}</time>
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

function inboxRank(kind: InboxItem["kind"]): number {
  if (kind === "direct" || kind === "mention") return 0;
  if (kind === "reply" || kind === "thread_activity") return 1;
  if (kind === "channel_activity") return 2;
  return 3;
}
