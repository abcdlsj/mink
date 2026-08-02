import { cleanup, fireEvent, render, screen } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { afterEach, describe, expect, it, vi } from "vitest";

import { ExpandableMessageText, MessageTimeline } from "./MessageTimeline";
import type { Message } from "../../api/client";

afterEach(() => { cleanup(); vi.restoreAllMocks(); });

describe("ExpandableMessageText", () => {
  it("expands and collapses text that exceeds eight rendered lines", () => {
    vi.spyOn(HTMLElement.prototype, "scrollHeight", "get").mockReturnValue(240);
    vi.spyOn(HTMLElement.prototype, "clientHeight", "get").mockReturnValue(120);

    render(
      <ExpandableMessageText
        messageId="message-1"
        body={Array.from({ length: 10 }, (_, index) => `Line ${index + 1}`).join("\n")}
        mentionedHandles={new Set()}
      />,
    );

    const body = screen.getByText(/Line 1/);
    const toggle = screen.getByRole("button", { name: "Show more" });
    expect(body).toHaveClass("message-body--collapsed");
    fireEvent.click(toggle);
    expect(body).toHaveClass("message-body--expanded");
    expect(screen.getByRole("button", { name: "Show less" })).toHaveAttribute("aria-expanded", "true");
  });
});

describe("highlightMentions", () => {
  it("highlights only recognized handles at mention boundaries", () => {
    const { container } = render(
      <ExpandableMessageText
        messageId="message-mentions"
        body="@Lin please check email@lin, @lincoln, and (@Lin)."
        mentionedHandles={new Set(["lin"])}
      />,
    );

    expect(container.querySelectorAll("mark.message-mention")).toHaveLength(1);
    expect(container.querySelector("mark.message-mention")).toHaveTextContent("@Lin");
    expect(container).toHaveTextContent("@Lin please check email@lin, @lincoln, and (@Lin).");
  });

  it("does not infer a mention from text when no structured handle is present", () => {
    const { container } = render(
      <ExpandableMessageText
        messageId="message-unrecognized-mention"
        body="Please check @lin."
        mentionedHandles={new Set()}
      />,
    );

    expect(container.querySelector("mark.message-mention")).not.toBeInTheDocument();
    expect(container).toHaveTextContent("Please check @lin.");
  });
});

const citation = {
  answer_start: 1,
  answer_end: 3,
  source_message_id: "source-message",
  source_start: 0,
  source_end: 8,
  source_thread_id: "source-thread",
  source_channel_id: "channel",
  source_author: { id: "grace", kind: "human", display_name: "Grace Hopper", handle: "grace" },
  source_excerpt: "The source context.",
} as const;

describe("context citations", () => {
  it("opens on keyboard focus, lists sources, and clears on blur", () => {
    const onFocus = vi.fn();
    render(
      <ExpandableMessageText
        messageId="citation-message"
        body="前😀引用"
        mentionedHandles={new Set()}
        contextCitations={[citation]}
        onCitationFocus={onFocus}
      />,
    );

    const mark = screen.getByRole("button", { name: /Context citation/ });
    fireEvent.focus(mark);
    expect(screen.getByRole("dialog", { name: "Used context" })).toHaveTextContent("Grace Hopper @grace");
    expect(screen.getByRole("dialog", { name: "Used context" })).toHaveTextContent("The source context.");
    expect(onFocus).toHaveBeenLastCalledWith(["source-message"]);
    const sourceButton = screen.getByRole("button", { name: "Jump to source" });
    sourceButton.focus();
    fireEvent.keyDown(sourceButton, { key: "Escape" });
    expect(screen.queryByRole("dialog", { name: "Used context" })).not.toBeInTheDocument();
    expect(mark).toHaveFocus();
    fireEvent.focus(mark);
    fireEvent.blur(mark, { relatedTarget: document.body });
    expect(screen.queryByRole("dialog", { name: "Used context" })).not.toBeInTheDocument();
    expect(onFocus).toHaveBeenLastCalledWith(null);
  });

  it("highlights a visible source message while the citation is focused", () => {
    const source = messageFixture("source-message", "Source text");
    const answer = messageFixture("answer-message", "Answer😀引用", [citation]);
    render(
      <QueryClientProvider client={new QueryClient()}>
        <MessageTimeline
        timelineRef={{ current: null }}
        page={{ channel_id: "channel", has_more_after: false, has_more_before: false, snapshot_channel_seq: 2, messages: [source, answer] }}
        pending={false}
        error={null}
        retry={() => undefined}
        emptyTitle="Empty"
        channelId="channel"
        spaceSlug="space"
        openThread={() => undefined}
        activityByMemberId={new Map()}
        members={[]}
        />
      </QueryClientProvider>,
    );
    const mark = screen.getByRole("button", { name: /Context citation/ });
    fireEvent.focus(mark);
    expect(document.getElementById("message-source-message")).toHaveClass("message-block--context-source");
    fireEvent.blur(mark, { relatedTarget: document.body });
    expect(document.getElementById("message-source-message")).not.toHaveClass("message-block--context-source");
  });

  it("opens the source Thread when the source Message is outside the current timeline", () => {
    const openThread = vi.fn();
    render(
      <QueryClientProvider client={new QueryClient()}>
        <MessageTimeline
          timelineRef={{ current: null }}
          page={{ channel_id: "channel", has_more_after: false, has_more_before: false, snapshot_channel_seq: 2, messages: [messageFixture("answer-message", "Answer😀引用", [citation])] }}
          pending={false}
          error={null}
          retry={() => undefined}
          emptyTitle="Empty"
          channelId="channel"
          spaceSlug="space"
          openThread={openThread}
          activityByMemberId={new Map()}
          members={[]}
        />
      </QueryClientProvider>,
    );

    fireEvent.focus(screen.getByRole("button", { name: /Context citation/ }));
    fireEvent.click(screen.getByRole("button", { name: "Jump to source" }));
    expect(openThread).toHaveBeenCalledWith("source-thread");
  });
});

function messageFixture(id: string, body: string, contextCitations: Message["context_citations"] = []): Message {
  return {
    id,
    channel_id: "channel",
    thread_id: "thread",
    seq: id === "source-message" ? 1 : 2,
    placement: "root",
    created_at: "2026-08-02T00:00:00Z",
    edited_at: null,
    deleted_at: null,
    author: { id: "author", display_name: "Author", handle: "author", kind: "human" },
    content: { type: "text", body_markdown: body },
    mentions: [],
    mention_all: false,
    context_citations: contextCitations,
    attachments: [],
    attention_failures: [],
    reply_count: 0,
  };
}
