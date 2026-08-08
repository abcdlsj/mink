import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { act, cleanup, fireEvent, render, screen } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";

import type { Message, ThreadRead } from "../../api/client";
import { ThreadPane } from "./ThreadPane";

afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
});

describe("ThreadPane latest message scroll", () => {
  it("auto-scrolls to the latest reply when it arrives near the bottom", async () => {
    const threadId = "thread-1";
    let threadData: ThreadRead = threadRead(threadId, []);
    const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
    vi.stubGlobal("fetch", vi.fn(async (input: RequestInfo | URL) => {
      const path = String(input);
      if (path.endsWith(`/api/v1/threads/${threadId}`)) return json(threadData);
      throw new Error(`Unexpected request: ${path}`);
    }));

    const { container } = renderThread(queryClient, threadId);
    await screen.findByText("Root message");
    const scroll = container.querySelector(".thread-messages") as HTMLElement;
    Object.defineProperty(scroll, "clientHeight", { configurable: true, value: 100 });
    Object.defineProperty(scroll, "scrollHeight", { configurable: true, value: 300 });
    scroll.scrollTop = 200;
    fireEvent.scroll(scroll);

    threadData = threadRead(threadId, [replyMessage("reply-1", "Reply one")]);
    Object.defineProperty(scroll, "scrollHeight", { configurable: true, value: 400 });
    await act(async () => {
      await queryClient.invalidateQueries({ queryKey: ["thread", threadId] });
    });

    await screen.findByText("Reply one");
    expect(scroll.scrollTop).toBe(400);
  });

  it("keeps the scroll position and shows To bottom when a new reply arrives far above the bottom", async () => {
    const threadId = "thread-1";
    let threadData: ThreadRead = threadRead(threadId, []);
    const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
    vi.stubGlobal("fetch", vi.fn(async (input: RequestInfo | URL) => {
      const path = String(input);
      if (path.endsWith(`/api/v1/threads/${threadId}`)) return json(threadData);
      throw new Error(`Unexpected request: ${path}`);
    }));

    const { container } = renderThread(queryClient, threadId);
    await screen.findByText("Root message");
    const scroll = container.querySelector(".thread-messages") as HTMLElement;
    Object.defineProperty(scroll, "clientHeight", { configurable: true, value: 100 });
    Object.defineProperty(scroll, "scrollHeight", { configurable: true, value: 300 });
    scroll.scrollTop = 60;
    fireEvent.scroll(scroll);

    threadData = threadRead(threadId, [replyMessage("reply-1", "Reply one")]);
    Object.defineProperty(scroll, "scrollHeight", { configurable: true, value: 400 });
    await act(async () => {
      await queryClient.invalidateQueries({ queryKey: ["thread", threadId] });
    });

    await screen.findByText("Reply one");
    expect(scroll.scrollTop).toBe(60);
    const button = screen.getByRole("button", { name: "Go to latest message" });
    expect(button).toBeVisible();
    fireEvent.click(button);
    expect(scroll.scrollTop).toBe(400);
    expect(screen.queryByRole("button", { name: "Go to latest message" })).not.toBeInTheDocument();
  });
});

function renderThread(queryClient: QueryClient, threadId: string) {
  return render(
    <QueryClientProvider client={queryClient}>
      <ThreadPane
        channelId="channel-1"
        spaceId="space-1"
        threadId={threadId}
        channelSlug="general"
        spaceSlug="sumi-lab"
        members={[]}
        latestMainMessageSeq={0}
        openedAtMainSeq={0}
        paneWidth={360}
        paneMaxWidth={480}
        startResize={vi.fn()}
        resizeWithKeyboard={vi.fn()}
        close={vi.fn()}
        showLatestChannelMessages={vi.fn()}
        activityByMemberId={new Map()}
      />
    </QueryClientProvider>,
  );
}

function threadRead(threadId: string, replies: Message[]): ThreadRead {
  return {
    thread_id: threadId,
    channel_id: "channel-1",
    snapshot_channel_seq: replies.length + 1,
    is_following: false,
    root: message("root-1", 1, "Root message"),
    replies,
  };
}

function replyMessage(id: string, body: string): Message {
  return message(id, 2, body);
}

function message(id: string, seq: number, body: string): Message {
  return {
    id,
    channel_id: "channel-1",
    seq,
    author: { id: "human-ada", kind: "human", display_name: "Ada" },
    placement: "root",
    content: { type: "text", body_markdown: body },
    mentions: [],
    mention_all: false,
    attachments: [],
    attention_failures: [],
    task_refs: [],
    thread_id: "thread-1",
    created_at: "2026-07-25T12:00:00Z",
    reply_count: 0,
  };
}

function json(body: unknown, status = 200): Response {
  return new Response(JSON.stringify(body), {
    status,
    headers: { "Content-Type": "application/json" },
  });
}
