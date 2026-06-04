import { useEffect, useMemo, useRef, useState } from "react";
import { Hash, MessageSquare, AtSign, Square, Settings, Plus } from "lucide-react";
import { useStore } from "@/lib/store";
import { Button } from "@/components/ui/button";
import { Identicon } from "@/components/Identicon";
import { EventBlock } from "@/components/EventBlock";
import { Markdown } from "@/components/Markdown";
import { renderMentions } from "@/components/Mention";
import { Dot } from "./LeftPane";
import { cn, relTime } from "@/lib/utils";

export function CenterPane() {
  const view = useStore((s) => s.view);
  const detail = useStore((s) => s.detail);
  const channels = useStore((s) => s.channels);
  const agents = useStore((s) => s.agents);
  const agentDMs = useStore((s) => s.agentDMs);
  const activeChannel = useStore((s) => s.activeChannel);
  const activeAgent = useStore((s) => s.activeAgent);
  const activeThread = useStore((s) => s.activeThread);
  const updateAgentChatTitle = useStore((s) => s.updateAgentChatTitle);

  const scrollRef = useRef<HTMLDivElement | null>(null);
  const lastScopeRef = useRef<string>("");
  const [editingTitle, setEditingTitle] = useState(false);
  const [titleDraft, setTitleDraft] = useState("");
  const [titleBusy, setTitleBusy] = useState(false);
  const [titleErr, setTitleErr] = useState<string | null>(null);

  const messageCount = detail?.messages.length ?? 0;
  const scope = `${view}:${activeChannel || ""}:${activeThread || ""}:${activeAgent || ""}`;

  useEffect(() => {
    const el = scrollRef.current;
    if (!el) return;
    if (lastScopeRef.current !== scope) {
      el.scrollTop = el.scrollHeight;
      lastScopeRef.current = scope;
      return;
    }
    const distanceFromBottom = el.scrollHeight - el.scrollTop - el.clientHeight;
    if (distanceFromBottom < 120) {
      el.scrollTop = el.scrollHeight;
    }
  }, [scope, messageCount]);

  useEffect(() => {
    setEditingTitle(false);
    setTitleDraft("");
    setTitleBusy(false);
    setTitleErr(null);
  }, [scope]);

  const threadDetail = useStore((s) => s.threadDetail);
  if (threadDetail) {
    return <ThreadView />;
  }

  if (!detail) {
    return (
      <main className="h-full min-w-0 grid grid-rows-[auto_1fr_auto] bg-panel">
        <div className="border-b-hard border-border px-5 py-4" />
        <div className="overflow-y-auto px-5 py-6 text-[12.5px] text-text-muted">
          Pick a channel, agent, or thread to start.
        </div>
        <Composer />
      </main>
    );
  }

  const item = detail.item;
  const channel = channels.find((c) => c.id === activeChannel);
  let titleText = item.title;
  let metaText = "";
  let TitleIcon = MessageSquare;
  let listeningHint = "";
  if (view === "channel") {
    TitleIcon = Hash;
    titleText = channel?.name || "channel";
    metaText = item.running ? "agents running" : "";
    listeningHint = listeningSummary(channel, agents);
  } else if (view === "thread") {
    TitleIcon = MessageSquare;
    metaText = channel ? `in #${channel.name}` : "";
  } else if (view === "agent") {
    TitleIcon = AtSign;
    titleText = titleText.replace(/^@/, "");
    metaText = detail.summary || "";
  }

  const editableAgentChat = view === "agent" && !!activeAgent && agentDMs.some((dm) => dm.id === activeAgent);
  const beginTitleEdit = () => {
    if (!editableAgentChat) return;
    setTitleDraft(titleText === "New chat" ? "" : titleText);
    setTitleErr(null);
    setEditingTitle(true);
  };
  const submitTitleEdit = async () => {
    if (!editableAgentChat || !activeAgent || titleBusy) return;
    const next = titleDraft.trim();
    if (!next) {
      setTitleErr("Title is required.");
      return;
    }
    if (next === titleText) {
      setEditingTitle(false);
      return;
    }
    setTitleBusy(true);
    setTitleErr(null);
    try {
      await updateAgentChatTitle(activeAgent, next);
      setEditingTitle(false);
    } catch (e) {
      setTitleErr(e instanceof Error ? e.message : String(e));
    } finally {
      setTitleBusy(false);
    }
  };
  const cancelTitleEdit = () => {
    setEditingTitle(false);
    setTitleDraft("");
    setTitleErr(null);
  };
  const showStop = item.running && view === "thread";

  return (
    <main className="h-full min-w-0 grid grid-rows-[auto_1fr_auto] bg-panel">
      <div className="flex items-end justify-between border-b-hard border-border bg-panel px-5 pb-3.5 pt-4">
        <div>
          <h2 className="flex items-center gap-2 font-display text-[18px] font-black text-text">
            <span className="inline-flex size-7 items-center justify-center border-2 border-border bg-accent">
              <TitleIcon className="size-[17px] text-text" />
            </span>
            {editableAgentChat && editingTitle ? (
              <span className="inline-flex min-w-[220px] flex-col gap-1">
                <input
                  value={titleDraft}
                  onChange={(e) => {
                    setTitleDraft(e.target.value);
                    if (titleErr) setTitleErr(null);
                  }}
                  onBlur={() => void submitTitleEdit()}
                  onKeyDown={(e) => {
                    if (e.key === "Enter") {
                      e.preventDefault();
                      void submitTitleEdit();
                    } else if (e.key === "Escape") {
                      e.preventDefault();
                      cancelTitleEdit();
                    }
                  }}
                  disabled={titleBusy}
                  autoFocus
                  className="h-8 border-hard border-border bg-bg px-2 font-display text-[18px] font-black text-text outline-none shadow-card disabled:opacity-70"
                />
                {titleErr && <span className="font-mono text-[10.5px] font-medium text-error">{titleErr}</span>}
              </span>
            ) : editableAgentChat ? (
              <button
                type="button"
                onClick={beginTitleEdit}
                className="min-w-0 truncate border border-transparent px-1 text-left hover:border-border hover:bg-bg"
                title="Click to rename"
              >
                {titleText}
              </button>
            ) : (
              <span>{titleText}</span>
            )}
            {view === "channel" && channel && (
              <AgentGear scope={{ kind: "channel", channel }} agents={agents} />
            )}
          </h2>
          {(metaText || listeningHint) && (
            <div className="mt-1 font-mono text-[11.5px] text-text-muted">
              {metaText}
              {metaText && listeningHint && " · "}
              {listeningHint}
            </div>
          )}
        </div>
        {showStop && (
          <Button variant="danger" size="sm" onClick={() => void useStore.getState().stop()}>
            <Square className="size-3" />
            <span>Stop run</span>
          </Button>
        )}
      </div>

      <div ref={scrollRef} className="overflow-y-auto px-5 pb-5 pt-5">
        <div className="mx-auto max-w-[880px]">
          {(() => {
            const visible = detail.messages.filter(renderableMessage);
            if (visible.length === 0) return <EmptyState />;
            return visible.map((m, i) => {
              const prev = visible[i - 1];
              const sameAuthor =
                prev && prev.role === m.role && (prev.author_id || "") === (m.author_id || "");
              const close =
                prev && new Date(m.time).getTime() - new Date(prev.time).getTime() < 5 * 60 * 1000;
              const compact =
                sameAuthor && close && !m.thread_id && !(m.events && m.events.length);
              return <MessageRow key={m.id} m={m} compact={!!compact} />;
            });
          })()}
        </div>
      </div>

      <Composer />
    </main>
  );
}

