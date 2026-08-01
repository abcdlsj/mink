import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Link, useLocation, useNavigate, useParams } from "@tanstack/react-router";
import { Archive, Asterisk, Check, Hash, LoaderCircle, Menu, MessageCircle, Monitor, Plus, X } from "lucide-react";
import { type CSSProperties, type FormEvent, type KeyboardEvent, type PointerEvent as ReactPointerEvent, useCallback, useEffect, useRef, useState } from "react";

import {
  addChannelAgents,
  archiveChannel,
  createMessage,
  listAgents,
  listChannelMembers,
  listComputers,
  listMembers,
  listMessages,
  type Channel,
  type ChannelMembers,
  type Member,
  type Message,
  type MessagePage,
} from "../api/client";
import { DialogFrame } from "../components/DialogFrame";
import { MessageComposer, type ComposerInput } from "../components/channel/MessageComposer";
import { MessageTimeline } from "../components/channel/MessageTimeline";
import { ThreadPane } from "../components/channel/ThreadPane";
import { PixelIdentity, PresenceIdentity, SpaceShell } from "../components/SpaceShell";

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
            setup={channel.slug === "general" ? {
              canPairComputer: ["owner", "admin"].includes(currentMember.access_level),
              canCreateAgent:
                ["owner", "admin"].includes(currentMember.access_level) ||
                currentMember.permissions.includes("agent.create"),
            } : undefined}
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
  direct = false,
  canArchive = false,
  spaceSlug,
  setup,
}: {
  channel: Channel;
  spaceId: string;
  currentDisplayName: string;
  openNavigation: () => void;
  title: string;
  subtitle: string;
  placeholder: string;
  emptyTitle: string;
  direct?: boolean;
  canArchive?: boolean;
  spaceSlug: string;
  setup?: { canPairComputer: boolean; canCreateAgent: boolean };
}) {
  const queryClient = useQueryClient();
  const navigate = useNavigate();
  const location = useLocation();
  const [threadId, setThreadId] = useState<string>();
  const [threadOpenedAtMainSeq, setThreadOpenedAtMainSeq] = useState(0);
  const [threadPaneWidth, setThreadPaneWidth] = useState(360);
  const [threadPaneMaxWidth, setThreadPaneMaxWidth] = useState(480);
  const [threadPaneResizing, setThreadPaneResizing] = useState(false);
  const [agentPickerOpen, setAgentPickerOpen] = useState(false);
  const workspace = useRef<HTMLElement>(null);
  const timeline = useRef<HTMLDivElement>(null);
  const channelScrollPosition = useRef(0);
  const threadTrigger = useRef<HTMLButtonElement | null>(null);
  const threadResizeStart = useRef<{ pointerId: number; x: number; width: number } | undefined>(undefined);

  const clampThreadPaneWidth = useCallback((width: number) =>
    Math.min(threadPaneMaxWidth, Math.max(360, width)), [threadPaneMaxWidth]);

  useEffect(() => {
    const element = workspace.current;
    if (!element) return;
    const updateMaximum = () => {
      if (window.innerWidth < 900) return;
      const workspaceWidth = element.clientWidth || window.innerWidth;
      const maximum = Math.min(480, Math.max(360, workspaceWidth - 480));
      setThreadPaneMaxWidth(maximum);
      setThreadPaneWidth((current) => Math.min(current, maximum));
    };
    updateMaximum();
    const observer = typeof ResizeObserver === "undefined" ? undefined : new ResizeObserver(updateMaximum);
    observer?.observe(element);
    window.addEventListener("resize", updateMaximum);
    return () => {
      observer?.disconnect();
      window.removeEventListener("resize", updateMaximum);
    };
  }, []);

  useEffect(() => {
    if (!threadPaneResizing) return;
    function move(event: globalThis.PointerEvent) {
      const start = threadResizeStart.current;
      if (!start || event.pointerId !== start.pointerId) return;
      setThreadPaneWidth(clampThreadPaneWidth(start.width + start.x - event.clientX));
    }
    function finish(event: globalThis.PointerEvent) {
      if (event.pointerId !== threadResizeStart.current?.pointerId) return;
      threadResizeStart.current = undefined;
      setThreadPaneResizing(false);
    }
    window.addEventListener("pointermove", move);
    window.addEventListener("pointerup", finish);
    window.addEventListener("pointercancel", finish);
    return () => {
      window.removeEventListener("pointermove", move);
      window.removeEventListener("pointerup", finish);
      window.removeEventListener("pointercancel", finish);
    };
  }, [clampThreadPaneWidth, threadPaneResizing]);

  function startThreadPaneResize(event: ReactPointerEvent<HTMLDivElement>) {
    if (event.button !== 0) return;
    event.preventDefault();
    event.currentTarget.focus();
    threadResizeStart.current = { pointerId: event.pointerId, x: event.clientX, width: threadPaneWidth };
    setThreadPaneResizing(true);
  }

  function resizeThreadPaneWithKeyboard(event: KeyboardEvent<HTMLDivElement>) {
    const step = event.shiftKey ? 24 : 8;
    let nextWidth: number | undefined;
    if (event.key === "ArrowLeft") nextWidth = threadPaneWidth + step;
    if (event.key === "ArrowRight") nextWidth = threadPaneWidth - step;
    if (event.key === "Home") nextWidth = 360;
    if (event.key === "End") nextWidth = threadPaneMaxWidth;
    if (nextWidth === undefined) return;
    event.preventDefault();
    setThreadPaneWidth(clampThreadPaneWidth(nextWidth));
  }

  function openThread(nextThreadId: string, trigger?: HTMLButtonElement) {
    if (trigger) threadTrigger.current = trigger;
    channelScrollPosition.current = timeline.current?.scrollTop ?? 0;
    setThreadOpenedAtMainSeq(latestMainMessageSeq);
    setThreadId(nextThreadId);
  }

  const closeThread = useCallback(() => {
    setThreadId(undefined);
    window.requestAnimationFrame(() => {
      if (timeline.current) timeline.current.scrollTop = channelScrollPosition.current;
      threadTrigger.current?.focus();
    });
  }, []);

  const showLatestChannelMessages = useCallback(() => {
    document.getElementById("channel-heading")?.focus();
    setThreadId(undefined);
    window.requestAnimationFrame(() => {
      if (timeline.current) timeline.current.scrollTop = timeline.current.scrollHeight;
    });
  }, []);
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
  const agents = useQuery({
    queryKey: ["agents", spaceId],
    queryFn: () => listAgents(spaceId),
  });
  const computers = useQuery({
    queryKey: ["computers", spaceId],
    queryFn: () => listComputers(spaceId),
    enabled: Boolean(setup),
    retry: false,
  });
  const latestMainMessageSeq = Math.max(
    0,
    ...(messages.data?.messages.map((message) => message.seq) ?? []),
  );
  useEffect(() => {
    if (!messages.data || !location.hash.startsWith("message-")) return;
    window.requestAnimationFrame(() => {
      const target = document.getElementById(location.hash);
      target?.scrollIntoView({ block: "center" });
      target?.focus({ preventScroll: true });
    });
  }, [location.hash, messages.data]);
  const activityByMemberId = new Map(
    (agents.data ?? []).map((agent) => [agent.member_id, agent.activity_status] as const),
  );
  async function sendMessage(input: ComposerInput): Promise<Message> {
    return createMessage(channel.id, input);
  }

  function addMessage(message: Message) {
    queryClient.setQueryData<MessagePage>(["messages", channel.id], (current) => ({
      channel_id: channel.id,
      snapshot_channel_seq: message.seq,
      messages: [...(current?.messages ?? []), message],
      has_more_before: current?.has_more_before ?? false,
      has_more_after: false,
    }));
  }

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

  return (
    <section
      ref={workspace}
      className={`channel-workspace${threadId ? " channel-workspace--thread-open" : ""}${threadPaneResizing ? " channel-workspace--thread-resizing" : ""}`}
      style={threadId ? ({ "--thread-pane-width": `${threadPaneWidth}px` } as CSSProperties) : undefined}
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
        <span className="channel-header-glyph" aria-hidden="true">
          {direct ? <MessageCircle /> : <Hash />}
        </span>
        <div className="channel-title">
          <h1 id="channel-heading" tabIndex={-1} aria-label={title}>{title.replace(/^#/, "")}</h1>
          <p>{subtitle}</p>
        </div>
        <div className="member-strip" aria-label="Current Member">
          {(channelMembers.data?.members ?? []).slice(0, 4).map((member) => (
            <PresenceIdentity key={member.id} name={member.display_name} kind={member.kind} seed={member.id} activityStatus={activityByMemberId.get(member.id)} />
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
      <MessageTimeline
        timelineRef={timeline}
        header={setup && spaceSlug ? (
          <SetupStrip
            spaceSlug={spaceSlug}
            computers={computers.data}
            agents={agents.data}
            loading={computers.isPending || agents.isPending}
            unavailable={Boolean(computers.error || agents.error)}
            canPairComputer={setup.canPairComputer}
            canCreateAgent={setup.canCreateAgent}
          />
        ) : undefined}
        page={messages.data}
        pending={messages.isPending}
        error={messages.error}
        retry={() => void messages.refetch()}
        emptyTitle={emptyTitle}
        channelId={channel.id}
        spaceSlug={spaceSlug}
        openThread={openThread}
        activityByMemberId={activityByMemberId}
        members={channelMembers.data?.members ?? []}
      />
      <MessageComposer
        spaceId={spaceId}
        members={channelMembers.data?.members ?? []}
        placeholder={placeholder}
        ariaLabel="Message"
        attachmentAriaLabel="Choose Attachment"
        attachButtonLabel="Attach file"
        sendButtonLabel="Send message"
        attachmentsAriaLabel="Attachments ready to send"
        send={sendMessage}
        onSent={addMessage}
      />

      {threadId ? (
        <ThreadPane
          channelId={channel.id}
          spaceId={spaceId}
          threadId={threadId}
          channelSlug={channel.slug}
          spaceSlug={spaceSlug}
          members={channelMembers.data?.members ?? []}
          latestMainMessageSeq={latestMainMessageSeq}
          openedAtMainSeq={threadOpenedAtMainSeq}
          paneWidth={threadPaneWidth}
          paneMaxWidth={threadPaneMaxWidth}
          startResize={startThreadPaneResize}
          resizeWithKeyboard={resizeThreadPaneWithKeyboard}
          close={closeThread}
          showLatestChannelMessages={showLatestChannelMessages}
          activityByMemberId={activityByMemberId}
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
function SetupStrip({
  spaceSlug,
  computers,
  agents,
  loading,
  unavailable,
  canPairComputer,
  canCreateAgent,
}: {
  spaceSlug: string;
  computers?: Awaited<ReturnType<typeof listComputers>>;
  agents?: Awaited<ReturnType<typeof listAgents>>;
  loading: boolean;
  unavailable: boolean;
  canPairComputer: boolean;
  canCreateAgent: boolean;
}) {
  if (loading) {
    return <section className="setup-strip setup-strip--loading" aria-label="Loading Space setup">Preparing Space setup...</section>;
  }
  if (unavailable) {
    return (
      <section className="setup-strip setup-strip--error" aria-label="Space setup unavailable">
        <strong>Setup status unavailable</strong>
        <span>Messages still work. Check the Server connection before pairing a Computer.</span>
      </section>
    );
  }

  const hasComputer = Boolean(computers?.length);
  const hasOnlineComputer = Boolean(computers?.some((computer) => computer.status === "online"));
  const hasAgent = Boolean(agents?.some((agent) => agent.desired_lifecycle !== "retired"));
  if (hasComputer && hasAgent) return null;

  return (
    <section className="setup-strip" aria-labelledby="setup-strip-title">
      <div className="setup-strip-heading">
        <span>03 / 03</span>
        <strong id="setup-strip-title">Finish your Space setup</strong>
        <small>#general is ready. Add local compute, then your first Agent.</small>
      </div>
      <ol className="setup-steps">
        <li className={hasComputer ? "is-complete" : ""}>
          <span className="setup-step-icon" aria-hidden="true">{hasComputer ? <Check /> : <Monitor />}</span>
          <span><strong>Connect a Computer</strong><small>{hasComputer ? `${computers?.length} paired` : "macOS or Linux"}</small></span>
          {!hasComputer && canPairComputer ? (
            <Link className="setup-action" to="/s/$spaceSlug/computers" params={{ spaceSlug }} hash="pair-computer">Pair</Link>
          ) : null}
        </li>
        <li className={hasAgent ? "is-complete" : ""}>
          <span className="setup-step-icon setup-step-icon--agent" aria-hidden="true">{hasAgent ? <Check /> : <Asterisk />}</span>
          <span><strong>Create your first Agent</strong><small>{hasAgent ? `${agents?.filter((agent) => agent.desired_lifecycle !== "retired").length} created` : hasOnlineComputer ? "Choose a Role and Driver" : "Requires an online Computer"}</small></span>
          {!hasAgent && canCreateAgent && hasOnlineComputer ? (
            <Link className="setup-action" to="/s/$spaceSlug/computers" params={{ spaceSlug }} hash="create-agent">Create</Link>
          ) : null}
        </li>
      </ol>
    </section>
  );
}

function AddAgentsDialog({ agents, pending, error, close, submit }: { agents: Member[]; pending: boolean; error?: string; close: () => void; submit: (ids: string[]) => void }) {
  function handleSubmit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    submit(new FormData(event.currentTarget).getAll("agent_member_ids").map(String));
  }
  return (
    <DialogFrame className="channel-dialog channel-member-dialog" close={close} labelId="add-channel-agents-title">
        <header><div><p className="section-kicker">CHANNEL MEMBERS</p><h2 id="add-channel-agents-title">Add Agents</h2></div><button className="icon-button" type="button" aria-label="Close Add Agents" onClick={close}><X /></button></header>
        <form onSubmit={handleSubmit}>
          <fieldset className="channel-agent-picker">
            <legend>Available Agents</legend>
            {agents.length ? agents.map((agent, index) => <label key={agent.id}><input type="checkbox" name="agent_member_ids" value={agent.id} {...(index === 0 ? { "data-dialog-initial-focus": true } : {})} /><PixelIdentity name={agent.display_name} kind="agent" seed={agent.id} /><span><strong>{agent.display_name}</strong><small>@{agent.handle}</small></span></label>) : <p>Every active Agent is already in this Channel.</p>}
          </fieldset>
          {error ? <p className="form-error" role="alert">{error}</p> : null}
          <footer><button className="command-button" type="button" onClick={close}>Cancel</button><button className="command-button command-button--accent" type="submit" disabled={pending || agents.length === 0}>{pending ? "Adding…" : "Add selected"}</button></footer>
        </form>
    </DialogFrame>
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
