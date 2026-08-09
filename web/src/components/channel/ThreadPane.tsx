import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Link } from "@tanstack/react-router";
import { ArrowLeft, Bell, BellOff, X } from "lucide-react";
import { type KeyboardEvent, type PointerEvent as ReactPointerEvent, useEffect, useRef } from "react";

import {
  createThreadReply,
  readThread,
  setThreadSubscription,
  type Member,
  type Agent,
  type Message,
  type ThreadRead,
} from "../../api/client";
import { useLatestMessageScroll } from "../../hooks/useLatestMessageScroll";
import { MessageComposer, type ComposerInput } from "./MessageComposer";
import { CompactMessage } from "./MessageTimeline";
import { ToBottomButton } from "./ToBottomButton";

export function ThreadPane({
  channelId,
  spaceId,
  threadId,
  channelSlug,
  spaceSlug,
  members,
  latestMainMessageSeq,
  openedAtMainSeq,
  paneWidth,
  paneMaxWidth,
  startResize,
  resizeWithKeyboard,
  close,
  showLatestChannelMessages,
  activityByMemberId,
  direct = false,
  onOpenAgentDm,
}: {
  channelId: string;
  spaceId: string;
  threadId: string;
  channelSlug: string;
  spaceSlug: string;
  members: Member[];
  latestMainMessageSeq: number;
  openedAtMainSeq: number;
  paneWidth: number;
  paneMaxWidth: number;
  startResize: (event: ReactPointerEvent<HTMLDivElement>) => void;
  resizeWithKeyboard: (event: KeyboardEvent<HTMLDivElement>) => void;
  close: () => void;
  showLatestChannelMessages: () => void;
  activityByMemberId: ReadonlyMap<string, Agent["activity_status"]>;
  direct?: boolean;
  onOpenAgentDm?: (memberId: string) => void;
}) {
  const queryClient = useQueryClient();
  const closeButton = useRef<HTMLButtonElement>(null);
  const threadMessagesRef = useRef<HTMLDivElement>(null);
  const channelHasNewMessages = latestMainMessageSeq > openedAtMainSeq;
  const thread = useQuery({
    queryKey: ["thread", threadId],
    queryFn: () => readThread(threadId),
  });
  const latestThreadMessageId = thread.data?.replies.at(-1)?.id ?? thread.data?.root.id;
  const { showToBottom, scrollToBottom } = useLatestMessageScroll(
    threadMessagesRef,
    latestThreadMessageId,
  );
  const threadLabel = thread.data?.root.seq ?? threadId;
  const subscription = useMutation({
    mutationFn: (isFollowing: boolean) =>
      setThreadSubscription(threadId, isFollowing),
    onSuccess: (result) => {
      queryClient.setQueryData<ThreadRead>(["thread", threadId], (current) =>
        current ? { ...current, is_following: result.is_following } : current,
      );
    },
  });

  useEffect(() => {
    closeButton.current?.focus();
    function handleEscape(event: globalThis.KeyboardEvent) {
      if (event.key === "Escape") close();
    }
    window.addEventListener("keydown", handleEscape);
    return () => window.removeEventListener("keydown", handleEscape);
  }, [close]);

  async function sendReply(input: ComposerInput): Promise<Message> {
    if (!thread.data) throw new Error("Thread is unavailable");
    return createThreadReply(threadId, {
      ...input,
      reply_to_message_id: thread.data.root.id,
    });
  }

  function addReply(message: Message) {
    queryClient.setQueryData<ThreadRead>(["thread", threadId], (current) =>
      current ? { ...current, snapshot_channel_seq: message.seq, replies: [...current.replies, message] } : current,
    );
    // A reply sent from this composer is an explicit navigation action. It
    // must reveal the new reply even when the reader was browsing older ones;
    // the scroll hook still applies the 3/4-screen rule to external updates.
    scrollToBottom();
    window.requestAnimationFrame(() => scrollToBottom());
    void queryClient.invalidateQueries({ queryKey: ["messages", channelId] });
  }

  return (
    <aside className="thread-pane" aria-label={`Thread #${channelSlug}:${threadLabel}`}>
      <div
        className="thread-resize-handle"
        role="separator"
        aria-label="Resize Thread pane"
        aria-orientation="vertical"
        aria-valuemin={360}
        aria-valuemax={paneMaxWidth}
        aria-valuenow={paneWidth}
        tabIndex={0}
        onPointerDown={startResize}
        onKeyDown={resizeWithKeyboard}
      />
      <header className="thread-header">
        <div>
          <span>THREAD</span>
          <strong>#{channelSlug}:{threadLabel}{channelHasNewMessages ? <i className="thread-unread-dot" aria-label="New messages" /> : null}</strong>
        </div>
        {thread.data?.task && thread.data.task_relation ? (
          <Link className="thread-task-context" to="/s/$spaceSlug/tasks/$taskId" params={{ spaceSlug, taskId: thread.data.task.id }} aria-label={`${thread.data.task.title} ${thread.data.task_relation.toUpperCase()}`}>
            <strong>!{thread.data.task.seq} {thread.data.task.title}</strong>
            <span>{thread.data.task_relation.toUpperCase()}</span>
          </Link>
        ) : null}
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
        <button ref={closeButton} className="thread-close icon-button" type="button" aria-label="Close Thread" title="Close Thread" onClick={close}>
          <ArrowLeft className="thread-close-back" aria-hidden="true" />
          <X className="thread-close-x" aria-hidden="true" />
          <span>Channel</span>
        </button>
      </header>
      <div className="thread-messages-shell">
        <div ref={threadMessagesRef} className="thread-messages">
          {channelHasNewMessages ? (
            <button className="thread-context-update" type="button" onClick={showLatestChannelMessages}>
              <span>New messages in #{channelSlug}</span>
              <strong>Return to latest</strong>
            </button>
          ) : null}
          {thread.isPending ? <div className="timeline-status">Loading Thread...</div> : null}
          {thread.error ? <div className="timeline-status timeline-status--error">{thread.error.message}</div> : null}
          {thread.data ? (
            <>
              <p className="thread-section-label">ROOT</p>
              <CompactMessage message={thread.data.root} activityStatus={activityByMemberId.get(thread.data.root.author.id)} spaceSlug={spaceSlug} members={members} direct={direct} onOpenAgentDm={onOpenAgentDm} />
              <p className="thread-section-label">{thread.data.replies.length} REPLIES</p>
              {thread.data.replies.map((message) => <CompactMessage key={message.id} message={message} activityStatus={activityByMemberId.get(message.author.id)} spaceSlug={spaceSlug} members={members} direct={direct} onOpenAgentDm={onOpenAgentDm} />)}
            </>
          ) : null}
        </div>
        {showToBottom ? <ToBottomButton onClick={scrollToBottom} /> : null}
      </div>
      <MessageComposer
        key={threadId}
        className="thread-composer"
        spaceId={spaceId}
        draftKey={threadId}
        members={members}
        placeholder={`Reply to #${channelSlug}`}
        ariaLabel="Thread reply"
        attachmentAriaLabel="Choose Thread Attachment"
        attachButtonLabel="Attach file to Thread"
        sendButtonLabel="Send Thread reply"
        attachmentsAriaLabel="Thread Attachments ready to send"
        send={sendReply}
        onSent={addReply}
      />
    </aside>
  );
}