function listeningSummary(
  ch: import("@/lib/types").ChannelItem | undefined,
  agents: import("@/lib/types").AgentItem[],
): string {
  if (!ch) return "";
  const joined = ch.agents || [];
  if (joined.length === 0) return "";
  const head = joined.length + " agent" + (joined.length === 1 ? "" : "s");
  const modes = ch.agent_modes || {};
  const visible = joined.slice(0, 2).map((id) => {
    const display = agents.find((a) => a.id === id)?.display || id;
    const mode = modes[id] === "listen" ? "listening" : "mention only";
    return `${display} ${mode}`;
  });
  let tail = visible.join(" · ");
  if (joined.length > 2) tail += ` · +${joined.length - 2}`;
  return `${head} · ${tail}`;
}

type GearScope =
  | { kind: "channel"; channel: import("@/lib/types").ChannelItem }
  | { kind: "thread"; detail: import("@/lib/types").ThreadDetail };

function AgentGear({
  scope,
  agents,
}: {
  scope: GearScope;
  agents: import("@/lib/types").AgentItem[];
}) {
  const [open, setOpen] = useState(false);
  const [picking, setPicking] = useState(false);
  const [pickQuery, setPickQuery] = useState("");
  const ref = useRef<HTMLDivElement | null>(null);
  const setChannelMode = useStore((s) => s.setChannelAgentMode);
  const setThreadMode = useStore((s) => s.setThreadAgentMode);
  const addAgent = useStore((s) => s.addAgentToChannel);

  useEffect(() => {
    if (!open) return;
    const onDown = (e: MouseEvent) => {
      if (!ref.current?.contains(e.target as Node)) {
        setOpen(false);
        setPicking(false);
        setPickQuery("");
      }
    };
    document.addEventListener("mousedown", onDown);
    return () => document.removeEventListener("mousedown", onDown);
  }, [open]);

  const joinedSource =
    scope.kind === "channel" ? scope.channel.agents : scope.detail.channel_agents;
  const modeMap =
    scope.kind === "channel" ? scope.channel.agent_modes : scope.detail.agent_modes;
  const joinedIDs = new Set(joinedSource || []);
  const joined = agents.filter((a) => joinedIDs.has(a.id));
  const candidates = agents.filter(
    (a) =>
      !joinedIDs.has(a.id) &&
      (pickQuery === "" ||
        a.id.toLowerCase().includes(pickQuery.toLowerCase()) ||
        a.display.toLowerCase().includes(pickQuery.toLowerCase())),
  );
  const modeFor = (id: string) => modeMap?.[id] || "mention_only";
  const flip = (id: string, next: string) => {
    if (scope.kind === "channel") void setChannelMode(scope.channel.id, id, next);
    else void setThreadMode(scope.detail.space_id, scope.detail.parent_id, id, next);
  };
  const heading =
    scope.kind === "channel" ? "Agents in this channel" : "Agents in this thread";
  const empty =
    scope.kind === "channel" ? "No agents joined yet." : null;
  if (scope.kind === "thread" && joined.length === 0) return null;

  return (
    <div className={cn("relative", scope.kind === "thread" && "ml-auto")} ref={ref}>
      <button
        onClick={() => setOpen(!open)}
        className={cn(
          "inline-flex size-6 items-center justify-center border border-transparent text-text-muted hover:border-border hover:bg-accent hover:text-text",
          scope.kind === "channel" && "ml-1",
        )}
        title={scope.kind === "channel" ? "Channel agents" : "Thread agents"}
      >
        <Settings className="size-3.5" />
      </button>
      {open && (
        <div
          className={cn(
            "absolute z-30 mt-1 w-[280px] border-hard border-border bg-panel py-1 text-[13px] shadow-hard",
            scope.kind === "thread" && "right-0",
          )}
        >
          <div className="border-b border-border px-3 py-1.5 font-display text-[10.5px] font-black uppercase tracking-[0.9px] text-text">
            {heading}
          </div>
          {joined.length === 0 && empty && (
            <div className="px-3 py-2 text-[11.5px] text-text-faint">{empty}</div>
          )}
          {joined.map((a) => {
            const m = modeFor(a.id);
            const next = m === "listen" ? "mention_only" : "listen";
            return (
              <button
                key={a.id}
                onClick={() => flip(a.id, next)}
                className="flex w-full cursor-pointer items-center justify-between px-3 py-1.5 hover:bg-accent"
              >
                <span className="flex items-center gap-1.5 text-text">
                  <AtSign className="size-3 text-text-faint" />
                  {a.display}
                </span>
                <span
                  className={cn(
                    "text-[11px]",
                    m === "listen" ? "font-semibold text-text" : "text-text-muted",
                  )}
                >
                  {m === "listen" ? "Listen" : "Mention only"}
                </span>
              </button>
            );
          })}
          {scope.kind === "channel" && (
            <div className="mt-1 border-t border-border pt-1">
              {!picking ? (
                <button
                  onClick={() => setPicking(true)}
                  className="flex w-full cursor-pointer items-center gap-1.5 px-3 py-1.5 text-text-muted hover:bg-accent hover:text-text"
                >
                  <Plus className="size-3" />
                  Add agent…
                </button>
              ) : (
                <div>
                  <input
                    autoFocus
                    value={pickQuery}
                    onChange={(e) => setPickQuery(e.target.value)}
                    placeholder="Search agents…"
                    className="w-full border-b border-border bg-bg px-3 py-1.5 text-[13px] outline-none"
                  />
                  <div className="max-h-[200px] overflow-y-auto">
                    {candidates.length === 0 && (
                      <div className="px-3 py-2 text-[11.5px] text-text-faint">No matching agent.</div>
                    )}
                    {candidates.map((a) => (
                      <button
                        key={a.id}
                        onClick={async () => {
                          await addAgent(scope.channel.id, a.id);
                          setPicking(false);
                          setPickQuery("");
                        }}
                        className="flex w-full cursor-pointer items-center gap-1.5 px-3 py-1.5 hover:bg-accent"
                      >
                        <AtSign className="size-3 text-text-faint" />
                        <span className="text-text">{a.display}</span>
                        {a.role && (
                          <span className="ml-auto text-[11px] text-text-faint truncate max-w-[55%]">{a.role}</span>
                        )}
                      </button>
                    ))}
                  </div>
                </div>
              )}
            </div>
          )}
          {scope.kind === "thread" && (
            <div className="mt-1 border-t border-border px-3 py-1.5 text-[10.5px] text-text-muted">
              Inherited from channel.
            </div>
          )}
        </div>
      )}
    </div>
  );
}

