import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useNavigate, useParams } from "@tanstack/react-router";
import { Archive, Bell, BellOff, Hash, LoaderCircle, Menu, MessageSquareReply, Paperclip, Send, X } from "lucide-react";
import { type ChangeEvent, type FormEvent, useRef, useState } from "react";

import {
  archiveChannel,
  createMessage,
  createThread,
  createThreadReply,
  listMembers,
  listMessages,
  readThread,
  setThreadSubscription,
  uploadAttachment,
  type Attachment,
  type Channel,
  type Member,
  type MessagePage,
  type Message,
  type ThreadRead,
} from "../api/client";
import { PixelIdentity, SpaceShell } from "../components/SpaceShell";

export function ChannelPage() {
  const { spaceSlug, channelSlug } = useParams({
    from: "/s/$spaceSlug/channels/$channelSlug",
  });
  return (
    <SpaceShell spaceSlug={spaceSlug} active="channel">
      {({ user, space, channels, currentMember, openNavigation }) => {
        const channel = channels.find(
          (candidate) => candidate.slug === channelSlug && candidate.joined,
        );
        if (!channel) {
          return <UnavailableChannel channelSlug={channelSlug} openNavigation={openNavigation} />;
        }
        return (
          <MessageWorkspace
            key={channel.id}
            channel={channel}
            spaceId={space.id}
            currentDisplayName={user.display_name}
            openNavigation={openNavigation}
            title={`#${channel.slug}`}
            subtitle={channel.topic ?? (channel.kind === "private" ? "Private Channel" : "Public Channel")}
            placeholder={`Message #${channel.slug}`}
            emptyTitle={`#${channel.slug} starts here.`}
            canArchive={
              channel.slug !== "general" &&
              (channel.created_by_member_id === space.current_member_id ||
                ["owner", "admin"].includes(currentMember.access_level))
            }
            spaceSlug={space.slug}
          />
        );
      }}
    </SpaceShell>
  );
}

