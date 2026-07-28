import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
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
import { MessageComposer, type ComposerInput } from "./MessageComposer";
import { CompactMessage } from "./MessageTimeline";

export function ThreadPane({
  channelId,
  spaceId,
  threadId,
  channelSlug,
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
}: {
  channelId: string;
  spaceId: string;
  threadId: number;
  channelSlug: string;
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
}) {
  const queryClient = useQueryClient();
  const closeButton = useRef<HTMLButtonElement>(null);
  const channelHasNewMessages = latestMainMessageSeq > openedAtMainSeq;
  const thread = useQuery({
    queryKey: ["thread", channelId, threadId],
    queryFn: () => readThread(channelId, threadId),
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
    return createThreadReply(channelId, threadId, {
      ...input,
      reply_to_message_id: thread.data.root.id,
    });
  }

  function addReply(message: Message) {
    queryClient.setQueryData<ThreadRead>(["thread", channelId, threadId], (current) =>
      current ? { ...current, snapshot_channel_seq: message.seq, replies: [...current.replies, message] } : current,
    );
    void queryClient.invalidateQueries({ queryKey: ["messages", channelId] });
  }

  return (
    <aside className="thread-pane" aria-label={`Thread #${channelSlug}:${threadId}`}>
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
        <button ref={closeButton} className="thread-close icon-button" type="button" aria-label="Close Thread" title="Close Thread" onClick={close}>
          <ArrowLeft className="thread-close-back" aria-hidden="true" />
          <X className="thread-close-x" aria-hidden="true" />
          <span>Channel</span>
        </button>
      </header>
      <div className="thread-messages">
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
            <CompactMessage message={thread.data.root} activityStatus={activityByMemberId.get(thread.data.root.author.id)} />
            <p className="thread-section-label">{thread.data.replies.length} REPLIES</p>
            {thread.data.replies.map((message) => <CompactMessage key={message.id} message={message} activityStatus={activityByMemberId.get(message.author.id)} />)}
          </>
        ) : null}
      </div>
      <MessageComposer
        className="thread-composer"
        spaceId={spaceId}
        members={members}
        placeholder={`Reply to Thread #${threadId}`}
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