function personaForActiveAgent(
  agents: import("@/lib/types").AgentItem[],
  agentDMs: import("@/lib/types").AgentDMItem[],
  activeAgent: string | null,
): import("@/lib/types").AgentItem | undefined {
  if (!activeAgent) return undefined;
  const direct = agents.find((a) => a.id === activeAgent);
  if (direct) return direct;
  const dm = agentDMs.find((d) => d.id === activeAgent);
  return dm && agents.find((a) => a.id === dm.persona_id);
}

function EmptyState() {
  const view = useStore((s) => s.view);
  const detail = useStore((s) => s.detail);
  const agents = useStore((s) => s.agents);
  const agentDMs = useStore((s) => s.agentDMs);
  const activeAgent = useStore((s) => s.activeAgent);
  const activeChannel = useStore((s) => s.activeChannel);
  const threads = useStore((s) => s.threads);
  const channels = useStore((s) => s.channels);
  const openThread = useStore((s) => s.openThread);

  if (view === "channel") {
    const ch = channels.find((c) => c.id === activeChannel);
    return (
      <div className="text-text-faint text-[13px] py-12 text-center">
        Start in #{ch?.name || "channel"}.
      </div>
    );
  }
  if (view === "thread") {
    return (
      <div className="text-text-faint text-[13px] py-12 text-center">
        Reply in this thread.
      </div>
    );
  }

  const ag = personaForActiveAgent(agents, agentDMs, activeAgent);
  const recent = threads.slice(0, 3);

  return (
    <div className="py-6">
      <div className="flex items-center gap-3 mb-2">
        <div className="size-10 overflow-hidden border-2 border-border bg-panel">
          <Identicon seed={ag?.id || activeAgent || "agent"} kind="agent" />
        </div>
        <div>
          <div className="font-display text-[17px] font-black text-text">
            {detail?.item?.title || "@" + (ag?.display || "")}
          </div>
          {ag?.role && <div className="text-[12.5px] text-text-muted mt-0.5">{ag.role}</div>}
        </div>
      </div>
      <div className="mb-6 text-[13px] leading-[1.6] text-text-muted">
        Message {ag?.display || "this agent"} directly.
      </div>
      {recent.length > 0 && (
        <div>
          <div className="mb-2 border-b border-border font-display text-[10px] font-black uppercase tracking-[1px] text-text">
            Recently with {ag?.display || "this agent"}
          </div>
          <div className="flex flex-col gap-1">
            {recent.map((t) => {
              const ch = channels.find((c) => c.id === t.channel_id);
              return (
                <button
                  key={t.id}
                  onClick={() => void openThread(t.id)}
                  className="flex w-full cursor-pointer items-center justify-between gap-2 border-2 border-transparent px-2 py-1.5 text-left text-text-muted hover:border-border hover:bg-panel-2 hover:text-text"
                >
                  <span className="flex items-center gap-1.5 text-[12.5px] text-text min-w-0">
                    {t.has_running && <Dot status="running" />}
                    <span className="truncate">{t.title}</span>
                  </span>
                  <span className="text-[11px] text-text-faint shrink-0">
                    {ch ? `#${ch.name} · ` : ""}
                    {relTime(t.updated_at)}
                  </span>
                </button>
              );
            })}
          </div>
        </div>
      )}
    </div>
  );
}