export function MessageWorkspace({
  channel,
  spaceId,
  currentDisplayName,
  openNavigation,
  title,
  subtitle,
  placeholder,
  emptyTitle,
  canArchive = false,
  spaceSlug,
}: {
  channel: Channel;
  spaceId: string;
  currentDisplayName: string;
  openNavigation: () => void;
  title: string;
  subtitle: string;
  placeholder: string;
  emptyTitle: string;
  canArchive?: boolean;
  spaceSlug?: string;
}) {
  const queryClient = useQueryClient();
  const navigate = useNavigate();
  const [body, setBody] = useState("");
  const [attachments, setAttachments] = useState<Attachment[]>([]);
  const [threadId, setThreadId] = useState<number>();
  const fileInput = useRef<HTMLInputElement>(null);
  const messages = useQuery({
    queryKey: ["messages", channel.id],
    queryFn: () => listMessages(channel.id),
  });
  const members = useQuery({
    queryKey: ["members", spaceId],
    queryFn: () => listMembers(spaceId),
  });
  const send = useMutation({
    mutationFn: (input: Parameters<typeof createMessage>[1]) => createMessage(channel.id, input),
    onSuccess: (message) => {
      queryClient.setQueryData<MessagePage>(["messages", channel.id], (current) => ({
        channel_id: channel.id,
        snapshot_channel_seq: message.seq,
        messages: [...(current?.messages ?? []), message],
        has_more_before: current?.has_more_before ?? false,
        has_more_after: false,
      }));
      setBody("");
      setAttachments([]);
    },
  });
  const threadCreation = useMutation({
    mutationFn: (rootMessageId: string) => createThread(channel.id, rootMessageId),
    onSuccess: (thread) => {
      setThreadId(thread.thread_id);
      void queryClient.invalidateQueries({ queryKey: ["messages", channel.id] });
    },
  });
  const archive = useMutation({
    mutationFn: () => archiveChannel(channel.id),
    onSuccess: () => {
      void queryClient.invalidateQueries({ queryKey: ["channels", spaceId] });
      if (spaceSlug) {
        void navigate({
          to: "/s/$spaceSlug/channels/$channelSlug",
          params: { spaceSlug, channelSlug: "general" },
        });
      }
    },
  });
  const upload = useMutation({
    mutationFn: (file: File) => uploadAttachment(spaceId, file),
    onSuccess: (attachment) => setAttachments((current) => [...current, attachment]),
  });
  function submit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    const trimmed = body.trim();
    if (!trimmed) return;
    send.mutate({
      body_markdown: trimmed,
      mentions: mentionIds(trimmed, members.data ?? []),
      attachment_ids: attachments.map((attachment) => attachment.id),
    });
  }

  function selectFile(event: ChangeEvent<HTMLInputElement>) {
    const file = event.target.files?.[0];
    if (file) upload.mutate(file);
    event.target.value = "";
  }

  return (
    <section
      className={`channel-workspace${threadId ? " channel-workspace--thread-open" : ""}`}
      aria-labelledby="channel-heading"
    >
      <header className="channel-header">
        <button
          className="mobile-menu icon-button"
          type="button"
          aria-label="Open navigation"
          title="Open navigation"
          onClick={openNavigation}
        >
          <Menu />
        </button>
        <div className="channel-title">
          <h1 id="channel-heading">{title}</h1>
          <p>{subtitle}</p>
        </div>
        <div className="member-strip" aria-label="Current Member">
          <PixelIdentity name={currentDisplayName} />
          <span>{currentDisplayName}</span>
        </div>
        <button className="icon-button" type="button" aria-label="Notification settings" disabled>
          <Bell />
        </button>
        {canArchive ? (
          <button
            className="icon-button"
            type="button"
            aria-label="Archive Channel"
            title="Archive Channel"
            disabled={archive.isPending}
            onClick={() => archive.mutate()}
          >
            {archive.isPending ? <LoaderCircle className="spin" /> : <Archive />}
          </button>
        ) : null}
      </header>

      <div className="message-timeline" aria-live="polite">
        {messages.isPending ? <div className="timeline-status">Loading Messages...</div> : null}
        {messages.error ? (
          <div className="timeline-status timeline-status--error">{messages.error.message}</div>
        ) : null}
        {messages.data?.messages.length === 0 ? (
          <div className="empty-channel">
            <span className="channel-glyph" aria-hidden="true">
              <Hash />
            </span>
            <h2>{emptyTitle}</h2>
          </div>
        ) : null}
        {messages.data?.messages.map((message) => (
          <article className="message-row" key={message.id}>
            <PixelIdentity name={message.author.display_name} />
            <div className="message-content">
              <header>
                <strong>{message.author.display_name}</strong>
                {message.author.kind === "agent" ? <span className="agent-label">AGENT</span> : null}
                <time dateTime={message.created_at}>{formatMessageTime(message.created_at)}</time>
                <span className="message-seq">@{message.seq}</span>
              </header>
              <p>{message.deleted_at ? "Message 已删除" : message.body_markdown}</p>
              {!message.deleted_at && message.attachments?.length ? (
                <AttachmentList attachments={message.attachments} />
              ) : null}
              {!message.deleted_at ? (
                <button
                  className="thread-action"
                  type="button"
                  disabled={threadCreation.isPending}
                  onClick={() => {
                    if (message.thread_id) setThreadId(message.thread_id);
                    else threadCreation.mutate(message.id);
                  }}
                >
                  <MessageSquareReply aria-hidden="true" />
                  {message.reply_count > 0
                    ? `${message.reply_count} ${message.reply_count === 1 ? "reply" : "replies"}`
                    : "Reply in Thread"}
                </button>
              ) : null}
            </div>
          </article>
        ))}
      </div>

      <form className="composer" onSubmit={submit}>
        <input
          ref={fileInput}
          className="visually-hidden"
          type="file"
          aria-label="Choose Attachment"
          onChange={selectFile}
        />
        <button
          className="icon-button"
          type="button"
          aria-label="Attach file"
          title="Attach file"
          disabled={upload.isPending}
          onClick={() => fileInput.current?.click()}
        >
          {upload.isPending ? <LoaderCircle className="spin" /> : <Paperclip />}
        </button>
        <label className="composer-input">
          <span className="visually-hidden">Message</span>
          <textarea
            placeholder={placeholder}
            rows={1}
            value={body}
            maxLength={20_000}
            onChange={(event) => setBody(event.target.value)}
          />
        </label>
        <button
          className="send-button"
          type="submit"
          aria-label="Send message"
          title="Send message"
          disabled={send.isPending || upload.isPending || !body.trim()}
        >
          {send.isPending ? <LoaderCircle className="spin" /> : <Send />}
        </button>
        {attachments.length ? (
          <div className="composer-attachments" aria-label="Attachments ready to send">
            {attachments.map((attachment) => (
              <span key={attachment.id}>
                <Paperclip aria-hidden="true" />
                {attachment.original_name}
                <button
                  type="button"
                  aria-label={`Remove ${attachment.original_name}`}
                  onClick={() =>
                    setAttachments((current) => current.filter((item) => item.id !== attachment.id))
                  }
                >
                  <X aria-hidden="true" />
                </button>
              </span>
            ))}
          </div>
        ) : null}
        {send.error || upload.error ? (
          <p className="composer-error" role="alert">
            {send.error?.message ?? upload.error?.message}
          </p>
        ) : null}
      </form>
      {threadId ? (
        <ThreadPane
          channelId={channel.id}
          spaceId={spaceId}
          threadId={threadId}
          members={members.data ?? []}
          close={() => setThreadId(undefined)}
        />
      ) : null}
    </section>
  );
}

