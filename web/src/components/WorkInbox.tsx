import {
  Check,
  CheckCircle2,
  Inbox,
  MessageSquare,
  RefreshCw,
} from "lucide-react";
import { useState } from "react";
import { InboxReason, type InboxItem } from "../gen/sumi/inbox/v1/inbox_pb";
import type { Space } from "../gen/sumi/space/v1/space_pb";
import { useInbox } from "../hooks/useInbox";
import { WorkApprovalDecision } from "../gen/sumi/work/v1/work_pb";
import { useWorkAttention } from "../hooks/useWorkAttention";
import { useWork } from "../hooks/useWork";
import type { InboxDestination } from "../lib/inbox";
import { IconButton } from "./IconButton";

export function WorkInbox({
  spaces,
  onOpenMessage,
}: {
  spaces: Space[];
  onOpenMessage: (destination: InboxDestination) => void;
}) {
  const messages = useInbox(true);
  const workAttention = useWorkAttention(true);
  const [selectedWorkID, setSelectedWorkID] = useState<string>();
  const selected = selectedWorkID ?? workAttention.data?.items[0]?.workId;
  const work = useWork(selected, true);
  const resolveApproval = async (
    approvalId: string,
    decision: WorkApprovalDecision,
  ) => {
    await work.resolveApproval({ approvalId, decision, note: "" });
    await workAttention.refresh();
  };
  const openMessage = async (item: InboxItem) => {
    const destination = await messages.open(item);
    onOpenMessage(destination);
  };

  if (
    (messages.status === "loading" || messages.status === "idle") &&
    (workAttention.status === "loading" || workAttention.status === "idle")
  ) {
    return (
      <section className="timeline" aria-label="Work Inbox">
        <p>Loading Work Inbox…</p>
      </section>
    );
  }
  if (!messages.data && !workAttention.data) {
    return (
      <section className="timeline" aria-label="Work Inbox">
        <p>
          Work Inbox is unavailable. {messages.error || workAttention.error}
        </p>
        <button
          onClick={() =>
            void Promise.all([messages.refresh(), workAttention.refresh()])
          }
        >
          Retry
        </button>
      </section>
    );
  }
  return (
    <section
      className="timeline workspace-list-view work-inbox"
      aria-label="Work Inbox"
    >
      <header className="workspace-list-header">
        <div>
          <Inbox size={18} />
          <strong>Work Inbox</strong>
        </div>
        <IconButton
          label="Refresh Work Inbox"
          tooltipPlacement="left"
          onClick={() =>
            void Promise.all([messages.refresh(), workAttention.refresh()])
          }
        >
          <RefreshCw size={16} />
        </IconButton>
      </header>
      <div className="work-inbox-queues">
        <section className="work-inbox-section" aria-label="Message attention">
          <h2>Messages</h2>
          {messages.data?.items.length ? (
            <ul className="work-inbox-list">
              {messages.data.items.map((item) => (
                <li key={item.id}>
                  <button
                    className="work-inbox-row"
                    type="button"
                    onClick={() => void openMessage(item)}
                  >
                    <MessageSquare size={15} />
                    <strong>{messageReason(item.reason)}</strong>
                    <span>{spaceName(spaces, item.spaceId)}</span>
                    <small>
                      {item.target?.target.case === "threadRootMessageId"
                        ? "Thread"
                        : "Space"}
                    </small>
                  </button>
                  {messages.canClaim(item) && (
                    <IconButton
                      className="compact"
                      label="Claim message attention"
                      disabled={messages.pendingItemId === item.id}
                      onClick={() => void messages.claim(item)}
                    >
                      <Check size={14} />
                    </IconButton>
                  )}
                  {messages.canComplete(item) && (
                    <IconButton
                      className="compact"
                      label="Complete message attention"
                      disabled={messages.pendingItemId === item.id}
                      onClick={() => void messages.complete(item)}
                    >
                      <CheckCircle2 size={14} />
                    </IconButton>
                  )}
                </li>
              ))}
            </ul>
          ) : (
            <p className="section-empty">No messages need your attention.</p>
          )}
          {(messages.error || messages.actionError) && (
            <p role="alert">{messages.actionError || messages.error}</p>
          )}
        </section>
        <section className="work-inbox-section" aria-label="Work attention">
          <h2>Work attention</h2>
          {workAttention.data?.items.length ? (
            <ul className="work-inbox-list">
              {workAttention.data.items.map((item) => (
                <li key={`${item.kind}:${item.workId}:${item.agentId}`}>
                  <button
                    className="work-inbox-row"
                    type="button"
                    onClick={() => setSelectedWorkID(item.workId)}
                    aria-pressed={selected === item.workId}
                  >
                    <strong>
                      {item.kind === "work_approval"
                        ? "Approval requested"
                        : "Agent exception"}
                    </strong>
                    <span>{item.reasonCode || item.status}</span>
                    <small>{spaceName(spaces, item.spaceId)}</small>
                  </button>
                </li>
              ))}
            </ul>
          ) : (
            <p className="section-empty">No Work needs your attention.</p>
          )}
        </section>
      </div>
      {work.data?.detail?.detail?.work && (
        <section className="work-detail" aria-label="Selected Work">
          <h2>{work.data.detail.detail.work.goal || "Work"}</h2>
          {work.data.detail.detail.approvals.every(
            (approval) => approval.status !== 1,
          ) && (
            <p className="section-empty">No pending approval for this Work.</p>
          )}
          {work.data.detail.detail.approvals
            .filter((approval) => approval.status === 1)
            .map((approval) => (
              <div key={approval.id} className="approval-action">
                <p>{approval.question}</p>
                <button
                  disabled={work.action.pending}
                  onClick={() =>
                    void resolveApproval(
                      approval.id,
                      WorkApprovalDecision.APPROVED,
                    )
                  }
                >
                  <CheckCircle2 size={15} /> Approve
                </button>
                <button
                  disabled={work.action.pending}
                  onClick={() =>
                    void resolveApproval(
                      approval.id,
                      WorkApprovalDecision.REJECTED,
                    )
                  }
                >
                  Reject
                </button>
              </div>
            ))}
          {work.action.error !== undefined && (
            <p role="alert">Could not update approval.</p>
          )}
        </section>
      )}
    </section>
  );
}

function messageReason(reason: InboxReason) {
  if (reason === InboxReason.DM) return "Direct message";
  if (reason === InboxReason.MENTION) return "Mention";
  if (reason === InboxReason.THREAD_FOLLOW) return "Thread reply";
  return "Message attention";
}

function spaceName(spaces: Space[], spaceId: string) {
  const space = spaces.find((candidate) => candidate.id === spaceId);
  return space?.name || (space ? "Direct message" : "Unavailable Space");
}
