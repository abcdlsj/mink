import { type RefObject, useCallback, useEffect, useRef, useState } from "react";

export const NEAR_BOTTOM_SCREEN_RATIO = 0.75;

/**
 * Keeps a message scrollport at the latest message while the reader stays
 * near the bottom, and exposes a To bottom control once the reader scrolls
 * more than three quarters of a screen away from it.
 */
export function useLatestMessageScroll(
  scrollRef: RefObject<HTMLElement | null>,
  latestMessageId: string | undefined,
) {
  const [showToBottom, setShowToBottom] = useState(false);
  const nearBottomRef = useRef(false);
  const previousLatestIdRef = useRef<string | null>(null);

  const updateScrollState = useCallback(() => {
    const element = scrollRef.current;
    if (!element) return;
    const distanceFromBottom = element.scrollHeight - element.clientHeight - element.scrollTop;
    const nearBottom = distanceFromBottom <= element.clientHeight * NEAR_BOTTOM_SCREEN_RATIO;
    nearBottomRef.current = nearBottom;
    setShowToBottom(!nearBottom);
  }, [scrollRef]);

  const scrollToBottom = useCallback(() => {
    const element = scrollRef.current;
    if (!element) return;
    element.scrollTop = element.scrollHeight;
    nearBottomRef.current = true;
    setShowToBottom(false);
  }, [scrollRef]);

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
    updateScrollState();
    element.addEventListener("scroll", updateScrollState, { passive: true });
    return () => element.removeEventListener("scroll", updateScrollState);
  }, [latestMessageId, scrollRef, updateScrollState]);

  return { showToBottom, scrollToBottom };
}
