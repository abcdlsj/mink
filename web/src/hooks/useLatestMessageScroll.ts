import { type RefObject, useCallback, useEffect, useRef, useState } from "react";

import { readChannelScrollMemory, writeChannelScrollMemory } from "../conversationMemory";

export const NEAR_BOTTOM_SCREEN_RATIO = 0.75;

export interface LatestMessageScrollOptions {
  memoryKey?: string;
  messageIds?: readonly string[];
}

export interface LatestMessageScrollApi {
  scrollToBottom: () => void;
  markLatestSeen: () => void;
}

/**
 * Keeps a message scrollport at the latest message while the reader stays
 * near the bottom, and exposes a To bottom control once the reader scrolls
 * more than three quarters of a screen away from it.
 *
 * With a memoryKey and messageIds, the hook also restores the last position
 * per conversation, counts messages that arrived after the last seen one, and
 * persists the position while the reader scrolls.
 */
export function useLatestMessageScroll(
  scrollRef: RefObject<HTMLElement | null>,
  latestMessageId: string | undefined,
  options?: LatestMessageScrollOptions,
): LatestMessageScrollApi & { showToBottom: boolean; newMessageCount: number } {
  const { memoryKey, messageIds } = options ?? {};
  const [showToBottom, setShowToBottom] = useState(false);
  const [baselineLatestId, setBaselineLatestId] = useState<string | null>(() =>
    memoryKey ? readChannelScrollMemory(memoryKey)?.latestMessageId ?? null : null,
  );
  const [seenMessageIdsKey, setSeenMessageIdsKey] = useState<string | undefined>(undefined);
  const nearBottomRef = useRef(false);
  const previousLatestIdRef = useRef<string | null>(null);
  const initializedRef = useRef(false);
  const latestMessageIdRef = useRef(latestMessageId);

  useEffect(() => {
    latestMessageIdRef.current = latestMessageId;
  }, [latestMessageId]);

  // First message page arrives after the initial render. Establish the
  // baseline once so later arrivals are counted from it.
  const messageIdsKey =
    messageIds === undefined ? undefined : `${messageIds.length}:${messageIds.at(-1) ?? ""}`;
  if (messageIdsKey !== seenMessageIdsKey) {
    setSeenMessageIdsKey(messageIdsKey);
    if (baselineLatestId === null && latestMessageId) {
      setBaselineLatestId(latestMessageId);
    }
  }

  const saveMemory = useCallback((scrollTop: number, baselineId: string | null) => {
    if (!memoryKey) return;
    writeChannelScrollMemory(memoryKey, { scrollTop, latestMessageId: baselineId });
  }, [memoryKey]);

  const markLatestSeen = useCallback(() => {
    const latest = latestMessageIdRef.current ?? null;
    setBaselineLatestId(latest);
    saveMemory(scrollRef.current?.scrollTop ?? 0, latest);
  }, [saveMemory, scrollRef]);

  const scrollToBottom = useCallback(() => {
    const element = scrollRef.current;
    if (element) element.scrollTop = element.scrollHeight;
    nearBottomRef.current = true;
    setShowToBottom(false);
    markLatestSeen();
  }, [markLatestSeen, scrollRef]);

  const updateScrollState = useCallback(() => {
    const element = scrollRef.current;
    if (!element) return;
    const distanceFromBottom = element.scrollHeight - element.clientHeight - element.scrollTop;
    const nearBottom = distanceFromBottom <= element.clientHeight * NEAR_BOTTOM_SCREEN_RATIO;
    nearBottomRef.current = nearBottom;
    setShowToBottom(!nearBottom);
    if (distanceFromBottom <= 0) {
      markLatestSeen();
    } else {
      saveMemory(element.scrollTop, baselineLatestId ?? latestMessageIdRef.current ?? null);
    }
  }, [baselineLatestId, markLatestSeen, saveMemory, scrollRef]);

  // Restore the saved position once, after the first message page is ready.
  // Without saved memory, land on the latest message.
  useEffect(() => {
    if (!memoryKey || messageIds === undefined || initializedRef.current) return;
    initializedRef.current = true;
    const element = scrollRef.current;
    const memory = readChannelScrollMemory(memoryKey);
    if (!memory) {
      if (element) element.scrollTop = element.scrollHeight;
      nearBottomRef.current = true;
      return;
    }
    if (element) {
      const maxScroll = element.scrollHeight - element.clientHeight;
      element.scrollTop = maxScroll <= 0 ? 0 : Math.min(memory.scrollTop, maxScroll);
    }
  }, [memoryKey, messageIds, scrollRef]);

  let newMessageCount = 0;
  if (messageIds && messageIds.length > 0 && baselineLatestId) {
    const baselineIndex = messageIds.indexOf(baselineLatestId);
    if (baselineIndex !== -1) {
      newMessageCount = Math.max(0, messageIds.length - baselineIndex - 1);
    }
  }

  // This effect must run before the scroll-state effect below: on a new
  // message the DOM already grew, so the near-bottom decision has to come
  // from the position recorded before the update.
  useEffect(() => {
    if (!latestMessageId) return;
    if (previousLatestIdRef.current === null) {
      previousLatestIdRef.current = latestMessageId;
      return;
    }
    const previousLatestId = previousLatestIdRef.current;
    previousLatestIdRef.current = latestMessageId;
    if (previousLatestId === latestMessageId || !nearBottomRef.current) return;
    scrollToBottom();
  }, [latestMessageId, scrollRef, scrollToBottom]);

  useEffect(() => {
    const element = scrollRef.current;
    if (!element) return;
    if (memoryKey && messageIds === undefined) return;
    updateScrollState();
    element.addEventListener("scroll", updateScrollState, { passive: true });
    return () => element.removeEventListener("scroll", updateScrollState);
  }, [latestMessageId, memoryKey, messageIds, scrollRef, updateScrollState]);

  return { showToBottom, newMessageCount, scrollToBottom, markLatestSeen };
}
