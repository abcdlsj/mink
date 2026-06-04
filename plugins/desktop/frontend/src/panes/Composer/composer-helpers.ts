import type { AgentItem } from "@/lib/types";

export type MentionState = { start: number; query: string };
export type MentionCandidate = { id: string; display: string };

export function nextMentionState(value: string, caret: number): MentionState | null {
  const before = value.slice(0, caret);
  const at = before.lastIndexOf("@");
  if (at < 0) return null;
  const prevChar = at === 0 ? "" : before[at - 1];
  if (prevChar !== "" && !/\s/.test(prevChar)) return null;
  const query = before.slice(at + 1);
  if (/\s/.test(query)) return null;
  return { start: at, query };
}

export function mentionCandidates(agents: AgentItem[], mentionState: MentionState | null): MentionCandidate[] {
  if (!mentionState) return [];
  const q = mentionState.query.toLowerCase();
  return agents
    .filter((a) => {
      if (q === "") return true;
      return (
        a.id.toLowerCase().includes(q) || (a.display || "").toLowerCase().includes(q)
      );
    })
    .slice(0, 6);
}

export function applyMention(
  input: string,
  mentionState: MentionState,
  choice: MentionCandidate,
): { next: string; caret: number } {
  const before = input.slice(0, mentionState.start);
  const afterStart = mentionState.start + 1 + mentionState.query.length;
  const tail = input.slice(afterStart);
  const inserted = "@" + choice.id + (tail.startsWith(" ") ? "" : " ");
  const next = before + inserted + tail;
  const caret = before.length + inserted.length;
  return { next, caret };
}
