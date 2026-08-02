import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Link } from "@tanstack/react-router";
import { CircleCheck, CircleDot, Hash, ListTodo, LoaderCircle, MessageSquareReply, Paperclip, Search, XCircle } from "lucide-react";
import { type ReactNode, type RefObject, useEffect, useLayoutEffect, useRef, useState } from "react";

import { createTaskFromRootMessage, readThread, type Agent, type Attachment, type Member, type Message, type MessagePage, type MessageTaskSummary, type TaskStatus } from "../../api/client";
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
  members,
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
  openThread: (threadId: string, trigger?: HTMLButtonElement) => void;
  activityByMemberId: ReadonlyMap<string, Agent["activity_status"]>;
  members: Member[];
}) {
  const [newMessageAnnouncement, setNewMessageAnnouncement] = useState("");
  const announcedMessageRef = useRef<string | null>(null);

  // The timeline itself must not be a live region: a polite region on the
  // whole list makes screen readers re-read every message on each update.
  // Announce only the newest arrival through one stable status region.
  useEffect(() => {
    if (!page || page.messages.length === 0) return;
    const latest = page.messages[page.messages.length - 1];
    if (announcedMessageRef.current === null) {
      announcedMessageRef.current = latest.id;
      return;
    }
    if (latest.id !== announcedMessageRef.current) {
      announcedMessageRef.current = latest.id;
      setNewMessageAnnouncement(
        `New message from ${latest.author.display_name} (#${latest.seq})`,
      );
    }
  }, [page]);

  return (
      <div ref={timelineRef} className="message-timeline">
        <div className="visually-hidden" role="status">{newMessageAnnouncement}</div>
        {header}
        {pending ? <div className="timeline-status">Loading Messages...</div> : null}
        {error ? (
          <div className="system-event system-event--standalone" role="alert">
            <div className="system-event-heading">
              <span>{error.message}</span>
              <button type="button" onClick={retry}>Retry</button>
            </div>
          </div>
        ) : null}
        {page?.messages.length === 0 ? (
          <div className="empty-channel">
            <span className="channel-glyph" aria-hidden="true">
              <Hash />
            </span>
            <h2>{emptyTitle}</h2>
            <p>Messages you send in this conversation will appear here.</p>
          </div>
        ) : null}
        {page?.messages.map((message, index, all) => {
          const previous = index > 0 ? all[index - 1] : undefined;
          const showDivider = !previous || dayKey(previous.created_at) !== dayKey(message.created_at);
          // A day divider always restarts the visual grouping.
          const grouped = !showDivider && !message.task && message.reply_count === 0 && !startsNewGroup(message, previous);
          return (
            <div className="message-block" id={`message-${message.id}`} data-message-id={message.id} tabIndex={-1} key={message.id}>
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
                  <MessageBody
                    message={message}
                    spaceSlug={spaceSlug}
                    members={members}
                  />
                  {!message.deleted_at && message.attachments?.length ? (
                    <AttachmentList attachments={message.attachments} />
                  ) : null}
                  <AttentionFailureNotice message={message} members={members} />
                  {!message.deleted_at && message.thread_id && message.reply_count > 0 ? (
                    <InlineThreadPreview
                      threadId={message.thread_id}
                      replyCount={message.reply_count}
                      open={(trigger) => openThread(message.thread_id!, trigger)}
                    />
                  ) : null}
                </div>
                {!message.deleted_at ? (
                  <MessageActions
                    message={message}
                    channelId={channelId}
                    openThread={openThread}
                  />
                ) : null}
              </article>
            </div>
          );
        })}
      </div>
  );
}