function ThreadPane({
  channelId,
  spaceId,
  threadId,
  members,
  close,
}: {
  channelId: string;
  spaceId: string;
  threadId: number;
  members: Member[];
  close: () => void;
}) {
  const queryClient = useQueryClient();
  const [body, setBody] = useState("");
  const [attachments, setAttachments] = useState<Attachment[]>([]);
  const fileInput = useRef<HTMLInputElement>(null);
  const thread = useQuery({
    queryKey: ["thread", channelId, threadId],
    queryFn: () => readThread(channelId, threadId),
  });
  const reply = useMutation({
    mutationFn: (input: Parameters<typeof createThreadReply>[2]) =>
      createThreadReply(channelId, threadId, input),
    onSuccess: (message) => {
      queryClient.setQueryData<ThreadRead>(["thread", channelId, threadId], (current) =>
        current ? { ...current, snapshot_channel_seq: message.seq, replies: [...current.replies, message] } : current,
      );
      void queryClient.invalidateQueries({ queryKey: ["messages", channelId] });
      setBody("");
      setAttachments([]);
    },
  });
  const upload = useMutation({
    mutationFn: (file: File) => uploadAttachment(spaceId, file),
    onSuccess: (attachment) => setAttachments((current) => [...current, attachment]),
  });
  const subscription = useMutation({
    mutationFn: (isFollowing: boolean) =>
      setThreadSubscription(channelId, threadId, isFollowing),
    onSuccess: (result) => {
      queryClient.setQueryData<ThreadRead>(["thread", channelId, threadId], (current) =>
        current ? { ...current, is_following: result.is_following } : current,
      );
    },
  });

  function submit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    const trimmed = body.trim();
    if (!trimmed || !thread.data) return;
    reply.mutate({
      body_markdown: trimmed,
      mentions: mentionIds(trimmed, members),
      attachment_ids: attachments.map((attachment) => attachment.id),
      reply_to_message_id: thread.data.root.id,
    });
  }

  return (
    <aside className="thread-pane" aria-label={`Thread ${threadId}`}>
      <header className="thread-header">
        <div>
          <span>THREAD</span>
          <strong>#{threadId}</strong>
        </div>
        {thread.data ? (
          <button
            className="icon-button"
            type="button"
            aria-label={thread.data.is_following ? "Unfollow Thread" : "Follow Thread"}
            title={thread.data.is_following ? "Unfollow Thread" : "Follow Thread"}
            disabled={subscription.isPending}
            onClick={() => subscription.mutate(!thread.data.is_following)}
          >
            {thread.data.is_following ? <BellOff /> : <Bell />}
          </button>
        ) : null}
        <button className="icon-button" type="button" aria-label="Close Thread" title="Close Thread" onClick={close}>
          <X />
        </button>
      </header>
      <div className="thread-messages">
        {thread.isPending ? <div className="timeline-status">Loading Thread...</div> : null}
        {thread.error ? <div className="timeline-status timeline-status--error">{thread.error.message}</div> : null}
        {thread.data ? (
          <>
            <p className="thread-section-label">ROOT</p>
            <CompactMessage message={thread.data.root} />
            <p className="thread-section-label">{thread.data.replies.length} REPLIES</p>
            {thread.data.replies.map((message) => <CompactMessage key={message.id} message={message} />)}
          </>
        ) : null}
      </div>
      <form className="thread-composer" onSubmit={submit}>
        <input
          ref={fileInput}
          className="visually-hidden"
          type="file"
          aria-label="Choose Thread Attachment"
          onChange={(event) => {
            const file = event.target.files?.[0];
            if (file) upload.mutate(file);
            event.target.value = "";
          }}
        />
        <button
          className="icon-button"
          type="button"
          aria-label="Attach file to Thread"
          disabled={upload.isPending}
          onClick={() => fileInput.current?.click()}
        >
          {upload.isPending ? <LoaderCircle className="spin" /> : <Paperclip />}
        </button>
        <label>
          <span className="visually-hidden">Thread reply</span>
          <textarea
            rows={2}
            value={body}
            maxLength={20_000}
            placeholder={`Reply to Thread #${threadId}`}
            onChange={(event) => setBody(event.target.value)}
          />
        </label>
        {attachments.length ? (
          <div className="composer-attachments" aria-label="Thread Attachments ready to send">
            {attachments.map((attachment) => <span key={attachment.id}>{attachment.original_name}</span>)}
          </div>
        ) : null}
        <button className="send-button" type="submit" aria-label="Send Thread reply" disabled={reply.isPending || upload.isPending || !body.trim()}>
          {reply.isPending ? <LoaderCircle className="spin" /> : <Send />}
        </button>
        {reply.error || upload.error ? <p className="composer-error" role="alert">{reply.error?.message ?? upload.error?.message}</p> : null}
      </form>
    </aside>
  );
}

