import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Link } from "@tanstack/react-router";
import { CircleCheck, CircleDot, Hash, ListTodo, MessageSquareReply, Paperclip, Plus, Search, XCircle } from "lucide-react";
import { type ReactNode, type RefObject } from "react";

import { createTaskFromRootMessage, readThread, type Agent, type Attachment, type Message, type MessagePage, type MessageTaskSummary, type TaskStatus } from "../../api/client";
import { formatBytes } from "../../format";
import { PixelIdentity, PresenceIdentity } from "../SpaceShell";

export function MessageTimeline({
  timelineRef,
  header,
  page,
  pending,
  error,
  retry,
  emptyTitle,
  channelId,
  spaceSlug,
  openThread,
  activityByMemberId,
}: {
  timelineRef: RefObject<HTMLDivElement | null>;
  header?: ReactNode;
  page?: MessagePage;
  pending: boolean;
  error: Error | null;
  retry: () => void;
  emptyTitle: string;
  channelId: string;
  spaceSlug: string;
  openThread: (threadId: string, trigger: HTMLButtonElement) => void;
  activityByMemberId: ReadonlyMap<string, Agent["activity_status"]>;
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
                  <PresenceIdentity name={message.author.display_name} kind={message.author.kind} seed={message.author.id} activityStatus={activityByMemberId.get(message.author.id)} />
                )}
                <div className="message-content">
                  {grouped ? null : (
                    <header>
                      <strong>{message.author.display_name}</strong>
                      {message.author.kind === "agent" ? <span className="agent-label">AGENT</span> : null}
                      {message.task ? <MessageTaskBadge task={message.task} spaceSlug={spaceSlug} /> : null}
                      <time dateTime={message.created_at}>{formatMessageTime(message.created_at)}</time>
                      <span className="message-seq">@{message.seq}</span>
                    </header>
                  )}
                  <MessageBody message={message} spaceSlug={spaceSlug} />
                  {!message.deleted_at && message.attachments?.length ? (
                    <AttachmentList attachments={message.attachments} />
                  ) : null}
                  {!message.deleted_at && message.placement === "root" && !message.task ? (
                    <CreateTaskAction message={message} channelId={channelId} />
                  ) : null}
                  {!message.deleted_at ? (
                    <button
                      className="thread-action"
                      type="button"
                      title="Reply in Thread"
                      onClick={(event) => openThread(message.thread_id, event.currentTarget)}
                    >
                      <MessageSquareReply aria-hidden="true" />
                      <span className="visually-hidden">Reply in Thread</span>
                    </button>
                  ) : null}
                  {!message.deleted_at && message.thread_id && message.reply_count > 0 ? (
                    <InlineThreadPreview
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

export function CompactMessage({ message, activityStatus, spaceSlug }: { message: Message; activityStatus?: Agent["activity_status"]; spaceSlug: string }) {
  return (
    <article className="thread-message">
      <PresenceIdentity name={message.author.display_name} kind={message.author.kind} seed={message.author.id} activityStatus={activityStatus} />
      <div>
        <header>
          <strong>{message.author.display_name}</strong>
          {message.author.kind === "agent" ? <span className="agent-label">AGENT</span> : null}
          {message.task ? <MessageTaskBadge task={message.task} spaceSlug={spaceSlug} /> : null}
          <span className="message-seq">@{message.seq}</span>
        </header>
        <MessageBody message={message} spaceSlug={spaceSlug} />
        {!message.deleted_at && message.attachments?.length ? (
          <AttachmentList attachments={message.attachments} />
        ) : null}
      </div>
    </article>
  );
}

function MessageTaskBadge({ task, spaceSlug }: { task: MessageTaskSummary; spaceSlug: string }) {
  const assignee = task.assignee_name ?? "Unassigned";
  const status = task.status.replace("_", " ");
  const detail = `${task.title} · ${status} · ${assignee}`;
  return (
    <Link
      className={`message-task-badge message-task-badge--${task.status}`}
      to="/s/$spaceSlug/tasks/$taskId"
      params={{ spaceSlug, taskId: task.id }}
      aria-label={`Task: ${detail}`}
      data-tooltip={detail}
      tabIndex={0}
    >
      <TaskStatusIcon status={task.status} />
      <span>{task.title}</span>
      <b>{status.toUpperCase()}</b>
      <small>{assignee}{task.working_elsewhere ? " · Working elsewhere" : ""}</small>
    </Link>
  );
}

function InlineThreadPreview({ threadId, replyCount, open }: { threadId: string; replyCount: number; open: (trigger: HTMLButtonElement) => void }) {
  const thread = useQuery({
    queryKey: ["thread", threadId],
    queryFn: () => readThread(threadId),
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
          <span className="inline-reply-body">{reply.deleted_at ? "Message 已删除" : messagePreview(reply)}</span>
          <time dateTime={reply.created_at}>{formatMessageTime(reply.created_at)}</time>
        </button>
      ))}
    </section>
  );
}

