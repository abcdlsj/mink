import { useEffect, useMemo, useState, type ReactNode } from "react";
import type { MessageView } from "@/lib/types";
import { MessageRow } from "./MessageRow";
import { MessageSelectionBar } from "./MessageSelectionBar";
import { renderableMessage } from "./message-helpers";

export function MessageStream({
  messages,
  empty,
  filterRenderable = true,
  compactAcrossThreadLinks = false,
  threadStartsEnabled = false,
  sourceLabel = "current conversation",
  title,
  hrefFor,
}: {
  messages: MessageView[];
  empty: ReactNode;
  filterRenderable?: boolean;
  compactAcrossThreadLinks?: boolean;
  threadStartsEnabled?: boolean;
  sourceLabel?: string;
  title?: string;
  hrefFor?: (m: MessageView) => string;
}) {
  const visible = useMemo(
    () => filterRenderable ? messages.filter(renderableMessage) : messages,
    [filterRenderable, messages],
  );
  const [selecting, setSelecting] = useState(false);
  const [selected, setSelected] = useState<Set<string>>(() => new Set());
  const visibleIDKey = useMemo(() => visible.map((m) => m.id).join("\n"), [visible]);
  const selectedMessages = useMemo(
    () => visible.filter((m) => selected.has(m.id)),
    [selected, visible],
  );

  useEffect(() => {
    setSelected((prev) => {
      const visibleIDs = visibleIDKey ? visibleIDKey.split("\n") : [];
      const ids = new Set(visibleIDs);
      const next = new Set([...prev].filter((id) => ids.has(id)));
      return next.size === prev.size ? prev : next;
    });
  }, [visibleIDKey]);

  if (visible.length === 0) return <>{empty}</>;
  return (
    <>
      <MessageSelectionBar
        active={selecting}
        selectedCount={selected.size}
        messages={selectedMessages}
        sourceLabel={sourceLabel}
        title={title}
        hrefFor={hrefFor || defaultHrefFor}
        onStart={() => setSelecting(true)}
        onCancel={() => {
          setSelecting(false);
          setSelected(new Set());
        }}
        onSelectAll={() => setSelected(new Set(visibleIDKey ? visibleIDKey.split("\n") : []))}
      />
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
            selected={selected.has(m.id)}
            onToggleSelected={() => setSelected((prev) => {
              const next = new Set(prev);
              if (next.has(m.id)) next.delete(m.id);
              else next.add(m.id);
              return next;
            })}
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

function defaultHrefFor(m: MessageView): string {
  if (typeof window === "undefined") return "#message-" + m.id;
  const url = new URL(window.location.href);
  url.searchParams.set("anchor", "message:" + m.id);
  return url.toString();
}