function CompactMessage({ message }: { message: Message }) {
  return (
    <article className="thread-message">
      <PixelIdentity name={message.author.display_name} />
      <div>
        <header>
          <strong>{message.author.display_name}</strong>
          {message.author.kind === "agent" ? <span className="agent-label">AGENT</span> : null}
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

function AttachmentList({ attachments }: { attachments: Attachment[] }) {
  return (
    <div className="message-attachments">
      {attachments.map((attachment) => (
        <a key={attachment.id} href={attachment.download_path} download={attachment.original_name}>
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

function UnavailableChannel({
  channelSlug,
  openNavigation,
}: {
  channelSlug: string;
  openNavigation: () => void;
}) {
  return (
    <section className="channel-workspace">
      <header className="channel-header">
        <button
          className="mobile-menu icon-button"
          type="button"
          aria-label="Open navigation"
          title="Open navigation"
          onClick={openNavigation}
        >
          <Menu />
        </button>
        <div className="channel-title">
          <h1>Channel unavailable</h1>
          <p>Join a public Channel from Discover or request private access.</p>
        </div>
      </header>
      <div className="empty-channel">
        <span className="channel-glyph" aria-hidden="true">
          <Hash />
        </span>
        <h2>#{channelSlug} is not available.</h2>
      </div>
    </section>
  );
}

function mentionIds(body: string, members: Member[]): string[] {
  const byHandle = new Map(members.map((member) => [member.handle.toLowerCase(), member.id]));
  const ids = new Set<string>();
  for (const match of body.matchAll(/(^|\s)@([a-z0-9]+(?:-[a-z0-9]+)*)/gi)) {
    const id = byHandle.get(match[2].toLowerCase());
    if (id) ids.add(id);
  }
  return [...ids];
}

function formatMessageTime(value: string): string {
  return new Intl.DateTimeFormat(undefined, { hour: "2-digit", minute: "2-digit" }).format(
    new Date(value),
  );
}

function formatBytes(bytes: number): string {
  if (bytes < 1024) return `${bytes} B`;
  if (bytes < 1024 * 1024) return `${(bytes / 1024).toFixed(1)} KiB`;
  return `${(bytes / (1024 * 1024)).toFixed(1)} MiB`;
}