function MessageRow({ m, compact }: { m: import("@/lib/types").MessageView; compact: boolean }) {
  const agents = useStore((s) => s.agents);
  const agentDMs = useStore((s) => s.agentDMs);
  const view = useStore((s) => s.view);
  const activeAgent = useStore((s) => s.activeAgent);

  const dmAgent = view === "agent" && m.role !== "user"
    ? personaForActiveAgent(agents, agentDMs, activeAgent)
    : undefined;

  const ag = dmAgent || agents.find((a) => a.id === m.author_id);
  const knownMentions = useMemo(
    () => new Set(agents.map((a) => a.id)),
    [agents],
  );
  const seed = m.role === "user"
    ? "user"
    : (dmAgent?.id || m.author_id || m.author_name || "agent");
  const kind = m.role === "user" ? "user" : "agent";
  const displayName = m.role === "user"
    ? "You"
    : (dmAgent?.display || m.author_name || ag?.display || "Sumi");

  const events = m.events || [];
  const collabEvents = events.filter((e) => e.kind === "mention" || e.kind === "delegate");
  const toolEvents = events.filter((e) => e.kind === "tool_call");
  const noticeEvents = events.filter((e) => e.kind === "service_notice");
  const shouldFoldTools = toolEvents.length > 1;

  return (
    <div
      className={cn(
        "grid grid-cols-[32px_1fr] gap-3.5",
        compact ? "-mt-4 mb-1" : "mb-6 border-b border-border-soft pb-5 last:border-b-0",
      )}
    >
      <div
        className={cn(
          "mt-px size-8 overflow-hidden border-2 border-border bg-panel",
          compact && "invisible",
        )}
      >
        <Identicon seed={seed} kind={kind} />
      </div>
      <div className="min-w-0">
        {!compact && (
          <div className="mb-1.5 flex items-baseline gap-2">
            <span className="font-display text-[13.5px] font-black text-text">
              {displayName}
            </span>
            {m.role !== "user" && ag?.role && (
              <span
                className="border border-border bg-panel-event px-1 font-display text-[10px] font-semibold uppercase tracking-[0.5px] text-text"
                title={ag.role}
              >
                {shortRole(ag.role)}
              </span>
            )}
            <span className="font-mono text-[11px] text-text-faint tabular-nums">{relTime(m.time)}</span>
          </div>
        )}
        {m.reasoning && m.role !== "user" && <ReasoningPreface text={m.reasoning} />}
        {m.auto_reply_reason && m.role !== "user" && (
          <div className="mb-1 inline-flex border border-border bg-accent-bg px-1.5 py-px text-[11.5px] text-text">
            {(ag?.display || m.author_name || "Agent")} joined from channel listening.
          </div>
        )}
        {m.content && (
          m.role === "user" ? (
            <div
              className={cn(
                "max-w-full whitespace-pre-wrap break-words text-[14.5px] leading-[1.7] text-text",
                m.reasoning && "mt-2",
              )}
            >
              {renderMentions(m.content, knownMentions)}
            </div>
          ) : (
            <Markdown
              className={cn(
                "max-w-full text-[14.5px] leading-[1.7] text-text",
                m.reasoning && "mt-2",
              )}
              mentions={knownMentions}
            >
              {stripCollabLeak(m.content)}
            </Markdown>
          )
        )}
        {events.length > 0 && (
          <div className="mt-2 flex flex-col gap-1">
            {collabEvents.map((ev, i) => (
              <EventBlock key={"c" + i} ev={ev} />
            ))}
            {shouldFoldTools ? (
              <ToolFold events={toolEvents} />
            ) : (
              toolEvents.map((ev, i) => <EventBlock key={"t" + i} ev={ev} />)
            )}
            {noticeEvents.map((ev, i) => (
              <EventBlock key={"n" + i} ev={ev} />
            ))}
          </div>
        )}
        {m.thread_id && m.thread_summary && (
          <ThreadLink threadId={m.thread_id} summary={m.thread_summary} />
        )}
        {m.thread_info && <ThreadSummaryRow info={m.thread_info} />}
        {m.task_accessory && <TaskAccessoryRow info={m.task_accessory} />}
      </div>
    </div>
  );
}

function ReasoningPreface({ text }: { text: string }) {
  const [open, setOpen] = useState(false);
  const flat = text.replace(/\s+/g, " ").trim();
  const isLong = flat.length > 280;
  const collapsed = isLong ? flat.slice(0, 280) + "…" : flat;
  return (
    <div className="mb-2 max-w-[66ch] border-l-2 border-border-soft bg-transparent px-2 py-1 text-[11.5px] leading-[1.5] text-text-faint">
      {open ? (
        <Markdown variant="lite" className="whitespace-pre-wrap">
          {text}
        </Markdown>
      ) : (
        <span className="whitespace-pre-wrap">{collapsed}</span>
      )}
      {isLong && (
        <>
          {" "}
          <button
            onClick={() => setOpen((v) => !v)}
            className="cursor-pointer text-[11px] text-text-muted underline underline-offset-2 hover:text-text"
          >
            {open ? "Show less" : "Show full thinking"}
          </button>
        </>
      )}
    </div>
  );
}