export function CompactMessage({ message, activityStatus, spaceSlug, members }: { message: Message; activityStatus?: Agent["activity_status"]; spaceSlug: string; members: Member[] }) {
  return (
    <article className="thread-message" id={`message-${message.id}`} data-message-id={message.id} tabIndex={-1}>
      <PresenceIdentity name={message.author.display_name} kind={message.author.kind} seed={message.author.id} activityStatus={activityStatus} />
      <div>
        <header>
          <strong>{message.author.display_name}</strong>
          {message.author.kind === "agent" ? <span className="agent-label">AGENT</span> : null}
          {message.task ? <MessageTaskBadge task={message.task} spaceSlug={spaceSlug} /> : null}
          <span className="message-seq">@{message.seq}</span>
        </header>
        <MessageBody message={message} spaceSlug={spaceSlug} members={members} />
        <AttentionFailureNotice message={message} members={members} />
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
          <span className="inline-reply-body">{reply.deleted_at ? "Message deleted" : messagePreview(reply)}</span>
          <time dateTime={reply.created_at}>{formatMessageTime(reply.created_at)}</time>
        </button>
      ))}
    </section>
  );
}

function MessageActions({ message, channelId, openThread }: { message: Message; channelId: string; openThread: (threadId: string, trigger: HTMLButtonElement) => void }) {
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
  const taskUnavailableReason = message.placement !== "root"
    ? "Task can only be created from a Root Message"
    : message.task
      ? "This Message already has a Task"
      : undefined;
  return (
    <div className="message-actions" role="toolbar" aria-label="Message actions">
      <button
        className="message-action-button"
        type="button"
        title="Reply to thread"
        aria-label="Reply in Thread"
        onClick={(event) => openThread(message.thread_id, event.currentTarget)}
      >
        <MessageSquareReply aria-hidden="true" />
      </button>
      <button
        type="button"
        className="message-action-button"
        disabled={create.isPending}
        aria-disabled={taskUnavailableReason ? true : undefined}
        aria-label={taskUnavailableReason ?? "Create Task"}
        title={taskUnavailableReason ?? "Create Task"}
        onClick={() => {
          if (!taskUnavailableReason) create.mutate();
        }}
      >
        {create.isPending ? <LoaderCircle className="spin" aria-hidden="true" /> : <ListTodo aria-hidden="true" />}
      </button>
      {create.error ? <span className="message-action-error" role="alert">Task creation failed. Retry.</span> : null}
    </div>
  );
}

