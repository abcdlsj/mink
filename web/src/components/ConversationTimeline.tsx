import { MessageSquareMore, MessageSquareText, RotateCw } from "lucide-react";
import type { Agent } from "../gen/sumi/agent/v1/agent_pb";

import { agentDisplayName } from "../lib/format";
import type { Human } from "../gen/sumi/organization/v1/organization_pb";
import { PrincipalKind, type Message } from "../gen/sumi/space/v1/space_pb";
import type { ConversationSnapshot } from "../lib/collaboration";
import { IconButton } from "./IconButton";
import { PixelAvatar } from "./PixelAvatar";

export function ConversationTimeline({
  snapshot,
  humans,
  agents,
  status,
  error,
  onRefresh,
  onLoadMore,
  onOpenThread,
}: {
  snapshot: ConversationSnapshot;
  humans: Human[];
  agents: Agent[];
  status: "ready" | "refreshing" | "stale";
  error?: string;
  onRefresh: () => void;
  onLoadMore: () => void;
  onOpenThread: (message: Message) => void;
}) {
  return (
    <div className="timeline conversation-timeline" data-testid="timeline">
      {(status === "refreshing" || status === "stale") && (
        <div className={`stale-notice ${status}`} role="status">
          <span>
            {status === "refreshing"
              ? "Refreshing conversation…"
              : error ||
                "Conversation refresh failed. Showing the last snapshot."}
          </span>
          {status === "stale" && (
            <button type="button" onClick={onRefresh}>
              Retry
            </button>
          )}
        </div>
      )}
      {snapshot.messages.length === 0 ? (
        <div className="empty-state compact-empty">
          <MessageSquareText size={28} />
          <h2>Start the conversation</h2>
          <p>Messages, decisions, and shared work will stay together here.</p>
        </div>
      ) : (
        <ol className="message-list" aria-label="Messages">
          {snapshot.messages.map((message) => (
            <li
              className={`message-row message-${authorKind(message)}`}
              key={message.id}
            >
              <PixelAvatar
                className="message-avatar"
                seed={message.author?.id ?? message.id}
                kind={authorKind(message)}
                size="md"
              />
              <article>
                <header>
                  <strong>{authorName(message, humans, agents)}</strong>
                  <time>{formatMessageTime(message)}</time>
                </header>
                <p>{message.body}</p>
                <IconButton
                  className="compact message-thread-action"
                  label="Open thread"
                  tooltip="Open message thread"
                  tooltipPlacement="left"
                  onClick={() => onOpenThread(message)}
                >
                  <MessageSquareMore size={15} />
                </IconButton>
              </article>
            </li>
          ))}
        </ol>
      )}
      {snapshot.hasMore && (
        <button className="load-more" type="button" onClick={onLoadMore}>
          <RotateCw size={14} /> More messages available
        </button>
      )}
    </div>
  );
}

function authorKind(message: Message): "agent" | "human" {
  return message.author?.kind === PrincipalKind.AGENT ? "agent" : "human";
}

export function authorName(
  message: Message,
  humans: readonly Human[],
  agents: readonly Agent[],
) {
  if (message.author?.kind === PrincipalKind.HUMAN) {
    return (
      humans.find((human) => human.id === message.author?.id)?.name ||
      "Unknown Human"
    );
  }
  if (message.author?.kind === PrincipalKind.AGENT) {
    return (
      (() => {
        const agent = agents.find(
          (candidate) => candidate.id === message.author?.id,
        );
        return agent ? agentDisplayName(agent) : undefined;
      })() || "Unknown Agent"
    );
  }
  return "Unknown author";
}

function formatMessageTime(message: Message) {
  if (!message.createdAt) return `#${message.targetSequence}`;
  return new Date(Number(message.createdAt.seconds) * 1000).toLocaleString([], {
    month: "short",
    day: "numeric",
    hour: "2-digit",
    minute: "2-digit",
  });
}
