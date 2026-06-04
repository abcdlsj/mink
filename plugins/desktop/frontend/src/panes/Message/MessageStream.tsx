import type { ReactNode } from "react";
import type { MessageView } from "@/lib/types";
import { MessageRow } from "./MessageRow";
import { renderableMessage } from "./message-helpers";

export function MessageStream({
  messages,
  empty,
  filterRenderable = true,
  compactAcrossThreadLinks = false,
}: {
  messages: MessageView[];
  empty: ReactNode;
  filterRenderable?: boolean;
  compactAcrossThreadLinks?: boolean;
}) {
  const visible = filterRenderable ? messages.filter(renderableMessage) : messages;
  if (visible.length === 0) return <>{empty}</>;
  return visible.map((m, i) => {
    const prev = visible[i - 1];
    const sameAuthor =
      prev && prev.role === m.role && (prev.author_id || "") === (m.author_id || "");
    const close =
      prev && new Date(m.time).getTime() - new Date(prev.time).getTime() < 5 * 60 * 1000;
    const compact =
      sameAuthor &&
      close &&
      (compactAcrossThreadLinks || !m.thread_id) &&
      !(m.events && m.events.length);
    return <MessageRow key={m.id} m={m} compact={!!compact} />;
  });
}
