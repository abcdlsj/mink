const LAST_ROUTE_PREFIX = "sumi.lastConversation.";
const SCROLL_MEMORY_PREFIX = "sumi.channelScroll.";

export type ConversationRoute =
  | { kind: "channel"; channelSlug: string }
  | { kind: "dm"; memberId: string };

export interface ChannelScrollMemory {
  scrollTop: number;
  latestMessageId: string | null;
}

function isConversationRoute(value: unknown): value is ConversationRoute {
  if (!value || typeof value !== "object") return false;
  const candidate = value as { kind?: unknown; channelSlug?: unknown; memberId?: unknown };
  if (candidate.kind === "channel") {
    return typeof candidate.channelSlug === "string" && candidate.channelSlug.length > 0;
  }
  if (candidate.kind === "dm") {
    return typeof candidate.memberId === "string" && candidate.memberId.length > 0;
  }
  return false;
}

export function saveLastConversationRoute(spaceSlug: string, route: ConversationRoute): void {
  try {
    window.localStorage.setItem(`${LAST_ROUTE_PREFIX}${spaceSlug}`, JSON.stringify(route));
  } catch {
    // Route memory is a convenience; ignore storage failures.
  }
}

export function loadLastConversationRoute(spaceSlug: string): ConversationRoute | null {
  try {
    const raw = window.localStorage.getItem(`${LAST_ROUTE_PREFIX}${spaceSlug}`);
    if (!raw) return null;
    const parsed: unknown = JSON.parse(raw);
    return isConversationRoute(parsed) ? parsed : null;
  } catch {
    return null;
  }
}

export function readChannelScrollMemory(channelId: string): ChannelScrollMemory | null {
  try {
    const raw = window.localStorage.getItem(`${SCROLL_MEMORY_PREFIX}${channelId}`);
    if (!raw) return null;
    const parsed = JSON.parse(raw) as Partial<ChannelScrollMemory>;
    if (
      typeof parsed.scrollTop !== "number" ||
      !Number.isFinite(parsed.scrollTop) ||
      parsed.scrollTop < 0
    ) {
      return null;
    }
    return {
      scrollTop: parsed.scrollTop,
      latestMessageId: typeof parsed.latestMessageId === "string" ? parsed.latestMessageId : null,
    };
  } catch {
    return null;
  }
}

export function writeChannelScrollMemory(channelId: string, memory: ChannelScrollMemory): void {
  try {
    window.localStorage.setItem(`${SCROLL_MEMORY_PREFIX}${channelId}`, JSON.stringify(memory));
  } catch {
    // Scroll memory is a convenience; ignore storage failures.
  }
}
