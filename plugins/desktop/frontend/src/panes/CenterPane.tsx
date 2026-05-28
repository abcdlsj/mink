import { Hash, MessageSquare, AtSign, Square } from "lucide-react";
import { useStore } from "@/lib/store";
import { Button } from "@/components/ui/button";
import { Identicon } from "@/components/Identicon";
import { Dot } from "./LeftPane";
import { cn, relTime } from "@/lib/utils";

export function CenterPane() {
  const view = useStore((s) => s.view);
  const detail = useStore((s) => s.detail);
  const channels = useStore((s) => s.channels);
  const activeChannel = useStore((s) => s.activeChannel);

  if (!detail) {
    return (
      <main className="grid grid-rows-[auto_1fr_auto] bg-panel min-w-0">
        <div className="border-b border-border-soft px-8 py-4" />
        <div className="overflow-y-auto px-8 py-6 text-text-faint text-[12.5px]">
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
  if (view === "channel") {
    TitleIcon = Hash;
    titleText = channel?.name || "channel";
    metaText = item.running ? "agents running" : "";
  } else if (view === "thread") {
    TitleIcon = MessageSquare;
    metaText = channel ? `in #${channel.name}` : "";
  } else if (view === "agent") {
    TitleIcon = AtSign;
    titleText = titleText.replace(/^@/, "");
    metaText = detail.summary || "";
  }

  const showStop = item.running && view === "thread";

  return (
    <main className="grid grid-rows-[auto_1fr_auto] bg-panel min-w-0">
      <div className="flex items-end justify-between border-b border-border-soft px-8 pt-4 pb-3.5">
        <div>
          <h2 className="flex items-center gap-1.5 text-[15px] font-semibold text-text">
            <TitleIcon className="size-4 text-text-muted" />
            <span>{titleText}</span>
          </h2>
          {metaText && <div className="mt-0.5 text-[12px] text-text-muted">{metaText}</div>}
        </div>
        {showStop && (
          <Button variant="danger" size="sm">
            <Square className="size-3" />
            <span>Stop run</span>
          </Button>
        )}
      </div>

      <div className="overflow-y-auto px-8 pt-5 pb-6">
        <div className="mx-auto max-w-[800px]">
          {detail.messages.length === 0 ? (
            <EmptyState />
          ) : (
            detail.messages.map((m, i) => {
              const prev = detail.messages[i - 1];
              const sameAuthor =
                prev && prev.role === m.role && (prev.author_id || "") === (m.author_id || "");
              const close =
                prev && new Date(m.time).getTime() - new Date(prev.time).getTime() < 5 * 60 * 1000;
              const compact =
                sameAuthor && close && !m.thread_id && !(m.events && m.events.length);
              return <MessageRow key={m.id} m={m} compact={!!compact} />;
            })
          )}
        </div>
      </div>

      <Composer />
    </main>
  );
}

function EmptyState() {
  const view = useStore((s) => s.view);
  const detail = useStore((s) => s.detail);
  if (view !== "agent") {
    return (
      <div className="text-text-faint text-[12.5px] py-10 text-center">No messages yet.</div>
    );
  }
  const ag = detail?.item;
  return (
    <div className="rounded-md border border-border-soft bg-panel-2 px-6 py-6 mb-4">
      <div className="text-[14px] font-semibold text-text">
        Direct conversation with {ag?.title || ""}
      </div>
      {detail?.summary && (
        <div className="mt-1 text-[12px] text-text-muted">{detail.summary}</div>
      )}
      <div className="mt-2.5 text-[12.5px] text-text-faint leading-relaxed">
        Send a message below to start. Threads and channels involving this agent appear on the right.
      </div>
    </div>
  );
}

function MessageRow({ m, compact }: { m: import("@/lib/types").MessageView; compact: boolean }) {
  const agents = useStore((s) => s.agents);
  const ag = agents.find((a) => a.id === m.author_id);
  const seed = m.role === "user" ? "user" : m.author_id || m.author_name || "agent";
  const kind = m.role === "user" ? "user" : "agent";

  return (
    <div
      className={cn(
        "grid grid-cols-[30px_1fr] gap-3.5",
        compact ? "-mt-4 mb-1" : "mb-6",
      )}
    >
      <div
        className={cn(
          "size-[30px] rounded-md border border-border-soft bg-panel overflow-hidden mt-px shadow-[0_1px_0_rgba(31,41,51,0.02)]",
          compact && "invisible",
        )}
      >
        <Identicon seed={seed} kind={kind} />
      </div>
      <div>
        {!compact && (
          <div className="flex items-baseline gap-1.5 mb-0.5">
            <span className="text-[12.5px] font-semibold text-text-muted tracking-[0.1px]">
              {m.role === "user" ? "You" : m.author_name || "Sumi"}
            </span>
            {m.role !== "user" && ag?.role && (
              <span className="text-[10px] uppercase tracking-[0.4px] text-text-faint border border-border-soft rounded-[3px] px-1.5 py-px font-medium">
                {ag.role}
              </span>
            )}
            <span className="text-[11px] text-text-faint">{relTime(m.time)}</span>
          </div>
        )}
        {m.content && (
          <div className="text-[14px] text-text leading-[1.7] whitespace-pre-wrap">{m.content}</div>
        )}
        {m.thread_id && m.thread_summary && (
          <ThreadLink threadId={m.thread_id} summary={m.thread_summary} />
        )}
      </div>
    </div>
  );
}

function ThreadLink({ threadId, summary }: { threadId: string; summary: string }) {
  const openThread = useStore((s) => s.openThread);
  return (
    <button
      onClick={() => void openThread(threadId)}
      className="mt-2.5 inline-flex items-center gap-1.5 px-1.5 py-0.5 rounded-sm text-[12px] text-text-muted hover:text-text hover:bg-panel-2"
    >
      <Dot status="running" />
      <span>{summary}</span>
    </button>
  );
}

function Composer() {
  const view = useStore((s) => s.view);
  const channels = useStore((s) => s.channels);
  const activeChannel = useStore((s) => s.activeChannel);
  const agents = useStore((s) => s.agents);
  const activeAgent = useStore((s) => s.activeAgent);
  const personas = useStore((s) => s.personas);
  const models = useStore((s) => s.models);

  let placeholder = "Message...";
  if (view === "channel") {
    const ch = channels.find((c) => c.id === activeChannel);
    placeholder = `Message #${ch?.name || "channel"}...`;
  } else if (view === "thread") {
    placeholder = "Reply in thread...";
  } else if (view === "agent") {
    const ag = agents.find((a) => a.id === activeAgent);
    placeholder = `Message @${ag?.display || "agent"}...`;
  }

  return (
    <div className="border-t border-border-soft px-8 pb-5 pt-3.5 bg-panel">
      <div className="mx-auto max-w-[800px]">
        <textarea
          rows={2}
          placeholder={placeholder}
          className="w-full min-h-[76px] resize-none rounded-md border border-border bg-panel px-3.5 py-3 text-[14px] leading-[1.55] text-text outline-none transition-[border,box-shadow] hover:border-border-strong focus:border-accent focus:ring-[3px] focus:ring-accent-bg"
        />
        <div className="mt-2.5 flex items-center gap-2">
          <select className="bg-transparent text-[12px] text-text-muted px-1.5 py-1 rounded-sm hover:bg-panel-2 hover:text-text outline-none cursor-pointer">
            <option value="">Default agent</option>
            {personas.map((p) => (
              <option key={p.id} value={p.id}>
                {p.display}
              </option>
            ))}
          </select>
          <select className="bg-transparent text-[12px] text-text-muted px-1.5 py-1 rounded-sm hover:bg-panel-2 hover:text-text outline-none cursor-pointer">
            {models.map((m) => (
              <option key={m.name} value={m.name}>
                {m.model}
              </option>
            ))}
          </select>
          <span className="flex-1" />
          <Button variant="primary">Send</Button>
        </div>
      </div>
    </div>
  );
}
