import { useEffect, useRef, useState } from "react";
import { Ear } from "lucide-react";
import { Button } from "@/components/ui/button";
import { useStore } from "@/lib/store";
import { cn } from "@/lib/utils";
import type { AgentItem, ChannelItem, ThreadDetail } from "@/lib/types";
import { personaForActiveAgent } from "../Message/message-helpers";
import { MentionAutocomplete } from "./MentionAutocomplete";
import { applyMention, mentionCandidates, nextMentionState, type MentionState } from "./composer-helpers";

const draftMap = new Map<string, string>();

export function Composer({ forceMainScope = false }: { forceMainScope?: boolean }) {
  const view = useStore((s) => s.view);
  const channels = useStore((s) => s.channels);
  const activeChannel = useStore((s) => s.activeChannel);
  const activeDirect = useStore((s) => s.activeDirect);
  const activeThread = useStore((s) => s.activeThread);
  const agents = useStore((s) => s.agents);
  const activeAgentSpace = useStore((s) => s.activeAgentSpace);
  const detail = useStore((s) => s.detail);
  const sendingByScope = useStore((s) => s.sendingByScope);
  const send = useStore((s) => s.send);
  const threadDetail = useStore((s) => s.threadDetail);
  const participants = useStore((s) => s.participants);
  const activeThreadDetail = forceMainScope ? null : threadDetail;

  const [input, setInput] = useState("");
  const [persona, setPersona] = useState("");
  const [mentionState, setMentionState] = useState<MentionState | null>(null);
  const [mentionIndex, setMentionIndex] = useState(0);
  const textareaRef = useRef<HTMLTextAreaElement | null>(null);

  const updateMentionState = (value: string, caret: number) => {
    const next = nextMentionState(value, caret);
    setMentionState(next);
    if (next) setMentionIndex(0);
  };

  const closeMention = () => {
    setMentionState(null);
    setMentionIndex(0);
  };

  const mentionOptions = mentionCandidates(agents, mentionState);
  const quickAgents = quickMentionAgents({
    view,
    agents,
    channel: view === "channel" ? channels.find((c) => c.id === activeChannel) : undefined,
    threadDetail: activeThreadDetail,
    participants: participants?.agents || [],
  });

  const acceptMention = (idx: number) => {
    if (!mentionState) return;
    const choice = mentionOptions[idx];
    if (!choice) return;
    const applied = applyMention(input, mentionState, choice);
    setInput(applied.next);
    if (currentScopeKey) draftMap.set(currentScopeKey, applied.next);
    closeMention();
    requestAnimationFrame(() => {
      const ta = textareaRef.current;
      if (ta) {
        ta.focus();
        ta.setSelectionRange(applied.caret, applied.caret);
      }
    });
  };
  const insertMention = (agentID: string) => {
    const mention = "@" + agentID + " ";
    const ta = textareaRef.current;
    const start = ta?.selectionStart ?? input.length;
    const end = ta?.selectionEnd ?? start;
    const needsLead = start > 0 && !/\s/.test(input[start - 1] || "");
    const prefix = needsLead ? " " : "";
    const next = input.slice(0, start) + prefix + mention + input.slice(end);
    const caret = start + prefix.length + mention.length;
    setInput(next);
    if (currentScopeKey) draftMap.set(currentScopeKey, next);
    closeMention();
    requestAnimationFrame(() => {
      const current = textareaRef.current;
      if (!current) return;
      current.focus();
      current.setSelectionRange(caret, caret);
    });
  };
  const agentDMs = useStore((s) => s.agentDMs);
  const inferredPersona = (() => {
    if (view === "agent" && activeAgentSpace) {
      if (detail?.item.persona_id) return detail.item.persona_id;
      const dm = agentDMs.find((d) => d.id === activeAgentSpace);
      return dm?.persona_id || activeAgentSpace;
    }
    return "";
  })();

  useEffect(() => {
    setPersona(inferredPersona);
  }, [view, activeAgentSpace, activeChannel, activeDirect, activeThread, inferredPersona]);

  let placeholder = "Message...";
  if (activeThreadDetail && !activeThreadDetail.unsupported && !activeThreadDetail.not_found) {
    placeholder = "Reply to thread...";
  } else if (view === "channel") {
    const ch = channels.find((c) => c.id === activeChannel);
    placeholder = `Message #${ch?.name || "channel"}...`;
  } else if (view === "direct") {
    placeholder = "Message this conversation...";
  } else if (view === "agent") {
    const ag = personaForActiveAgent(agents, agentDMs, activeAgentSpace, detail?.item.persona_id);
    placeholder = `Message @${detail?.item.persona_name || ag?.display || "agent"}...`;
  }

  const trimmed = input.trim();
  const currentScopeKey = (() => {
    if (activeThreadDetail && !activeThreadDetail.unsupported && !activeThreadDetail.not_found) {
      return activeThreadDetail.space_id + "::thread:" + activeThreadDetail.parent_id;
    }
    if (view === "agent") return detail?.item.id || activeAgentSpace || "";
    if (view === "direct") return activeDirect || "";
    if (view === "channel") return activeChannel || "";
    return "";
  })();

  useEffect(() => {
    const saved = currentScopeKey ? (draftMap.get(currentScopeKey) ?? "") : "";
    setInput(saved);
  }, [currentScopeKey]);
  const sending = currentScopeKey ? !!sendingByScope[currentScopeKey] : false;
  const canSend = trimmed.length > 0 && !sending;
  const usesRouting =
    (view === "channel" && !!activeChannel) ||
    !!activeThreadDetail;
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
  const handleSend = async () => {
    if (!canSend) return;
    const text = trimmed;
    setInput("");
    if (currentScopeKey) draftMap.delete(currentScopeKey);
    await send(text, persona || undefined, {
      parentMessageID: activeThreadDetail && !activeThreadDetail.unsupported && !activeThreadDetail.not_found
        ? activeThreadDetail.parent_id
        : null,
      scopeKey: currentScopeKey,
    });
  };

  const handleKey = (e: React.KeyboardEvent<HTMLTextAreaElement>) => {
    if (mentionState && mentionOptions.length > 0) {
      if (e.key === "ArrowDown") {
        e.preventDefault();
        setMentionIndex((i) => (i + 1) % mentionOptions.length);
        return;
      }
      if (e.key === "ArrowUp") {
        e.preventDefault();
        setMentionIndex((i) => (i - 1 + mentionOptions.length) % mentionOptions.length);
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
    if (currentScopeKey) draftMap.set(currentScopeKey, value);
    updateMentionState(value, e.target.selectionStart ?? value.length);
  };

  const handleSelect = (e: React.SyntheticEvent<HTMLTextAreaElement>) => {
    const ta = e.currentTarget;
    updateMentionState(ta.value, ta.selectionStart ?? ta.value.length);
  };

  return (
    <div className="border-t-hard border-border bg-panel px-3 pb-[max(0.875rem,env(safe-area-inset-bottom))] pt-2.5 md:px-5 md:pb-3.5 md:pt-3">
      <div className="w-full">
        {showRoutingHint && (
          <div className="mb-2 inline-flex border border-mention-border bg-mention-bg px-2 py-0.5 text-[11.5px] text-mention">
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
        {quickAgents.length > 0 && (
          <div className="mb-2 flex flex-wrap items-center gap-1.5">
            {quickAgents.map((ag) => (
              <button
                key={ag.id}
                type="button"
                onMouseDown={(e) => e.preventDefault()}
                onClick={() => insertMention(ag.id)}
                title={ag.listening ? `@${ag.display} is listening` : `Mention @${ag.display}`}
                className={cn(
                  "inline-flex items-center gap-1 border border-border bg-panel px-1.5 py-0.5 text-[11.5px] font-medium text-text-muted hover:bg-accent hover:text-text",
                  ag.listening && "border-agent-border bg-agent-bg text-agent",
                )}
              >
                {ag.listening && <Ear className="size-3 text-agent" />}
                <span>@{ag.display}</span>
              </button>
            ))}
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
            className="min-h-[54px] w-full resize-none bg-transparent px-3 py-2 text-[16px] leading-[1.5] text-text outline-none md:min-h-[68px] md:px-3.5 md:py-2.5 md:text-[14px] md:leading-[1.55]"
          />
          {mentionState && (
            <MentionAutocomplete
              state={mentionState}
              candidates={mentionOptions}
              index={mentionIndex}
              onAccept={acceptMention}
              onHover={setMentionIndex}
            />
          )}
        </div>
        <div className="mt-2 flex flex-wrap items-center gap-2">
          <span className="min-w-4 flex-1" />
          <Button
            variant="default"
            disabled={!canSend}
            onClick={() => void handleSend()}
            className={cn(
              "border-hard border-border bg-action px-4 font-extrabold uppercase text-panel shadow-card disabled:bg-action disabled:text-panel disabled:opacity-100 md:px-4",
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

function quickMentionAgents({
  view,
  agents,
  channel,
  threadDetail,
  participants,
}: {
  view: string;
  agents: AgentItem[];
  channel?: ChannelItem;
  threadDetail: ThreadDetail | null;
  participants: AgentItem[];
}): { id: string; display: string; listening: boolean }[] {
  const ids = new Set<string>();
  let modes: Record<string, string> | undefined;
  if (threadDetail && !threadDetail.unsupported && !threadDetail.not_found) {
    (threadDetail.channel_agents || []).forEach((id) => ids.add(id));
    (threadDetail.participants || []).forEach((agent) => ids.add(agent.id));
    modes = threadDetail.agent_modes;
  } else if (view === "channel" && channel) {
    (channel.agents || []).forEach((id) => ids.add(id));
    modes = channel.agent_modes;
  } else {
    return [];
  }
  participants.forEach((agent) => ids.add(agent.id));
  return Array.from(ids)
    .map((id) => {
      const agent = agents.find((item) => item.id === id) || participants.find((item) => item.id === id);
      if (!agent) return null;
      return {
        id,
        display: agent.display || id,
        listening: modes?.[id] === "listen",
      };
    })
    .filter((item): item is { id: string; display: string; listening: boolean } => !!item)
    .slice(0, 6);
}
