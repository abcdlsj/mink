import { useQuery } from "@tanstack/react-query";
import { Hash, ListTodo, MessageSquareReply, Paperclip } from "lucide-react";
import { type ReactNode, type RefObject } from "react";

import { readThread, type Attachment, type Message, type MessagePage } from "../../api/client";
import { formatBytes } from "../../format";
import { PixelIdentity } from "../SpaceShell";

export function MessageTimeline({
  timelineRef,
  header,
  page,
  pending,
  error,
  retry,
  emptyTitle,
  channelId,
  creatingThread,
  createThread,
  openThread,
}: {
  timelineRef: RefObject<HTMLDivElement | null>;
  header?: ReactNode;
  page?: MessagePage;
  pending: boolean;
  error: Error | null;
  retry: () => void;
  emptyTitle: string;
  channelId: string;
  creatingThread: boolean;
  createThread: (messageId: string, trigger: HTMLButtonElement) => void;
  openThread: (threadId: number, trigger: HTMLButtonElement) => void;
}) {
  return (
      <div ref={timelineRef} className="message-timeline" aria-live="polite">
        {header}
        {pending ? <div className="timeline-status">Loading Messages...</div> : null}
        {error ? (
          <div className="timeline-status timeline-status--error" role="alert">
            <span>{error.message}</span>
            <button className="compact-action" type="button" onClick={retry}>Retry</button>
          </div>
        ) : null}
        {page?.messages.length === 0 ? (
          <div className="empty-channel">
            <span className="channel-glyph" aria-hidden="true">
              <Hash />
            </span>
            <h2>{emptyTitle}</h2>
          </div>
        ) : null}
        {page?.messages.map((message, index, all) => {
          const previous = index > 0 ? all[index - 1] : undefined;
          const showDivider = !previous || dayKey(previous.created_at) !== dayKey(message.created_at);
          // A day divider always restarts the visual grouping.
          const grouped = !showDivider && !message.task && message.reply_count === 0 && !startsNewGroup(message, previous);
          return (
            <div className="message-block" id={`message-${message.id}`} tabIndex={-1} key={message.id}>
              {showDivider ? (
                <div className="day-divider" role="separator">
                  <time dateTime={message.created_at}>{formatDayDivider(message.created_at)}</time>
                </div>
              ) : null}
              <article className={`message-row${grouped ? " message-row--grouped" : ""}${message.reply_count > 0 ? " message-row--has-thread" : ""}`}>
                {grouped ? (
                  <time className="message-gutter-time" dateTime={message.created_at}>
                    {formatMessageTime(message.created_at)}
                  </time>
                ) : (
                  <PixelIdentity name={message.author.display_name} kind={message.author.kind} seed={message.author.id} />
                )}
                <div className="message-content">
                  {grouped ? null : (
                    <header>
                      <strong>{message.author.display_name}</strong>
                      {message.author.kind === "agent" ? <span className="agent-label">AGENT</span> : null}
                      {message.task ? <MessageTaskBadge task={message.task} /> : null}
                      <time dateTime={message.created_at}>{formatMessageTime(message.created_at)}</time>
                      <span className="message-seq">@{message.seq}</span>
                    </header>
                  )}
                  <p>{message.deleted_at ? "Message 已删除" : message.body_markdown}</p>
                  {!message.deleted_at && message.attachments?.length ? (
                    <AttachmentList attachments={message.attachments} />
                  ) : null}
                  {!message.deleted_at && message.reply_count === 0 ? (
                    <button
                      className="thread-action"
                      type="button"
                      disabled={creatingThread}
                      onClick={(event) => {
                        if (message.thread_id) openThread(message.thread_id, event.currentTarget);
                        else createThread(message.id, event.currentTarget);
                      }}
                    >
                      <MessageSquareReply aria-hidden="true" />
                      Reply in Thread
                    </button>
                  ) : null}
                  {!message.deleted_at && message.thread_id && message.reply_count > 0 ? (
                    <InlineThreadPreview
                      channelId={channelId}
                      threadId={message.thread_id}
                      replyCount={message.reply_count}
                      open={(trigger) => openThread(message.thread_id!, trigger)}
                    />
                  ) : null}
                </div>
              </article>
            </div>
          );
        })}
      </div>
  );
}

export function CompactMessage({ message }: { message: Message }) {
  return (
    <article className="thread-message">
      <PixelIdentity name={message.author.display_name} kind={message.author.kind} seed={message.author.id} />
      <div>
        <header>
          <strong>{message.author.display_name}</strong>
          {message.author.kind === "agent" ? <span className="agent-label">AGENT</span> : null}
          {message.task ? <MessageTaskBadge task={message.task} /> : null}
          <span className="message-seq">@{message.seq}</span>
        </header>
        <p>{message.deleted_at ? "Message 已删除" : message.body_markdown}</p>
        {!message.deleted_at && message.attachments?.length ? (
          <AttachmentList attachments={message.attachments} />
        ) : null}
      </div>
    </article>
  );
}