function MessageBody({ message, spaceSlug, members }: { message: Message; spaceSlug: string; members: Member[] }) {
  if (message.deleted_at) return <p>Message deleted</p>;
  if (message.content.type === "text") {
    const mentionedMemberIds = new Set(message.mentions);
    const mentionedNames = new Set(members.filter((member) => mentionedMemberIds.has(member.id)).map((member) => member.display_name.toLowerCase()));
    if (message.mention_all) mentionedNames.add("all");
    return <ExpandableMessageText messageId={message.id} body={message.content.body_markdown} mentionedNames={mentionedNames} />;
  }
  if (message.content.type === "channel_created") {
    return <p className="action-message"><Hash aria-hidden="true" /><strong>{message.author.display_name}</strong> Created channel {message.content.channel.available ? <Link to="/s/$spaceSlug/channels/$channelSlug" params={{ spaceSlug, channelSlug: message.content.channel.slug }}>#{message.content.channel.name}</Link> : <span>Unavailable channel</span>}</p>;
  }
  return <p className="action-message"><PixelIdentity name={message.content.agent.name} kind="agent" seed={message.content.agent.member_id} /><strong>{message.author.display_name}</strong> Created agent {message.content.agent.available ? <Link to="/s/$spaceSlug/agents/$agentId" params={{ spaceSlug, agentId: message.content.agent.member_id }}>{message.content.agent.name}</Link> : <span>Unavailable Agent</span>} <small>{message.content.agent.lifecycle}</small></p>;
}

export function ExpandableMessageText({ messageId, body, mentionedNames }: { messageId: string; body: string; mentionedNames: ReadonlySet<string> }) {
  const paragraph = useRef<HTMLDivElement>(null);
  const [expanded, setExpanded] = useState(false);
  const [overflowing, setOverflowing] = useState(false);
  const bodyId = `message-body-${messageId}`;

  useLayoutEffect(() => {
    const node = paragraph.current;
    if (!node || expanded) return;
    const measure = () => setOverflowing(node.scrollHeight > node.clientHeight + 1);
    measure();
    if (typeof ResizeObserver === "undefined") return;
    const observer = new ResizeObserver(measure);
    observer.observe(node);
    return () => observer.disconnect();
  }, [body, expanded]);

  return (
    <div className="message-body-wrap">
      <div ref={paragraph} id={bodyId} className={`message-body${expanded ? " message-body--expanded" : " message-body--collapsed"}`}>
        {renderMessageBody(body, mentionedNames)}
      </div>
      {overflowing || expanded ? (
        <button className="message-expand" type="button" aria-expanded={expanded} aria-controls={bodyId} onClick={() => setExpanded((value) => !value)}>
          {expanded ? "Show less" : "Show more"}
        </button>
      ) : null}
    </div>
  );
}

function AttentionFailureNotice({ message, members }: { message: Message; members: Member[] }) {
  if (message.attention_failures.length === 0) return null;
  if (message.attention_failures.length === 1) {
    const failure = message.attention_failures[0];
    const agentName = members.find((member) => member.id === failure.agent_member_id)?.display_name ?? "Agent";
    return (
      <div className="system-event" role="alert">
        <div className="system-event-heading">
          <span>Could not start <strong>{agentName}</strong>. {failure.retrying ? "Sumi will retry automatically." : "Retry is required."} <code>{failure.error_code}</code></span>
        </div>
      </div>
    );
  }
  return (
    <details className="system-event" role="alert">
      <summary className="system-event-heading"><span>{message.attention_failures.length} system errors · Show details</span></summary>
      <div className="system-event-details">
        {message.attention_failures.map((failure) => (
          <p key={failure.agent_member_id}>
            Could not start <strong>{members.find((member) => member.id === failure.agent_member_id)?.display_name ?? "Agent"}</strong>. {failure.retrying ? "Sumi will retry automatically." : "Retry is required."} <code>{failure.error_code}</code>
          </p>
        ))}
      </div>
    </details>
  );
}

function highlightMentions(
  body: string,
  mentionedNames: ReadonlySet<string>,
  keyPrefix: string,
): ReactNode[] {
  const nodes: ReactNode[] = [];
  const mentionPattern = /(^|\s)(@[\p{L}_]+)/giu;
  let cursor = 0;

  for (const match of body.matchAll(mentionPattern)) {
    const matchStart = match.index ?? 0;
    const token = match[2];
    const tokenStart = matchStart + match[1].length;
    nodes.push(body.slice(cursor, tokenStart));
    nodes.push(
      mentionedNames.has(token.slice(1).toLowerCase())
        ? <mark className="message-mention" key={`${keyPrefix}-mention-${tokenStart}`}>{token}</mark>
        : token,
    );
    cursor = tokenStart + token.length;
  }

  nodes.push(body.slice(cursor));
  return nodes;
}

type MessageBlock =
  | { kind: "heading"; level: number; line: string }
  | { kind: "list"; ordered: boolean; items: string[] }
  | { kind: "quote"; lines: string[] }
  | { kind: "code"; lines: string[] }
  | { kind: "hr" }
  | { kind: "paragraph"; lines: string[] };

const MAX_INLINE_DEPTH = 6;
const HEADING_TAGS = ["h1", "h2", "h3", "h4", "h5", "h6"] as const;

function renderMessageBody(body: string, mentionedHandles: ReadonlySet<string>): ReactNode[] {
  return splitMessageBlocks(body).map((block, index) =>
    renderMessageBlock(block, mentionedHandles, index),
  );
}

function splitMessageBlocks(body: string): MessageBlock[] {
  const blocks: MessageBlock[] = [];
  const lines = body.split("\n");
  let index = 0;
  while (index < lines.length) {
    const line = lines[index];
    const trimmed = line.trim();
    if (trimmed === "") {
      index += 1;
      continue;
    }
    if (trimmed.startsWith("```")) {
      const codeLines: string[] = [];
      index += 1;
      while (index < lines.length && !lines[index].trim().startsWith("```")) {
        codeLines.push(lines[index]);
        index += 1;
      }
      if (index < lines.length) index += 1;
      blocks.push({ kind: "code", lines: codeLines });
      continue;
    }
    const heading = trimmed.match(/^(#{1,6})\s+(.*)$/);
    if (heading) {
      blocks.push({ kind: "heading", level: heading[1].length, line: heading[2] });
      index += 1;
      continue;
    }
    if (/^(-{3,}|\*{3,}|_{3,})$/.test(trimmed)) {
      blocks.push({ kind: "hr" });
      index += 1;
      continue;
    }
    const ordered = /^\d+[.)]\s+/.test(trimmed);
    if (ordered || /^[-*+]\s+/.test(trimmed)) {
      const items: string[] = [];
      const itemPattern = ordered ? /^\s*\d+[.)]\s+/ : /^\s*[-*+]\s+/;
      while (index < lines.length && itemPattern.test(lines[index])) {
        items.push(lines[index].replace(itemPattern, ""));
        index += 1;
      }
      blocks.push({ kind: "list", ordered, items });
      continue;
    }
    if (trimmed.startsWith(">")) {
      const quoteLines: string[] = [];
      while (index < lines.length && lines[index].trim().startsWith(">")) {
        quoteLines.push(lines[index].replace(/^\s*>\s?/, ""));
        index += 1;
      }
      blocks.push({ kind: "quote", lines: quoteLines });
      continue;
    }
    const paragraph: string[] = [line];
    index += 1;
    while (
      index < lines.length &&
      lines[index].trim() !== "" &&
      !startsMessageBlock(lines[index])
    ) {
      paragraph.push(lines[index]);
      index += 1;
    }
    blocks.push({ kind: "paragraph", lines: paragraph });
  }
  return blocks;
}

function startsMessageBlock(line: string): boolean {
  const trimmed = line.trim();
  return (
    /^#{1,6}\s+/.test(trimmed) ||
    trimmed.startsWith("```") ||
    /^[-*+]\s+/.test(trimmed) ||
    /^\d+[.)]\s+/.test(trimmed) ||
    /^>\s?/.test(trimmed) ||
    /^(-{3,}|\*{3,}|_{3,})$/.test(trimmed)
  );
}