function renderableMessage(m: import("@/lib/types").MessageView): boolean {
  if (m.is_thread_reply) return false;
  if (m.content && m.content.trim() !== "") return true;
  if (m.reasoning && m.reasoning.trim() !== "") return true;
  if (m.events && m.events.length > 0) return true;
  if (m.thread_id && m.thread_summary) return true;
  if (m.thread_info) return true;
  return false;
}

function shortRole(role: string): string {
  const trimmed = role.trim().replace(/[.。!?！？]$/, "");
  if (!trimmed) return "";
  const firstWord = trimmed.split(/[\s—·,、/]+/)[0] || trimmed;
  const word = firstWord.length <= 14 ? firstWord : firstWord.slice(0, 14) + "…";
  return titleCase(word);
}

function stripCollabLeak(text: string): string {
  let out = text;
  out = out.replace(/[（(]\s*task_id\s*=\s*[A-Za-z0-9_-]+\s*[）)]/g, "");
  out = out.replace(/[,，]?\s*task_id\s*=\s*[A-Za-z0-9_-]+/g, "");
  out = out.replace(/\bscheduled\s+next\s+team\s+turn\s+for\s+\S+.*$/gim, "");
  return out.replace(/[ \t]+\n/g, "\n").replace(/\n{3,}/g, "\n\n").trim();
}

function titleCase(s: string): string {
  if (!/[a-zA-Z]/.test(s)) return s;
  return s
    .toLowerCase()
    .replace(/(?:^|[\s-])(\p{L})/gu, (m) => m.toUpperCase());
}

function ToolFold({ events }: { events: import("@/lib/types").EventBlock[] }) {
  const [open, setOpen] = useState(false);
  const totalMs = events.reduce((sum, e) => sum + (e.duration_ms || 0), 0);
  const anyRunning = events.some((e) => e.status === "running");
  const anyError = events.some((e) => e.status === "error");
  const status = anyRunning ? "running" : anyError ? "error" : "done";
  const label =
    "Used " + events.length + " tools · " + (anyRunning ? "running" : (totalMs >= 1000 ? Math.round(totalMs / 100) / 10 + "s" : totalMs + "ms"));
  if (open) {
    return (
      <div className="flex flex-col gap-1">
        <button
          onClick={() => setOpen(false)}
      className={cn(
        "self-start cursor-pointer text-[12px] underline underline-offset-2",
            status === "error" ? "text-error" : status === "running" ? "text-running" : "text-text-muted",
          )}
        >
          Hide {events.length} tool details
        </button>
        {events.map((ev, i) => (
          <EventBlock key={i} ev={ev} />
        ))}
      </div>
    );
  }
  return (
    <button
      onClick={() => setOpen(true)}
      className={cn(
        "self-start cursor-pointer text-[12px]",
        status === "error" ? "text-error" : status === "running" ? "text-running" : "text-text-muted",
      )}
    >
      <span>{label}</span>
      <span className="text-text-faint"> · </span>
      <span className="underline underline-offset-2 text-text-faint">view details</span>
      {anyRunning && <span className="ml-1.5 inline-block size-1.5 rounded-full bg-running align-middle" />}
    </button>
  );
}

function ThreadLink({ threadId, summary }: { threadId: string; summary: string }) {
  const openThread = useStore((s) => s.openThread);
  return (
    <button
      onClick={() => void openThread(threadId)}
      className="mt-2.5 inline-flex items-center gap-1.5 border border-border bg-panel-event px-1.5 py-0.5 text-[12px] text-text-muted hover:bg-accent hover:text-text"
    >
      <Dot status="running" />
      <span>{summary}</span>
    </button>
  );
}

function ThreadSummaryRow({ info }: { info: import("@/lib/types").ThreadSummary }) {
  const openThread = useStore((s) => s.openThread);
  const continueLabel = info.reply_count >= 2 ? "Continue in thread →" : "Open thread →";
  const replyLabel = info.reply_count === 1 ? "1 reply" : info.reply_count + " replies";
  const last = info.last_reply_author ? "last by " + info.last_reply_author : "";
  const when = relTime(info.last_reply_time);
  const segments = [replyLabel, last, when].filter((s) => s !== "");
  return (
    <button
      onClick={() => void openThread(info.parent_id)}
      className="mt-1.5 inline-flex items-center gap-1.5 border border-border bg-accent-bg px-1.5 py-0.5 text-[11.5px] text-text hover:bg-accent underline-offset-2 hover:underline"
    >
      {info.has_running_worker && <Dot status="running" />}
      <span className="font-medium">{continueLabel}</span>
      <span className="text-text-faint font-normal">{segments.join(" · ")}</span>
    </button>
  );
}

function TaskAccessoryRow({ info }: { info: import("@/lib/types").TaskAccessoryInfo }) {
  const expandTaskInRail = useStore((s) => s.expandTaskInRail);
  const expandedTaskID = useStore((s) => s.expandedTaskID);
  const taskInScope = useTaskInActiveRail(info.task_id);
  const label = taskAccessoryLabel(info);
  const isRunning = info.status === "running" || info.status === "queued";
  const opened = expandedTaskID === info.task_id;
  const onClick = (e: React.MouseEvent) => {
    e.stopPropagation();
    if (!taskInScope) return;
    expandTaskInRail(info.task_id);
  };
  return (
    <button
      type="button"
      onClick={onClick}
      title={taskInScope ? undefined : "Task is outside current view"}
      className={cn(
        "mt-1.5 inline-flex cursor-pointer items-center gap-1.5 border border-border bg-panel-event px-1.5 py-0.5 text-left text-[11.5px]",
        info.terminal ? "text-text-faint" : "text-text-muted",
        taskInScope ? "hover:text-text" : "cursor-help",
        opened && "text-text",
      )}
    >
      {isRunning && <Dot status="running" />}
      <span>{label}</span>
    </button>
  );
}

