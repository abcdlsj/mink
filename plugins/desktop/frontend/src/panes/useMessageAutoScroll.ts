import { useEffect, useMemo, useRef } from "react";
import type { MutableRefObject } from "react";
import type { MessageView } from "@/lib/types";

const NEAR_BOTTOM_PX = 180;
const FOLLOW_THROTTLE_MS = 120;

export function useMessageAutoScroll(messages: MessageView[], scope: string) {
  const scrollRef = useRef<HTMLDivElement | null>(null);
  const lastScopeRef = useRef("");
  const lastMessageIDRef = useRef("");
  const followRef = useRef(true);
  const lastFollowAtRef = useRef(0);
  const followTimerRef = useRef<number | null>(null);
  const ignoreScrollUntilRef = useRef(0);

  const signal = useMemo(() => {
    const last = messages[messages.length - 1];
    if (!last) return "empty";
    const eventSignal = (last.events || [])
      .map((ev) => `${ev.kind}:${ev.status}:${ev.output?.length || 0}:${ev.reply?.length || 0}`)
      .join("|");
    return [
      messages.length,
      last.id,
      last.role,
      last.content?.length || 0,
      last.reasoning?.length || 0,
      eventSignal,
    ].join(":");
  }, [messages]);

  useEffect(() => {
    return () => {
      if (followTimerRef.current !== null) {
        window.clearTimeout(followTimerRef.current);
      }
    };
  }, []);

  const onScroll = () => {
    if (Date.now() < ignoreScrollUntilRef.current) return;
    const el = scrollRef.current;
    if (!el) return;
    followRef.current = isNearBottom(el);
  };

  useEffect(() => {
    const el = scrollRef.current;
    if (!el) return;
    const last = messages[messages.length - 1];

    if (lastScopeRef.current !== scope) {
      el.scrollTop = el.scrollHeight;
      lastScopeRef.current = scope;
      lastMessageIDRef.current = last?.id || "";
      followRef.current = true;
      return;
    }

    if (!last) return;
    const isNewMessage = last.id !== lastMessageIDRef.current;
    if (isNewMessage) {
      lastMessageIDRef.current = last.id;
      if (last.role === "user") {
        scrollMessageToTop(last.id);
        followRef.current = true;
        return;
      }
      if (followRef.current) {
        scrollToBottom(el, true, lastFollowAtRef, followTimerRef);
      }
      return;
    }

    if (followRef.current) {
      scrollToBottom(el, false, lastFollowAtRef, followTimerRef);
    }
  }, [scope, signal, messages]);

  const scrollMessageToTop = (id: string) => {
    ignoreScrollUntilRef.current = Date.now() + 250;
    window.requestAnimationFrame(() => {
      document.getElementById("message-" + id)?.scrollIntoView({
        block: "start",
      });
      window.setTimeout(() => {
        followRef.current = true;
      }, 0);
    });
  };

  return { scrollRef, onScroll };
}

function isNearBottom(el: HTMLDivElement): boolean {
  return el.scrollHeight - el.scrollTop - el.clientHeight < NEAR_BOTTOM_PX;
}

function scrollToBottom(
  el: HTMLDivElement,
  force: boolean,
  lastFollowAtRef: MutableRefObject<number>,
  followTimerRef: MutableRefObject<number | null>,
) {
  const now = Date.now();
  const delay = FOLLOW_THROTTLE_MS - (now - lastFollowAtRef.current);
  if (!force && delay > 0) {
    if (followTimerRef.current !== null) {
      window.clearTimeout(followTimerRef.current);
    }
    followTimerRef.current = window.setTimeout(() => {
      followTimerRef.current = null;
      scrollToBottom(el, true, lastFollowAtRef, followTimerRef);
    }, delay);
    return;
  }
  lastFollowAtRef.current = now;
  window.requestAnimationFrame(() => {
    el.scrollTop = el.scrollHeight;
  });
}
