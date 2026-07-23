import { CheckCircle2, Inbox, RefreshCw } from "lucide-react";
import { useState } from "react";
import { WorkApprovalDecision } from "../gen/sumi/work/v1/work_pb";
import { useHumanInbox } from "../hooks/useHumanInbox";
import { useWork } from "../hooks/useWork";
import { IconButton } from "./IconButton";

export function WorkInbox() {
  const inbox = useHumanInbox(true);
  const [selectedWorkID, setSelectedWorkID] = useState<string>();
  const selected = selectedWorkID ?? inbox.data?.items[0]?.workId;
  const work = useWork(selected, true);
  const resolveApproval = async (
    approvalId: string,
    decision: WorkApprovalDecision,
  ) => {
    await work.resolveApproval({ approvalId, decision, note: "" });
    await inbox.refresh();
  };

  if (inbox.status === "loading" || inbox.status === "idle") {
    return (
      <section className="timeline" aria-label="Work Inbox">
        <p>Loading Work Inbox…</p>
      </section>
    );
  }
  if (!inbox.data) {
    return (
      <section className="timeline" aria-label="Work Inbox">
        <p>Work Inbox is unavailable. {inbox.error}</p>
        <button onClick={() => void inbox.refresh()}>Retry</button>
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
          onClick={() => void inbox.refresh()}
        >
          <RefreshCw size={16} />
        </IconButton>
      </header>
      {inbox.data.items.length === 0 ? (
        <p className="empty-state">No Work needs your attention.</p>
      ) : (
        <ul className="work-inbox-list">
          {inbox.data.items.map((item) => (
            <li key={`${item.kind}:${item.workId}:${item.agentId}`}>
              <button
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
                <code>{item.workId}</code>
              </button>
            </li>
          ))}
        </ul>
      )}
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
