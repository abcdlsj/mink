import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useNavigate, useParams } from "@tanstack/react-router";
import { Archive, Bell, BellOff, Hash, LoaderCircle, Menu, MessageSquareReply, Paperclip, Plus, Send, X } from "lucide-react";
import { type ChangeEvent, type FormEvent, type KeyboardEvent, useRef, useState } from "react";

import {
  addChannelAgents,
  archiveChannel,
  createMessage,
  createThread,
  createThreadReply,
  listChannelMembers,
  listMembers,
  listMessages,
  readThread,
  setThreadSubscription,
  uploadAttachment,
  type Attachment,
  type Channel,
  type ChannelMembers,
  type Member,
  type MessagePage,
  type Message,
  type ThreadRead,
} from "../api/client";
import { PixelIdentity, SpaceShell } from "../components/SpaceShell";
import { formatBytes } from "../format";

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
  const [agentPickerOpen, setAgentPickerOpen] = useState(false);
  const fileInput = useRef<HTMLInputElement>(null);
  const messages = useQuery({
    queryKey: ["messages", channel.id],
    queryFn: () => listMessages(channel.id),
  });
  const spaceMembers = useQuery({
    queryKey: ["members", spaceId],
    queryFn: () => listMembers(spaceId),
  });
  const channelMembers = useQuery({
    queryKey: ["channel-members", channel.id],
    queryFn: () => listChannelMembers(channel.id),
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
  const addAgents = useMutation({
    mutationFn: (agentIds: string[]) => addChannelAgents(channel.id, agentIds),
    onSuccess: (result) => {
      queryClient.setQueryData<ChannelMembers>(["channel-members", channel.id], result);
      setAgentPickerOpen(false);
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
      mentions: mentionIds(trimmed, channelMembers.data?.members ?? []),
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
          {(channelMembers.data?.members ?? []).slice(0, 4).map((member) => (
            <PixelIdentity key={member.id} name={member.display_name} />
          ))}
          <span>{channelMembers.data ? `${channelMembers.data.members.length} Members` : currentDisplayName}</span>
        </div>
        {channelMembers.data?.can_manage ? (
          <button className="icon-button" type="button" aria-label="Add Agents to Channel" title="Add Agents to Channel" onClick={() => { addAgents.reset(); setAgentPickerOpen(true); }}><Plus /></button>
        ) : null}
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
          <div className="timeline-status timeline-status--error" role="alert">
            <span>{messages.error.message}</span>
            <button className="compact-action" type="button" onClick={() => void messages.refetch()}>Retry</button>
          </div>
        ) : null}
        {messages.data?.messages.length === 0 ? (
          <div className="empty-channel">
            <span className="channel-glyph" aria-hidden="true">
              <Hash />
            </span>
            <h2>{emptyTitle}</h2>
          </div>
        ) : null}
        {messages.data?.messages.map((message, index, all) => {
          const previous = index > 0 ? all[index - 1] : undefined;
          const showDivider = !previous || dayKey(previous.created_at) !== dayKey(message.created_at);
          // A day divider always restarts the visual grouping.
          const grouped = !showDivider && !startsNewGroup(message, previous);
          return (
            <div className="message-block" key={message.id}>
              {showDivider ? (
                <div className="day-divider" role="separator">
                  <time dateTime={message.created_at}>{formatDayDivider(message.created_at)}</time>
                </div>
              ) : null}
              <article className={`message-row${grouped ? " message-row--grouped" : ""}`}>
                {grouped ? (
                  <time className="message-gutter-time" dateTime={message.created_at}>
                    {formatMessageTime(message.created_at)}
                  </time>
                ) : (
                  <PixelIdentity name={message.author.display_name} />
                )}
                <div className="message-content">
                  {grouped ? null : (
                    <header>
                      <strong>{message.author.display_name}</strong>
                      {message.author.kind === "agent" ? <span className="agent-label">AGENT</span> : null}
                      <time dateTime={message.created_at}>{formatMessageTime(message.created_at)}</time>
                      <span className="message-seq">@{message.seq}</span>
                    </header>
                  )}
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
            </div>
          );
        })}
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
        <MentionInput
          ariaLabel="Message"
          className="composer-input"
          placeholder={placeholder}
          rows={1}
          value={body}
          members={channelMembers.data?.members ?? []}
          onChange={setBody}
        />
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
          channelSlug={channel.slug}
          members={channelMembers.data?.members ?? []}
          close={() => setThreadId(undefined)}
        />
      ) : null}
      {agentPickerOpen ? (
        <AddAgentsDialog
          agents={(spaceMembers.data ?? []).filter((member) => member.kind === "agent" && !channelMembers.data?.members.some((joined) => joined.id === member.id))}
          pending={addAgents.isPending}
          error={addAgents.error?.message}
          close={() => setAgentPickerOpen(false)}
          submit={(ids) => addAgents.mutate(ids)}
        />
      ) : null}
    </section>
  );
}