function useTaskInActiveRail(taskID: string): boolean {
  const participants = useStore((s) => s.participants);
  const threadDetail = useStore((s) => s.threadDetail);
  if (threadDetail && !threadDetail.unsupported && !threadDetail.not_found) {
    return (threadDetail.recent_runs || []).some((r) => r.id === taskID);
  }
  return (participants?.recent_runs || []).some((r) => r.id === taskID);
}

function taskAccessoryLabel(info: import("@/lib/types").TaskAccessoryInfo): string {
  const who = info.worker_display || info.worker_id || "worker";
  switch (info.status) {
    case "queued":
      return who + " · queued";
    case "running":
      return who + " · working...";
    case "finished":
      return who + " · finished";
    case "failed":
      return info.short_outcome
        ? who + " · failed: " + info.short_outcome
        : who + " · failed";
    case "canceled":
      return info.short_outcome
        ? who + " · canceled: " + info.short_outcome
        : who + " · canceled";
    case "no_output":
      return who + " · finished with no output";
    default:
      return who + " · " + info.status;
  }
}

function ThreadView() {
  const threadDetail = useStore((s) => s.threadDetail);
  const channels = useStore((s) => s.channels);
  const activeChannel = useStore((s) => s.activeChannel);
  const closeThread = useStore((s) => s.closeThread);
  const channel = channels.find((c) => c.id === activeChannel);

  if (!threadDetail) return null;
  if (threadDetail.unsupported) {
    return (
      <main className="h-full min-w-0 grid grid-rows-[auto_1fr] bg-panel">
        <div className="flex items-center gap-3 border-b-hard border-border px-5 py-3">
          <button onClick={() => closeThread()} className="border border-border bg-panel-2 px-2 py-0.5 text-[12px] text-text-muted hover:bg-accent hover:text-text">
            ← Back
          </button>
          <div className="text-[13px] font-semibold text-text">Thread</div>
        </div>
        <div className="overflow-y-auto px-5 py-8 text-[13px] text-text-muted">
          {threadDetail.unsupported_hint || "Threads are not supported here."}
        </div>
      </main>
    );
  }
  if (threadDetail.not_found) {
    return (
      <main className="h-full min-w-0 grid grid-rows-[auto_1fr] bg-panel">
        <div className="flex items-center gap-3 border-b-hard border-border px-5 py-3">
          <button onClick={() => closeThread()} className="border border-border bg-panel-2 px-2 py-0.5 text-[12px] text-text-muted hover:bg-accent hover:text-text">
            ← Back to {channel ? "#" + channel.name : "channel"}
          </button>
          <div className="text-[13px] font-semibold text-text">Thread</div>
        </div>
        <div className="overflow-y-auto px-5 py-8 text-[13px] text-text-muted">
          Thread not found.
        </div>
      </main>
    );
  }

  const root = threadDetail.parent;
  const replies = threadDetail.replies || [];
  return (
    <main className="h-full min-w-0 grid grid-rows-[auto_1fr_auto] bg-panel">
      <div className="flex items-center gap-3 border-b-hard border-border px-5 py-3">
        <button onClick={() => closeThread()} className="border border-border bg-panel-2 px-2 py-0.5 text-[12px] text-text-muted hover:bg-accent hover:text-text">
          ← Back to {channel ? "#" + channel.name : "channel"}
        </button>
        <div className="text-[13px] font-black uppercase tracking-[0.5px] text-text">Thread</div>
        <div className="font-mono text-[12px] text-text-muted">
          {replies.length === 1 ? "1 reply" : replies.length + " replies"}
        </div>
        <AgentGear scope={{ kind: "thread", detail: threadDetail }} agents={useStore.getState().agents} />
      </div>
      <div className="overflow-y-auto px-5 pt-4 pb-5">
        <div className="mx-auto max-w-[880px]">
          {root && (
            <div className="mb-4 border-b border-border-soft border-l-2 border-l-border pl-4 pb-3">
              <div className="mb-1 inline-flex border border-border bg-accent-bg px-1.5 py-px text-[11px] uppercase tracking-wide text-text">Root message · context only</div>
              <MessageRow m={root} compact={false} />
            </div>
          )}
          {replies.length === 0 && (
            <div className="py-4 text-[12.5px] text-text-muted">No replies yet. Send the first reply below.</div>
          )}
          {replies.map((m, i) => {
            const prev = i > 0 ? replies[i - 1] : null;
            const sameAuthor = prev && prev.role === m.role && (prev.author_id || "") === (m.author_id || "");
            const close = prev && new Date(m.time).getTime() - new Date(prev.time).getTime() < 5 * 60 * 1000;
            const compact = sameAuthor && close && !(m.events && m.events.length);
            return <MessageRow key={m.id} m={m} compact={!!compact} />;
          })}
        </div>
      </div>
      <Composer />
    </main>
  );
}

