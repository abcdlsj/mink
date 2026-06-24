import { useMemo, type ReactNode } from "react";
import type { MessageView } from "@/lib/types";
import { useStore } from "@/lib/store";
import { MessageRow } from "./MessageRow";
import { renderableMessage } from "./message-helpers";

export function MessageStream({
  messages,
  empty,
  filterRenderable = true,
  compactAcrossThreadLinks = false,
  threadStartsEnabled = false,
  selecting = false,
  selectedIDs,
  onToggleSelected,
}: {
  messages: MessageView[];
  empty: ReactNode;
  filterRenderable?: boolean;
  compactAcrossThreadLinks?: boolean;
  threadStartsEnabled?: boolean;
  selecting?: boolean;
  selectedIDs?: Set<string>;
  onToggleSelected?: (messageID: string) => void;
}) {
  const visible = useMemo(
    () => filterRenderable ? messages.filter(renderableMessage) : messages,
    [filterRenderable, messages],
  );
  const activeAnchor = useStore((s) => s.activeAnchor);
  const activeMessageID = activeAnchor?.startsWith("message:") ? activeAnchor.slice("message:".length) : "";

  if (visible.length === 0) return <>{empty}</>;
  return (
    <>
      {visible.map((m, i) => {
        const prev = visible[i - 1];
        const sameAuthor =
          prev && prev.role === m.role && (prev.author_id || "") === (m.author_id || "");
        const close =
          prev && new Date(m.time).getTime() - new Date(prev.time).getTime() < 5 * 60 * 1000;
        const compact =
          sameAuthor &&
          close &&
          (compactAcrossThreadLinks || !m.thread_id) &&
          !hasHardBreakEvents(m);
        return (
          <MessageRow
            key={m.id}
            m={m}
            compact={!!compact}
            threadStartsEnabled={threadStartsEnabled}
            selecting={selecting}
            selected={!!selectedIDs?.has(m.id)}
            highlighted={m.id === activeMessageID}
            onToggleSelected={() => onToggleSelected?.(m.id)}
          />
        );
      })}
    </>
  );
}

function hasHardBreakEvents(m: MessageView): boolean {
  const events = m.events || [];
  return events.some((ev) => ev.kind !== "mention" && ev.kind !== "delegate");
}