function ThreadPane({
  channelId,
  spaceId,
  threadId,
  channelSlug,
  members,
  close,
}: {
  channelId: string;
  spaceId: string;
  threadId: number;
  channelSlug: string;
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
    <aside className="thread-pane" aria-label={`Thread #${channelSlug}:${threadId}`}>
      <header className="thread-header">
        <div>
          <span>THREAD</span>
          <strong>#{channelSlug}:{threadId}</strong>
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
        <MentionInput
          ariaLabel="Thread reply"
          placeholder={`Reply to Thread #${threadId}`}
          rows={2}
          value={body}
          members={members}
          onChange={setBody}
        />
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

function MentionInput({ ariaLabel, className, placeholder, rows, value, members, onChange }: { ariaLabel: string; className?: string; placeholder: string; rows: number; value: string; members: Member[]; onChange: (value: string) => void }) {
  const textarea = useRef<HTMLTextAreaElement>(null);
  const [cursor, setCursor] = useState(0);
  const [activeIndex, setActiveIndex] = useState(0);
  const match = mentionMatch(value, cursor);
  const suggestions = match ? members.filter((member) => {
    const query = match.query.toLowerCase();
    return member.handle.toLowerCase().includes(query) || member.display_name.toLowerCase().includes(query);
  }).slice(0, 6) : [];

  function choose(member: Member) {
    if (!match) return;
    const inserted = `@${member.handle} `;
    const next = `${value.slice(0, match.start)}${inserted}${value.slice(cursor)}`;
    const nextCursor = match.start + inserted.length;
    onChange(next);
    setCursor(nextCursor);
    window.requestAnimationFrame(() => {
      textarea.current?.focus();
      textarea.current?.setSelectionRange(nextCursor, nextCursor);
    });
  }

  function handleKey(event: KeyboardEvent<HTMLTextAreaElement>) {
    if (event.key === "Enter" && (event.metaKey || event.ctrlKey)) {
      event.preventDefault();
      event.currentTarget.form?.requestSubmit();
      return;
    }
    if (!suggestions.length) return;
    if (event.key === "ArrowDown" || event.key === "ArrowUp") {
      event.preventDefault();
      const direction = event.key === "ArrowDown" ? 1 : -1;
      setActiveIndex((index) => (index + direction + suggestions.length) % suggestions.length);
    } else if (event.key === "Enter" || event.key === "Tab") {
      event.preventDefault();
      choose(suggestions[Math.min(activeIndex, suggestions.length - 1)]);
    } else if (event.key === "Escape") {
      setCursor(-1);
    }
  }

  return (
    <div className={`mention-input${className ? ` ${className}` : ""}`}>
      {suggestions.length ? (
        <div className="mention-suggestions" role="listbox" aria-label="Mention suggestions">
          {suggestions.map((member, index) => (
            <button key={member.id} type="button" role="option" aria-selected={index === activeIndex} onMouseDown={(event) => event.preventDefault()} onClick={() => choose(member)}>
              <PixelIdentity name={member.display_name} />
              <span><strong>{member.display_name}</strong><small>@{member.handle}</small></span>
              {member.kind === "agent" ? <span className="agent-label">AGENT</span> : null}
            </button>
          ))}
        </div>
      ) : null}
      <textarea
        ref={textarea}
        aria-label={ariaLabel}
        placeholder={placeholder}
        rows={rows}
        value={value}
        maxLength={20_000}
        onClick={(event) => setCursor(event.currentTarget.selectionStart)}
        onSelect={(event) => setCursor(event.currentTarget.selectionStart)}
        onChange={(event) => { onChange(event.target.value); setCursor(event.target.selectionStart); setActiveIndex(0); }}
        onKeyDown={handleKey}
      />
    </div>
  );
}

function mentionMatch(value: string, cursor: number): { start: number; query: string } | undefined {
  if (cursor < 0) return undefined;
  const prefix = value.slice(0, cursor);
  const match = prefix.match(/(?:^|\s)@([a-z0-9-]*)$/i);
  if (!match) return undefined;
  return { start: cursor - match[1].length - 1, query: match[1] };
}

function AddAgentsDialog({ agents, pending, error, close, submit }: { agents: Member[]; pending: boolean; error?: string; close: () => void; submit: (ids: string[]) => void }) {
  function handleSubmit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    submit(new FormData(event.currentTarget).getAll("agent_member_ids").map(String));
  }
  return (
    <div className="dialog-backdrop" role="presentation">
      <section className="channel-dialog channel-member-dialog" role="dialog" aria-modal="true" aria-labelledby="add-channel-agents-title">
        <header><div><p className="section-kicker">CHANNEL MEMBERS</p><h2 id="add-channel-agents-title">Add Agents</h2></div><button className="icon-button" type="button" aria-label="Close Add Agents" onClick={close}><X /></button></header>
        <form onSubmit={handleSubmit}>
          <fieldset className="channel-agent-picker">
            <legend>Available Agents</legend>
            {agents.length ? agents.map((agent) => <label key={agent.id}><input type="checkbox" name="agent_member_ids" value={agent.id} /><PixelIdentity name={agent.display_name} /><span><strong>{agent.display_name}</strong><small>@{agent.handle}</small></span></label>) : <p>Every active Agent is already in this Channel.</p>}
          </fieldset>
          {error ? <p className="form-error" role="alert">{error}</p> : null}
          <footer><button className="command-button" type="button" onClick={close}>Cancel</button><button className="command-button command-button--accent" type="submit" disabled={pending || agents.length === 0}>{pending ? "Adding…" : "Add selected"}</button></footer>
        </form>
      </section>
    </div>
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
