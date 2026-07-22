import type { Message } from "../../gen/sumi/space/v1/space_pb";
import type { MessagePage } from "./types";

export const MESSAGE_PAGE_LIMIT = 200;
export const MESSAGE_PAGE_BATCH = 5;

export async function loadMessagePages(
  fetchPage: (afterSequence: bigint, limit: number) => Promise<Message[]>,
  afterSequence = 0n,
  maxPages = MESSAGE_PAGE_BATCH,
): Promise<MessagePage> {
  let cursor = afterSequence;
  let collected: Message[] = [];
  for (let pageIndex = 0; pageIndex < maxPages; pageIndex += 1) {
    const page = await fetchPage(cursor, MESSAGE_PAGE_LIMIT);
    collected = mergeMessages(collected, page);
    if (page.length < MESSAGE_PAGE_LIMIT) {
      return {
        messages: collected,
        hasMore: false,
        nextAfterSequence: maxSequence(collected, cursor),
      };
    }
    const next = maxSequence(page, cursor);
    if (next <= cursor) {
      throw new Error("Message pagination did not advance");
    }
    cursor = next;
  }
  return { messages: collected, hasMore: true, nextAfterSequence: cursor };
}

export function mergeMessages(
  current: readonly Message[],
  incoming: readonly Message[],
): Message[] {
  const byId = new Map<string, Message>();
  const bySequence = new Map<bigint, string>();
  for (const message of [...current, ...incoming]) {
    const previous = byId.get(message.id);
    if (previous && previous.targetSequence !== message.targetSequence) {
      throw new Error(`Message ${message.id} changed sequence`);
    }
    const sequenceOwner = bySequence.get(message.targetSequence);
    if (sequenceOwner && sequenceOwner !== message.id) {
      throw new Error(
        `Messages ${sequenceOwner} and ${message.id} share sequence ${message.targetSequence}`,
      );
    }
    byId.set(message.id, message);
    bySequence.set(message.targetSequence, message.id);
  }
  return [...byId.values()].sort((left, right) =>
    left.targetSequence < right.targetSequence
      ? -1
      : left.targetSequence > right.targetSequence
        ? 1
        : left.id.localeCompare(right.id),
  );
}

function maxSequence(messages: readonly Message[], fallback: bigint) {
  return messages.reduce(
    (maximum, message) =>
      message.targetSequence > maximum ? message.targetSequence : maximum,
    fallback,
  );
}