function CreateTaskAction({ message, channelId }: { message: Message; channelId: string }) {
  const queryClient = useQueryClient();
  const create = useMutation({
    mutationFn: () => createTaskFromRootMessage(message.id, { title: textBody(message).trim().slice(0, 120) || undefined }),
    onSuccess: (task) => {
      queryClient.setQueryData<MessagePage>(["messages", channelId], (page) => page ? {
        ...page,
        messages: page.messages.map((item) => item.id === message.id ? {
          ...item,
          task: {
            id: task.id,
            title: task.title,
            status: task.status,
            assignee_agent_member_id: task.assignee_agent_member_id,
            assignee_name: task.assignee_name,
            working_elsewhere: false,
          },
        } : item),
      } : page);
      void queryClient.invalidateQueries({ queryKey: ["tasks", task.space_id] });
    },
  });
  return (
    <span className="create-task-control">
      <button type="button" className="create-task-action" disabled={create.isPending} onClick={() => create.mutate()}>
        <Plus aria-hidden="true" />{create.isPending ? "Creating Task…" : "Create Task"}
      </button>
      {create.error ? <span role="alert">Task creation failed. Retry.</span> : null}
    </span>
  );
}

function MessageBody({ message, spaceSlug }: { message: Message; spaceSlug: string }) {
  if (message.deleted_at) return <p>Message 已删除</p>;
  if (message.content.type === "text") return <p>{message.content.body_markdown}</p>;
  if (message.content.type === "channel_created") {
    return <p className="action-message"><Hash aria-hidden="true" /><strong>{message.author.display_name}</strong> Created channel {message.content.channel.available ? <Link to="/s/$spaceSlug/channels/$channelSlug" params={{ spaceSlug, channelSlug: message.content.channel.slug }}>#{message.content.channel.name}</Link> : <span>Unavailable channel</span>}</p>;
  }
  return <p className="action-message"><PixelIdentity name={message.content.agent.name} kind="agent" seed={message.content.agent.member_id} /><strong>{message.author.display_name}</strong> Created agent {message.content.agent.available ? <Link to="/s/$spaceSlug/agents/$agentId" params={{ spaceSlug, agentId: message.content.agent.member_id }}>{message.content.agent.name}</Link> : <span>Unavailable Agent</span>} <small>{message.content.agent.lifecycle}</small></p>;
}

function textBody(message: Message): string {
  return message.content.type === "text" ? message.content.body_markdown : "";
}

function messagePreview(message: Message): string {
  if (message.content.type === "text") return message.content.body_markdown;
  if (message.content.type === "channel_created") return `Created channel #${message.content.channel.name}`;
  return `Created agent ${message.content.agent.name} · ${message.content.agent.lifecycle}`;
}

function TaskStatusIcon({ status }: { status: TaskStatus }) {
  if (status === "done") return <CircleCheck aria-hidden="true" />;
  if (status === "closed") return <XCircle aria-hidden="true" />;
  if (status === "in_review") return <Search aria-hidden="true" />;
  if (status === "in_progress") return <CircleDot aria-hidden="true" />;
  return <ListTodo aria-hidden="true" />;
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
