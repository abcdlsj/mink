import { ArrowLeft, MessageSquareText, RotateCw, X } from "lucide-react";
import type { DirectorySnapshot, ThreadSnapshot } from "../lib/collaboration";
import { authorName } from "./ConversationTimeline";
import { MessageComposer } from "./MessageComposer";
import { IconButton } from "./IconButton";

export function ThreadPane({
  snapshot,
  directory,
  status,
  error,
  compact,
  sendDisabledReason,
  onClose,
  onRefresh,
  onLoadMore,
  onSend,
}: {
  snapshot?: ThreadSnapshot;
  directory: DirectorySnapshot;
  status: "idle" | "loading" | "ready" | "refreshing" | "error" | "stale";
  error?: string;
  compact: boolean;
  sendDisabledReason?: string;
  onClose: () => void;
  onRefresh: () => void;
  onLoadMore: () => void;
  onSend: (requestId: string, body: string) => Promise<void>;
}) {
  return (
    <>
      <header className="context-header">
        <IconButton
          className="compact-context-back"
          label="Back to conversation"
          tooltipPlacement="right"
          onClick={onClose}
        >
          <ArrowLeft size={18} />
        </IconButton>
        <div>
          <span className="eyebrow">Thread</span>
          <strong>
            {snapshot
              ? authorName(snapshot.root, directory.humans, directory.agents)
              : "Loading thread"}
          </strong>
        </div>
        <IconButton
          className={compact ? "" : "desktop-context-close"}
          label="Close thread"
          tooltipPlacement="left"
          onClick={onClose}
        >
          <X size={18} />
        </IconButton>
      </header>
      {status === "idle" ||
      status === "loading" ||
      (!snapshot && status === "refreshing") ? (
        <div className="context-feedback" role="status">
          Loading thread…
        </div>
      ) : !snapshot ? (
        <div className="context-feedback error" role="alert">
          <strong>Thread unavailable</strong>
          <p>{error}</p>
          <button
            className="secondary-action"
            type="button"
            onClick={onRefresh}
          >
            Retry
          </button>
        </div>
      ) : (
        <div className="thread-layout">
          <div className="thread-messages">
            {(status === "stale" || status === "refreshing") && (
              <div className={`stale-notice ${status}`}>
                <span>{status === "stale" ? error : "Refreshing thread…"}</span>
                {status === "stale" && (
                  <button type="button" onClick={onRefresh}>
                    Retry
                  </button>
                )}
              </div>
            )}
            <article className="thread-root">
              <strong>
                {authorName(snapshot.root, directory.humans, directory.agents)}
              </strong>
              <p>{snapshot.root.body}</p>
            </article>
            {snapshot.replies.length === 0 ? (
              <div className="thread-empty">
                <MessageSquareText size={22} />
                <p>No replies yet. Start the Thread below.</p>
              </div>
            ) : (
              <ol className="thread-reply-list" aria-label="Thread replies">
                {snapshot.replies.map((reply) => (
                  <li key={reply.id}>
                    <strong>
                      {authorName(reply, directory.humans, directory.agents)}
                    </strong>
                    <p>{reply.body}</p>
                  </li>
                ))}
              </ol>
            )}
            {snapshot.hasMore && (
              <button className="load-more" type="button" onClick={onLoadMore}>
                <RotateCw size={14} /> More replies available
              </button>
            )}
          </div>
          <MessageComposer
            key={snapshot.root.id}
            targetKey={`thread:${snapshot.root.id}`}
            label="thread"
            placeholder="Reply in Thread"
            disabledReason={sendDisabledReason}
            onSend={onSend}
          />
        </div>
      )}
    </>
  );
}