function MessageTaskBadge({ task }: { task: NonNullable<Message["task"]> }) {
  const assignee = task.assignee_name ?? "Unassigned";
  const status = task.status.replace("_", " ");
  const detail = `${task.title} · ${status} · ${assignee}`;
  return (
    <span
      className={`message-task-badge message-task-badge--${task.status}`}
      aria-label={`Task: ${detail}`}
      data-tooltip={detail}
      tabIndex={0}
    >
      <ListTodo aria-hidden="true" />
      TASK
    </span>
  );
}

function InlineThreadPreview({ channelId, threadId, replyCount, open }: { channelId: string; threadId: number; replyCount: number; open: (trigger: HTMLButtonElement) => void }) {
  const thread = useQuery({
    queryKey: ["thread", channelId, threadId],
    queryFn: () => readThread(channelId, threadId),
    staleTime: 15_000,
  });
  const replies = thread.data?.replies.slice(-3) ?? [];
  const hiddenReplyCount = Math.max(0, replyCount - replies.length);
  return (
    <section className="inline-thread-preview" aria-label={`${replyCount} Thread ${replyCount === 1 ? "reply" : "replies"}`}>
      <button className="inline-thread-heading" type="button" aria-label={`${replyCount} ${replyCount === 1 ? "reply" : "replies"}`} onClick={(event) => open(event.currentTarget)}>
        <span>{hiddenReplyCount > 0 ? `${hiddenReplyCount} earlier ${hiddenReplyCount === 1 ? "reply" : "replies"}` : `${replyCount} ${replyCount === 1 ? "reply" : "replies"}`} <b aria-hidden="true">›</b></span>
      </button>
      {thread.isPending ? <span className="inline-thread-status">Loading replies…</span> : null}
      {thread.error ? <span className="inline-thread-status">Replies unavailable</span> : null}
      {replies.map((reply) => (
        <button className="inline-reply" type="button" key={reply.id} onClick={(event) => open(event.currentTarget)}>
          <PixelIdentity name={reply.author.display_name} kind={reply.author.kind} seed={reply.author.id} />
          <span className="inline-reply-author">
            <strong>{reply.author.display_name}</strong>
            {reply.author.kind === "agent" ? <span className="agent-label">AGENT</span> : null}
          </span>
          <span className="inline-reply-body">{reply.deleted_at ? "Message 已删除" : reply.body_markdown}</span>
          <time dateTime={reply.created_at}>{formatMessageTime(reply.created_at)}</time>
        </button>
      ))}
    </section>
  );
}

function AttachmentList({ attachments }: { attachments: Attachment[] }) {
  return (
    <div className="message-attachments">
      {attachments.map((attachment) => (
        <a
          key={attachment.id}
          href={attachment.download_path ?? undefined}
          download={attachment.original_name}
        >
          <Paperclip aria-hidden="true" />
          <span>
            <strong>{attachment.original_name}</strong>
            <small>{formatBytes(attachment.size ?? 0)}</small>
          </span>
        </a>
      ))}
    </div>
  );
}

function formatMessageTime(value: string): string {
  return new Intl.DateTimeFormat(undefined, { hour: "2-digit", minute: "2-digit" }).format(
    new Date(value),
  );
}

const MESSAGE_GROUP_GAP_MS = 5 * 60 * 1000;

// Merge a message into the block above it when the same author posted within a
// short window, so a burst reads as one turn instead of repeating the identity.
function startsNewGroup(message: Message, previous: Message | undefined): boolean {
  if (!previous) return true;
  if (previous.author.id !== message.author.id) return true;
  const gap = new Date(message.created_at).getTime() - new Date(previous.created_at).getTime();
  return !Number.isFinite(gap) || gap > MESSAGE_GROUP_GAP_MS;
}

function dayKey(value: string): string {
  const date = new Date(value);
  return `${date.getFullYear()}-${date.getMonth()}-${date.getDate()}`;
}

function formatDayDivider(value: string): string {
  const date = new Date(value);
  const today = new Date();
  const yesterday = new Date(today);
  yesterday.setDate(today.getDate() - 1);
  if (dayKey(value) === dayKey(today.toISOString())) return "Today";
  if (dayKey(value) === dayKey(yesterday.toISOString())) return "Yesterday";
  const sameYear = date.getFullYear() === today.getFullYear();
  return new Intl.DateTimeFormat(undefined, {
    month: "long",
    day: "numeric",
    year: sameYear ? undefined : "numeric",
  }).format(date);
}
