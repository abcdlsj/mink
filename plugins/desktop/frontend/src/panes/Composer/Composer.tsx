import { useEffect, useMemo, useRef, useState } from "react";
import { Button } from "@/components/ui/button";
import { useStore } from "@/lib/store";
import { cn } from "@/lib/utils";
import { personaForActiveAgent } from "../Message/message-helpers";
import { MentionAutocomplete } from "./MentionAutocomplete";
import { WorkingAgents } from "./WorkingAgents";
import { applyMention, mentionCandidates, nextMentionState, type MentionState } from "./composer-helpers";

export function Composer() {
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

  const acceptMention = (idx: number) => {
    if (!mentionState) return;
    const choice = mentionOptions[idx];
    if (!choice) return;
    const applied = applyMention(input, mentionState, choice);
    setInput(applied.next);
    closeMention();
    requestAnimationFrame(() => {
      const ta = textareaRef.current;
      if (ta) {
        ta.focus();
        ta.setSelectionRange(applied.caret, applied.caret);
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
    updateMentionState(value, e.target.selectionStart ?? value.length);
  };

  const handleSelect = (e: React.SyntheticEvent<HTMLTextAreaElement>) => {
    const ta = e.currentTarget;
    updateMentionState(ta.value, ta.selectionStart ?? ta.value.length);
  };

  return (
    <div className="border-t-hard border-border bg-panel px-3 pb-[max(0.875rem,env(safe-area-inset-bottom))] pt-2.5 md:px-5 md:pb-3.5 md:pt-3">
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
            className="min-h-[54px] w-full resize-none bg-transparent px-3 py-2 text-[16px] leading-[1.5] text-text outline-none disabled:opacity-70 md:min-h-[68px] md:px-3.5 md:py-2.5 md:text-[14px] md:leading-[1.55]"
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
          {(() => {
            if (view !== "agent") return null;
            const ag = personaForActiveAgent(agents, agentDMs, activeAgent);
            if (!ag) return null;
            return (
              <span className="border border-border bg-panel-2 px-1.5 py-1 text-[12px] text-text-muted">
                @{ag.display}
              </span>
            );
          })()}
          <WorkingAgents agents={workingAgents} />
          <span className="min-w-4 flex-1" />
          <Button
            variant="default"
            disabled={!canSend}
            onClick={() => void handleSend()}
            className={cn(
              "border-hard border-border bg-action px-4 font-black uppercase tracking-[0.5px] text-panel shadow-card disabled:bg-action disabled:text-panel disabled:opacity-100 md:px-4",
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