function Composer() {
  const view = useStore((s) => s.view);
  const channels = useStore((s) => s.channels);
  const activeChannel = useStore((s) => s.activeChannel);
  const activeThread = useStore((s) => s.activeThread);
  const agents = useStore((s) => s.agents);
  const activeAgent = useStore((s) => s.activeAgent);
  const detail = useStore((s) => s.detail);
  const sending = useStore((s) => s.sending);
  const send = useStore((s) => s.send);
  const threadDetail = useStore((s) => s.threadDetail);
  const participants = useStore((s) => s.participants);
  const streamingByID = useStore((s) => s.streamingByID);

  const [input, setInput] = useState("");
  const [persona, setPersona] = useState("");
  const [mentionState, setMentionState] = useState<{ start: number; query: string } | null>(null);
  const [mentionIndex, setMentionIndex] = useState(0);
  const textareaRef = useRef<HTMLTextAreaElement | null>(null);

  const updateMentionState = (value: string, caret: number) => {
    const before = value.slice(0, caret);
    const at = before.lastIndexOf("@");
    if (at < 0) {
      setMentionState(null);
      return;
    }
    const prevChar = at === 0 ? "" : before[at - 1];
    if (prevChar !== "" && !/\s/.test(prevChar)) {
      setMentionState(null);
      return;
    }
    const query = before.slice(at + 1);
    if (/\s/.test(query)) {
      setMentionState(null);
      return;
    }
    setMentionState({ start: at, query });
    setMentionIndex(0);
  };

  const closeMention = () => {
    setMentionState(null);
    setMentionIndex(0);
  };

  const mentionCandidates = (() => {
    if (!mentionState) return [] as { id: string; display: string }[];
    const q = mentionState.query.toLowerCase();
    return agents
      .filter((a) => {
        if (q === "") return true;
        return (
          a.id.toLowerCase().includes(q) || (a.display || "").toLowerCase().includes(q)
        );
      })
      .slice(0, 6);
  })();

  const acceptMention = (idx: number) => {
    if (!mentionState) return;
    const choice = mentionCandidates[idx];
    if (!choice) return;
    const before = input.slice(0, mentionState.start);
    const afterStart = mentionState.start + 1 + mentionState.query.length;
    const tail = input.slice(afterStart);
    const inserted = "@" + choice.id + (tail.startsWith(" ") ? "" : " ");
    const next = before + inserted + tail;
    const caret = before.length + inserted.length;
    setInput(next);
    closeMention();
    requestAnimationFrame(() => {
      const ta = textareaRef.current;
      if (ta) {
        ta.focus();
        ta.setSelectionRange(caret, caret);
      }
    });
  };
  const agentDMs = useStore((s) => s.agentDMs);
  const inferredPersona = (() => {
    if (view === "agent" && activeAgent) {
      if (detail?.item.persona_id) return detail.item.persona_id;
      const dm = agentDMs.find((d) => d.id === activeAgent);
      return dm?.persona_id || activeAgent;
    }
    if (view === "thread" && detail) {
      for (let i = detail.messages.length - 1; i >= 0; i--) {
        const m = detail.messages[i];
        if (m.role !== "user" && m.author_id) return m.author_id;
      }
    }
    return "";
  })();

  useEffect(() => {
    setPersona(inferredPersona);
  }, [view, activeAgent, activeChannel, activeThread, inferredPersona]);

  let placeholder = "Message...";
  if (threadDetail && !threadDetail.unsupported && !threadDetail.not_found) {
    placeholder = "Reply to thread...";
  } else if (view === "channel") {
    const ch = channels.find((c) => c.id === activeChannel);
    placeholder = `Message #${ch?.name || "channel"}...`;
  } else if (view === "thread") {
    placeholder = "Reply in thread...";
  } else if (view === "agent") {
    const ag = personaForActiveAgent(agents, agentDMs, activeAgent);
    placeholder = `Message @${detail?.item.persona_name || ag?.display || "agent"}...`;
  }

  const trimmed = input.trim();
  const canSend = trimmed.length > 0 && !sending;
  const usesRouting = view === "channel" || view === "thread";
  const hasMention = /(^|\s)@/.test(input);
  const showRouteHint = usesRouting && trimmed.length >= 5 && !hasMention;
  const channelForHint = view === "channel" ? channels.find((c) => c.id === activeChannel) : undefined;
  const showEmptyAgentsHint = view === "channel" && channelForHint && (channelForHint.agents || []).length === 0;
  const composerHint = useStore((s) => s.composerHint);
  const [now, setNow] = useState(() => Date.now());
  useEffect(() => {
    if (!composerHint) return;
    setNow(Date.now());
    const t = setTimeout(() => setNow(Date.now()), 4000);
    return () => clearTimeout(t);
  }, [composerHint]);
  const showRoutingHint = composerHint && now - composerHint.at < 4000;
  const workingAgents = useMemo(() => {
    const byID = new Map<string, string>();
    const labelFor = (id: string) => agents.find((a) => a.id === id)?.display || id;
    Object.values(streamingByID).forEach((turn) => {
      if (turn.agentID) byID.set(turn.agentID, labelFor(turn.agentID));
    });
    if (byID.size === 0) {
      (participants?.active_runs || []).forEach((run) => {
        if (run.status === "running" && run.agent_id) {
          byID.set(run.agent_id, labelFor(run.agent_id));
        }
      });
    }
    return Array.from(byID, ([id, display]) => ({ id, display }));
  }, [agents, participants?.active_runs, streamingByID]);

  const handleSend = async () => {
    if (!canSend) return;
    const text = trimmed;
    setInput("");
    await send(text, persona || undefined);
  };

  const handleKey = (e: React.KeyboardEvent<HTMLTextAreaElement>) => {
    if (mentionState && mentionCandidates.length > 0) {
      if (e.key === "ArrowDown") {
        e.preventDefault();
        setMentionIndex((i) => (i + 1) % mentionCandidates.length);
        return;
      }
      if (e.key === "ArrowUp") {
        e.preventDefault();
        setMentionIndex((i) => (i - 1 + mentionCandidates.length) % mentionCandidates.length);
        return;
      }
      if (e.key === "Enter" && !(e.metaKey || e.ctrlKey)) {
        e.preventDefault();
        acceptMention(mentionIndex);
        return;
      }
      if (e.key === "Tab") {
        e.preventDefault();
        acceptMention(mentionIndex);
        return;
      }
      if (e.key === "Escape") {
        e.preventDefault();
        closeMention();
        return;
      }
    }
    if ((e.metaKey || e.ctrlKey) && e.key === "Enter") {
      e.preventDefault();
      void handleSend();
    }
  };

  const handleChange = (e: React.ChangeEvent<HTMLTextAreaElement>) => {
    const value = e.target.value;
    setInput(value);
    updateMentionState(value, e.target.selectionStart ?? value.length);
  };

  const handleSelect = (e: React.SyntheticEvent<HTMLTextAreaElement>) => {
    const ta = e.currentTarget;
    updateMentionState(ta.value, ta.selectionStart ?? ta.value.length);
  };

  return (
    <div className="border-t-hard border-border bg-panel px-5 pb-3.5 pt-3">
      <div className="mx-auto max-w-[1040px]">
        {showRoutingHint && (
          <div className="mb-2 inline-flex border border-border bg-accent-bg px-2 py-0.5 text-[11.5px] text-text">
            {composerHint?.text}
          </div>
        )}
        {showEmptyAgentsHint && !showRoutingHint && (
          <div className="mb-2 inline-flex border border-border bg-panel-2 px-2 py-0.5 text-[11.5px] text-text-muted">
            Mention or add an agent to collaborate.
          </div>
        )}
        {showRouteHint && !showEmptyAgentsHint && !showRoutingHint && (
          <div className="mb-2 inline-flex border border-border bg-panel-2 px-2 py-0.5 text-[11.5px] text-text-muted">
            Mention an agent, or let listening agents pick it up.
          </div>
        )}
        <div className="relative border-hard border-border bg-bg shadow-card">
          <textarea
            ref={textareaRef}
            rows={2}
            placeholder={placeholder}
            value={input}
            onChange={handleChange}
            onSelect={handleSelect}
            onKeyDown={handleKey}
            onBlur={() => {
              setTimeout(() => closeMention(), 120);
            }}
            disabled={sending}
            className="min-h-[68px] w-full resize-none bg-transparent px-3.5 py-2.5 text-[14px] leading-[1.55] text-text outline-none disabled:opacity-70"
          />
          {mentionState &&
            (mentionCandidates.length > 0 ? (
              <div className="absolute bottom-full left-0 z-30 mb-1.5 max-h-[260px] w-[280px] overflow-y-auto border-hard border-border bg-panel py-1 text-[13px] shadow-hard">
                {mentionCandidates.map((a, i) => (
                  <button
                    key={a.id}
                    type="button"
                    onMouseDown={(e) => {
                      e.preventDefault();
                      acceptMention(i);
                    }}
                    onMouseEnter={() => setMentionIndex(i)}
                    className={cn(
                      "flex w-full items-center gap-2 px-3 py-1.5 text-left",
                      i === mentionIndex ? "bg-accent" : "hover:bg-accent",
                    )}
                  >
                    <span className="text-text-faint">@</span>
                    <span className="text-text">{a.id}</span>
                    {a.display && a.display !== a.id && (
                      <span className="ml-auto text-[11.5px] text-text-faint">{a.display}</span>
                    )}
                  </button>
                ))}
              </div>
            ) : (
              <div className="absolute bottom-full left-0 z-30 mb-1.5 w-[280px] border-hard border-border bg-panel px-3 py-1.5 text-[12px] text-text-muted shadow-hard">
                No agent matches "{mentionState.query}"
              </div>
            ))}
        </div>
        <div className="mt-2 flex items-center gap-2">
          {view === "agent" ? (
            <span className="border border-border bg-panel-2 px-1.5 py-1 text-[12px] text-text-muted">
              {(() => {
                const ag = personaForActiveAgent(agents, agentDMs, activeAgent);
                return ag ? "@" + ag.display : "";
              })()}
            </span>
          ) : null}
          <WorkingAgents agents={workingAgents} />
          <span className="flex-1" />
          <Button
            variant="default"
            disabled={!canSend}
            onClick={() => void handleSend()}
            className={cn(
              "border-hard border-border bg-action px-4 font-black uppercase tracking-[0.5px] text-panel shadow-card disabled:bg-action disabled:text-panel disabled:opacity-100",
              canSend && "hover:bg-action",
            )}
          >
            {sending ? "Sending…" : "Send"}
          </Button>
        </div>
      </div>
    </div>
  );
}

function WorkingAgents({ agents }: { agents: { id: string; display: string }[] }) {
  if (agents.length === 0) return null;
  return (
    <div className="flex min-w-0 flex-wrap items-center gap-1.5">
      {agents.map((agent) => (
        <span
          key={agent.id}
          className="inline-flex h-7 max-w-[180px] items-center gap-1.5 border border-border bg-panel-event px-2 text-[11.5px] text-text-muted"
          title={agent.display + " working"}
        >
          <Dot status="running" />
          <span className="truncate">
            <span className="text-text">{agent.display}</span> working
          </span>
        </span>
      ))}
    </div>
  );
}