function renderMessageBlock(
  block: MessageBlock,
  mentionedHandles: ReadonlySet<string>,
  blockIndex: number,
): ReactNode {
  const key = `message-block-${blockIndex}`;
  switch (block.kind) {
    case "heading": {
      const Heading = HEADING_TAGS[Math.min(block.level, HEADING_TAGS.length) - 1];
      return (
        <Heading key={key} className="message-heading">
          {renderInline(block.line, mentionedHandles, `heading-${blockIndex}`)}
        </Heading>
      );
    }
    case "list":
      return block.ordered ? (
        <ol key={key}>
          {block.items.map((item, itemIndex) => (
            <li key={`${key}-${itemIndex}`}>
              {renderInline(item, mentionedHandles, `list-${blockIndex}-${itemIndex}`)}
            </li>
          ))}
        </ol>
      ) : (
        <ul key={key}>
          {block.items.map((item, itemIndex) => (
            <li key={`${key}-${itemIndex}`}>
              {renderInline(item, mentionedHandles, `list-${blockIndex}-${itemIndex}`)}
            </li>
          ))}
        </ul>
      );
    case "quote":
      return (
        <blockquote key={key}>
          {block.lines.map((line, lineIndex) => (
            <p key={`${key}-${lineIndex}`}>
              {renderInline(line, mentionedHandles, `quote-${blockIndex}-${lineIndex}`)}
            </p>
          ))}
        </blockquote>
      );
    case "code":
      return (
        <pre key={key}>
          <code>{block.lines.join("\n")}</code>
        </pre>
      );
    case "hr":
      return <hr key={key} />;
    case "paragraph":
      return (
        <p key={key}>
          {block.lines.map((line, lineIndex) =>
            lineIndex === 0
              ? renderInline(line, mentionedHandles, `paragraph-${blockIndex}`)
              : renderInline(`\n${line}`, mentionedHandles, `paragraph-${blockIndex}-${lineIndex}`),
          )}
        </p>
      );
  }
}

function renderInline(
  text: string,
  mentionedHandles: ReadonlySet<string>,
  keyPrefix: string,
  depth = 0,
): ReactNode[] {
  if (depth > MAX_INLINE_DEPTH) {
    return highlightMentions(text, mentionedHandles, keyPrefix);
  }
  const codeMatch = /`([^`\n]+)`/.exec(text);
  const boldMatch = /\*\*([^*\n]+)\*\*/.exec(text);
  const italicMatch = /(?<!\*)\*([^*\n]+)\*(?!\*)|(?<!_)_([^_\n]+)_(?!_)/.exec(text);
  const deleteMatch = /~~([^~\n]+)~~/.exec(text);
  const linkMatch = /\[([^\]\n]+)\]\(([^)\s]+)\)/.exec(text);
  const candidates = [
    { kind: "code" as const, match: codeMatch },
    { kind: "bold" as const, match: boldMatch },
    { kind: "italic" as const, match: italicMatch },
    { kind: "delete" as const, match: deleteMatch },
    { kind: "link" as const, match: linkMatch },
  ]
    .filter((candidate): candidate is { kind: typeof candidate.kind; match: RegExpExecArray } =>
      Boolean(candidate.match),
    )
    .sort((left, right) => (left.match.index ?? 0) - (right.match.index ?? 0));
  if (candidates.length === 0) {
    return highlightMentions(text, mentionedHandles, keyPrefix);
  }
  const first = candidates[0];
  const match = first.match;
  const matchStart = match.index ?? 0;
  const nodes: ReactNode[] = [];
  nodes.push(...highlightMentions(text.slice(0, matchStart), mentionedHandles, `${keyPrefix}-b`));
  const innerKey = `${keyPrefix}-t`;
  switch (first.kind) {
    case "code":
      nodes.push(
        <code key={innerKey} className="message-inline-code">
          {match[1]}
        </code>,
      );
      break;
    case "bold":
      nodes.push(
        <strong key={innerKey}>
          {renderInline(match[1], mentionedHandles, `${keyPrefix}-s`, depth + 1)}
        </strong>,
      );
      break;
    case "italic":
      nodes.push(
        <em key={innerKey}>
          {renderInline(match[1] ?? match[2], mentionedHandles, `${keyPrefix}-e`, depth + 1)}
        </em>,
      );
      break;
    case "delete":
      nodes.push(
        <del key={innerKey}>
          {renderInline(match[1], mentionedHandles, `${keyPrefix}-d`, depth + 1)}
        </del>,
      );
      break;
    case "link": {
      const href = safeMessageLink(match[2]);
      nodes.push(
        href ? (
          <a key={innerKey} href={href} rel="noreferrer" target="_blank">
            {renderInline(match[1], mentionedHandles, `${keyPrefix}-l`, depth + 1)}
          </a>
        ) : (
          <span key={innerKey}>{match[0]}</span>
        ),
      );
      break;
    }
  }
  nodes.push(
    ...renderInline(
      text.slice(matchStart + match[0].length),
      mentionedHandles,
      `${keyPrefix}-a`,
      depth + 1,
    ),
  );
  return nodes;
}

function safeMessageLink(url: string): string | null {
  const trimmed = url.trim();
  if (trimmed.startsWith("/") && !trimmed.startsWith("//")) {
    return trimmed;
  }
  try {
    const parsed = new URL(trimmed);
    if (parsed.protocol === "http:" || parsed.protocol === "https:") {
      return parsed.href;
    }
  } catch {
    return null;
  }
  return null;
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
