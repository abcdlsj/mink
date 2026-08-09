/// <reference types="node" />

import { cleanup, fireEvent, render, screen, within } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { readFileSync } from "node:fs";
import { join } from "node:path";
import { createRef } from "react";
import { afterEach, describe, expect, it, vi } from "vitest";

import type { Message, MessagePage } from "../../api/client";
import { ExpandableMessageText, MessageTimeline } from "./MessageTimeline";

afterEach(() => {
  cleanup();
  vi.restoreAllMocks();
  window.localStorage.clear();
});

describe("System Notice timeline", () => {
  it("renders one notice as body copy without a separator", () => {
    const { container } = renderSystemNotices([
      systemNotice(1, "Lin joined the channel", "2026-07-25T12:00:00Z"),
    ]);

    const copy = screen.getByText((content, element) => Boolean(element?.classList.contains("system-event--message")) && element?.textContent === "Lin joined the channel");
    expect(copy.tagName).toBe("P");
    expect(copy).toHaveClass("system-event--message");
    expect(container.querySelector(".system-event-heading")).not.toBeInTheDocument();
    expect(screen.queryByRole("button", { name: /system notices/i })).not.toBeInTheDocument();
  });

  it("folds consecutive notices from the same day and exposes the expanded state", () => {
    renderSystemNotices([
      systemNotice(1, "Lin joined the channel", "2026-07-25T12:00:00Z"),
      systemNotice(2, "Reviewer joined the channel", "2026-07-25T12:01:00Z"),
    ]);

    const toggle = screen.getByRole("button", { name: "Show 2 system notices" });
    expect(toggle).toHaveAttribute("aria-expanded", "false");
    expect(screen.getByText((content, element) => Boolean(element?.classList.contains("system-event--message")) && element?.textContent === "Lin joined the channel")).not.toBeVisible();
    expect(screen.getByText((content, element) => Boolean(element?.classList.contains("system-event--message")) && element?.textContent === "Reviewer joined the channel")).not.toBeVisible();

    fireEvent.click(toggle);

    expect(screen.getByRole("button", { name: "Hide 2 system notices" })).toHaveAttribute("aria-expanded", "true");
    expect(screen.getByText((content, element) => Boolean(element?.classList.contains("system-event--message")) && element?.textContent === "Lin joined the channel")).toBeVisible();
    expect(screen.getByText((content, element) => Boolean(element?.classList.contains("system-event--message")) && element?.textContent === "Reviewer joined the channel")).toBeVisible();
  });

  it("keeps notices from different days in separate visible rows", () => {
    renderSystemNotices([
      systemNotice(1, "Lin joined the channel", "2026-07-25T12:00:00Z"),
      systemNotice(2, "Reviewer joined the channel", "2026-07-26T12:00:00Z"),
    ]);

    expect(screen.queryByRole("button", { name: /system notices/i })).not.toBeInTheDocument();
    expect(screen.getByText((content, element) => Boolean(element?.classList.contains("system-event--message")) && element?.textContent === "Lin joined the channel")).toBeVisible();
    expect(screen.getByText((content, element) => Boolean(element?.classList.contains("system-event--message")) && element?.textContent === "Reviewer joined the channel")).toBeVisible();
  });

  it("resets Message identity grouping after a System Notice", () => {
    const notice = systemNotice(1, "Sumi joined the channel", "2026-07-25T12:00:00Z");
    const message: Message = {
      ...notice,
      id: "message-2",
      seq: 2,
      content: { type: "text", body_markdown: "Hello after the notice" },
      created_at: "2026-07-25T12:01:00Z",
    };

    const { container } = renderSystemNotices([notice, message]);
    const row = container.querySelector('[data-message-id="message-2"] .message-row');

    expect(row).not.toHaveClass("message-row--grouped");
    expect(screen.getByRole("img", { name: "Sumi avatar" })).toBeVisible();
  });
});

describe("ExpandableMessageText", () => {
  it("expands and collapses text that exceeds eight rendered lines", () => {
    vi.spyOn(HTMLElement.prototype, "scrollHeight", "get").mockReturnValue(240);
    vi.spyOn(HTMLElement.prototype, "clientHeight", "get").mockReturnValue(120);

    const { container } = render(
      <ExpandableMessageText
        messageId="message-1"
        body={Array.from({ length: 10 }, (_, index) => `Line ${index + 1}`).join("\n")}
        mentionedMembers={new Map()} taskRefs={new Map()} spaceSlug="sumi-lab"
      />,
    );

    const body = container.querySelector(".message-body") as HTMLElement;
    const toggle = screen.getByRole("button", { name: "Show more" });
    expect(body).toHaveClass("message-body--collapsed");
    fireEvent.click(toggle);
    expect(body).toHaveClass("message-body--expanded");
    expect(screen.getByRole("button", { name: "Show less" })).toHaveAttribute("aria-expanded", "true");
  });
});

describe("agent avatar DM shortcut", () => {
  it("opens the DM of an agent author when the avatar is clicked", () => {
    const onOpenAgentDm = vi.fn();
    render(
      <QueryClientProvider client={new QueryClient()}>
        <MessageTimeline
          timelineRef={createRef<HTMLDivElement>()}
          page={pageWith([agentMessage(1, "Lin")])}
          pending={false}
          error={null}
          retry={vi.fn()}
          emptyTitle="No messages"
          channelId="channel-1"
          spaceSlug="sumi-lab"
          openThread={vi.fn()}
          activityByMemberId={new Map()}
          members={[]}
          onOpenAgentDm={onOpenAgentDm}
        />
      </QueryClientProvider>,
    );

    const avatar = screen.getByRole("button", { name: "Open DM with Lin" });
    expect(within(avatar).getByRole("img", { name: "Lin avatar" })).toBeVisible();
    fireEvent.click(avatar);
    expect(onOpenAgentDm).toHaveBeenCalledWith("agent-lin");
  });

  it("keeps avatars inert for human authors, in DMs, and without a handler", () => {
    const onOpenAgentDm = vi.fn();
    const { rerender } = render(
      <QueryClientProvider client={new QueryClient()}>
        <MessageTimeline
          timelineRef={createRef<HTMLDivElement>()}
          page={pageWith([humanMessage(1)])}
          pending={false}
          error={null}
          retry={vi.fn()}
          emptyTitle="No messages"
          channelId="channel-1"
          spaceSlug="sumi-lab"
          openThread={vi.fn()}
          activityByMemberId={new Map()}
          members={[]}
          onOpenAgentDm={onOpenAgentDm}
        />
      </QueryClientProvider>,
    );
    expect(screen.queryByRole("button", { name: /Open DM with/ })).not.toBeInTheDocument();

    rerender(
      <QueryClientProvider client={new QueryClient()}>
        <MessageTimeline
          timelineRef={createRef<HTMLDivElement>()}
          page={pageWith([agentMessage(1, "Lin")])}
          pending={false}
          error={null}
          retry={vi.fn()}
          emptyTitle="No messages"
          channelId="channel-1"
          spaceSlug="sumi-lab"
          openThread={vi.fn()}
          activityByMemberId={new Map()}
          members={[]}
          direct
          onOpenAgentDm={onOpenAgentDm}
        />
      </QueryClientProvider>,
    );
    expect(screen.queryByRole("button", { name: /Open DM with/ })).not.toBeInTheDocument();

    rerender(
      <QueryClientProvider client={new QueryClient()}>
        <MessageTimeline
          timelineRef={createRef<HTMLDivElement>()}
          page={pageWith([agentMessage(1, "Lin")])}
          pending={false}
          error={null}
          retry={vi.fn()}
          emptyTitle="No messages"
          channelId="channel-1"
          spaceSlug="sumi-lab"
          openThread={vi.fn()}
          activityByMemberId={new Map()}
          members={[]}
        />
      </QueryClientProvider>,
    );
    expect(screen.queryByRole("button", { name: /Open DM with/ })).not.toBeInTheDocument();
  });
});

describe("timeline position", () => {
  it("shows a jump to the latest message after scrolling more than three quarters of a screen up", () => {
    const timelineRef = createRef<HTMLDivElement>();
    render(
      <QueryClientProvider client={new QueryClient()}>
        <MessageTimeline
          timelineRef={timelineRef}
          page={pageWith([humanMessage(1)])}
          pending={false}
          error={null}
          retry={vi.fn()}
          emptyTitle="No messages"
          channelId="channel-1"
          spaceSlug="sumi-lab"
          openThread={vi.fn()}
          activityByMemberId={new Map()}
          members={[]}
        />
      </QueryClientProvider>,
    );

    const timeline = timelineRef.current!;
    Object.defineProperty(timeline, "clientHeight", { configurable: true, value: 100 });
    Object.defineProperty(timeline, "scrollHeight", { configurable: true, value: 350 });
    timeline.scrollTop = 0;
    fireEvent.scroll(timeline);

    const button = screen.getByRole("button", { name: "Go to latest message" });
    expect(button).toHaveTextContent("To bottom");
    fireEvent.click(button);
    expect(timeline.scrollTop).toBe(350);
    expect(screen.queryByRole("button", { name: "Go to latest message" })).not.toBeInTheDocument();
  });

  it("hides the jump button within three quarters of the latest message", () => {
    const timelineRef = createRef<HTMLDivElement>();
    render(
      <QueryClientProvider client={new QueryClient()}>
        <MessageTimeline
          timelineRef={timelineRef}
          page={pageWith([humanMessage(1)])}
          pending={false}
          error={null}
          retry={vi.fn()}
          emptyTitle="No messages"
          channelId="channel-1"
          spaceSlug="sumi-lab"
          openThread={vi.fn()}
          activityByMemberId={new Map()}
          members={[]}
        />
      </QueryClientProvider>,
    );

    const timeline = timelineRef.current!;
    Object.defineProperty(timeline, "clientHeight", { configurable: true, value: 100 });
    Object.defineProperty(timeline, "scrollHeight", { configurable: true, value: 350 });
    timeline.scrollTop = 200;
    fireEvent.scroll(timeline);

    expect(screen.queryByRole("button", { name: "Go to latest message" })).not.toBeInTheDocument();
  });

  it("auto-scrolls to the latest message when a new message arrives near the bottom", () => {
    const timelineRef = createRef<HTMLDivElement>();
    const { rerender } = render(
      <QueryClientProvider client={new QueryClient()}>
        <MessageTimeline
          timelineRef={timelineRef}
          page={pageWith([humanMessage(1)])}
          pending={false}
          error={null}
          retry={vi.fn()}
          emptyTitle="No messages"
          channelId="channel-1"
          spaceSlug="sumi-lab"
          openThread={vi.fn()}
          activityByMemberId={new Map()}
          members={[]}
        />
      </QueryClientProvider>,
    );

    const timeline = timelineRef.current!;
    Object.defineProperty(timeline, "clientHeight", { configurable: true, value: 100 });
    Object.defineProperty(timeline, "scrollHeight", { configurable: true, value: 250 });
    timeline.scrollTop = 150;
    fireEvent.scroll(timeline);
    Object.defineProperty(timeline, "scrollHeight", { configurable: true, value: 500 });

    rerender(
      <QueryClientProvider client={new QueryClient()}>
        <MessageTimeline
          timelineRef={timelineRef}
          page={pageWith([humanMessage(1), humanMessage(2)])}
          pending={false}
          error={null}
          retry={vi.fn()}
          emptyTitle="No messages"
          channelId="channel-1"
          spaceSlug="sumi-lab"
          openThread={vi.fn()}
          activityByMemberId={new Map()}
          members={[]}
        />
      </QueryClientProvider>,
    );

    expect(timeline.scrollTop).toBe(500);
  });

  it("keeps the scroll position and shows the jump button when a new message arrives far above the bottom", () => {
    const timelineRef = createRef<HTMLDivElement>();
    const { rerender } = render(
      <QueryClientProvider client={new QueryClient()}>
        <MessageTimeline
          timelineRef={timelineRef}
          page={pageWith([humanMessage(1)])}
          pending={false}
          error={null}
          retry={vi.fn()}
          emptyTitle="No messages"
          channelId="channel-1"
          spaceSlug="sumi-lab"
          openThread={vi.fn()}
          activityByMemberId={new Map()}
          members={[]}
        />
      </QueryClientProvider>,
    );

    const timeline = timelineRef.current!;
    Object.defineProperty(timeline, "clientHeight", { configurable: true, value: 100 });
    Object.defineProperty(timeline, "scrollHeight", { configurable: true, value: 250 });
    timeline.scrollTop = 50;
    fireEvent.scroll(timeline);
    Object.defineProperty(timeline, "scrollHeight", { configurable: true, value: 500 });

    rerender(
      <QueryClientProvider client={new QueryClient()}>
        <MessageTimeline
          timelineRef={timelineRef}
          page={pageWith([humanMessage(1), humanMessage(2)])}
          pending={false}
          error={null}
          retry={vi.fn()}
          emptyTitle="No messages"
          channelId="channel-1"
          spaceSlug="sumi-lab"
          openThread={vi.fn()}
          activityByMemberId={new Map()}
          members={[]}
        />
      </QueryClientProvider>,
    );

    expect(timeline.scrollTop).toBe(50);
    const button = screen.getByRole("button", { name: /1 new message/ });
    expect(button).toHaveTextContent("To bottom");
    expect(button).toHaveTextContent("1");
  });

  it("restores the saved position and shows messages after it on To bottom", () => {
    mockScrollMetrics(500, 100);
    window.localStorage.setItem(
      "sumi.channelScroll.channel-1",
      JSON.stringify({ scrollTop: 120, latestMessageId: "human-message-1" }),
    );
    const timelineRef = createRef<HTMLDivElement>();
    render(
      <QueryClientProvider client={new QueryClient()}>
        <MessageTimeline
          timelineRef={timelineRef}
          page={pageWith([humanMessage(1), humanMessage(2), humanMessage(3)])}
          pending={false}
          error={null}
          retry={vi.fn()}
          emptyTitle="No messages"
          channelId="channel-1"
          spaceSlug="sumi-lab"
          openThread={vi.fn()}
          activityByMemberId={new Map()}
          members={[]}
        />
      </QueryClientProvider>,
    );

    const timeline = timelineRef.current!;
    expect(timeline.scrollTop).toBe(120);
    const button = screen.getByRole("button", { name: /2 new messages/ });
    expect(button).toHaveTextContent("To bottom");
    expect(button).toHaveTextContent("2");

    fireEvent.click(button);

    expect(timeline.scrollTop).toBe(500);
    expect(screen.queryByRole("button", { name: /new messages/ })).not.toBeInTheDocument();
  });

  it("does not overwrite saved memory while the first page is loading", () => {
    mockScrollMetrics(500, 100);
    window.localStorage.setItem(
      "sumi.channelScroll.channel-1",
      JSON.stringify({ scrollTop: 120, latestMessageId: "human-message-1" }),
    );
    const timelineRef = createRef<HTMLDivElement>();
    const { rerender } = render(
      <QueryClientProvider client={new QueryClient()}>
        <MessageTimeline
          timelineRef={timelineRef}
          pending
          error={null}
          retry={vi.fn()}
          emptyTitle="No messages"
          channelId="channel-1"
          spaceSlug="sumi-lab"
          openThread={vi.fn()}
          activityByMemberId={new Map()}
          members={[]}
        />
      </QueryClientProvider>,
    );

    rerender(
      <QueryClientProvider client={new QueryClient()}>
        <MessageTimeline
          timelineRef={timelineRef}
          page={pageWith([humanMessage(1), humanMessage(2), humanMessage(3)])}
          pending={false}
          error={null}
          retry={vi.fn()}
          emptyTitle="No messages"
          channelId="channel-1"
          spaceSlug="sumi-lab"
          openThread={vi.fn()}
          activityByMemberId={new Map()}
          members={[]}
        />
      </QueryClientProvider>,
    );

    expect(timelineRef.current!.scrollTop).toBe(120);
    expect(screen.getByRole("button", { name: /2 new messages/ })).toBeVisible();
  });

  it("lands on the latest message when no saved position exists", () => {
    mockScrollMetrics(500, 100);
    const timelineRef = createRef<HTMLDivElement>();
    render(
      <QueryClientProvider client={new QueryClient()}>
        <MessageTimeline
          timelineRef={timelineRef}
          page={pageWith([humanMessage(1), humanMessage(2)])}
          pending={false}
          error={null}
          retry={vi.fn()}
          emptyTitle="No messages"
          channelId="channel-1"
          spaceSlug="sumi-lab"
          openThread={vi.fn()}
          activityByMemberId={new Map()}
          members={[]}
        />
      </QueryClientProvider>,
    );

    expect(timelineRef.current!.scrollTop).toBe(500);
    expect(screen.queryByRole("button", { name: /new messages/ })).not.toBeInTheDocument();
  });

  it("persists the scroll position while reading older messages", () => {
    mockScrollMetrics(500, 100);
    const timelineRef = createRef<HTMLDivElement>();
    render(
      <QueryClientProvider client={new QueryClient()}>
        <MessageTimeline
          timelineRef={timelineRef}
          page={pageWith([humanMessage(1), humanMessage(2)])}
          pending={false}
          error={null}
          retry={vi.fn()}
          emptyTitle="No messages"
          channelId="channel-1"
          spaceSlug="sumi-lab"
          openThread={vi.fn()}
          activityByMemberId={new Map()}
          members={[]}
        />
      </QueryClientProvider>,
    );

    const timeline = timelineRef.current!;
    timeline.scrollTop = 150;
    fireEvent.scroll(timeline);

    const saved = JSON.parse(window.localStorage.getItem("sumi.channelScroll.channel-1")!);
    expect(saved.scrollTop).toBe(150);
    expect(saved.latestMessageId).toBe("human-message-2");
  });
});

function mockScrollMetrics(scrollHeight: number, clientHeight: number) {
  vi.spyOn(HTMLElement.prototype, "scrollHeight", "get").mockReturnValue(scrollHeight);
  vi.spyOn(HTMLElement.prototype, "clientHeight", "get").mockReturnValue(clientHeight);
}

function renderSystemNotices(messages: Message[]) {
  const page = pageWith(messages);
  return render(
    <QueryClientProvider client={new QueryClient()}>
      <MessageTimeline
        timelineRef={createRef<HTMLDivElement>()}
        page={page}
        pending={false}
        error={null}
        retry={vi.fn()}
        emptyTitle="No messages"
        channelId="channel-1"
        spaceSlug="sumi-lab"
        openThread={vi.fn()}
        activityByMemberId={new Map()}
        members={[]}
      />
    </QueryClientProvider>,
  );
}

function pageWith(messages: Message[]): MessagePage {
  return {
    channel_id: "channel-1",
    snapshot_channel_seq: messages.length,
    messages,
    has_more_before: false,
    has_more_after: false,
  };
}

function systemNotice(seq: number, body: string, createdAt: string): Message {
  return {
    id: `notice-${seq}`,
    channel_id: "channel-1",
    seq,
    author: { id: "system", kind: "human", display_name: "Sumi" },
    placement: "root",
    content: { type: "system_notice", body_markdown: body },
    mentions: [],
    mention_all: false,
    attachments: [],
    attention_failures: [],
    task_refs: [],
    thread_id: `thread-${seq}`,
    created_at: createdAt,
    reply_count: 0,
  };
}

function agentMessage(seq: number, displayName: string): Message {
  return {
    ...systemNotice(seq, "Hello", "2026-07-25T12:00:00Z"),
    id: `agent-message-${seq}`,
    author: { id: "agent-lin", kind: "agent", display_name: displayName },
    content: { type: "text", body_markdown: "Hello" },
  };
}

function humanMessage(seq: number): Message {
  return {
    ...systemNotice(seq, "Hello", "2026-07-25T12:00:00Z"),
    id: `human-message-${seq}`,
    author: { id: "human-ada", kind: "human", display_name: "Ada" },
    content: { type: "text", body_markdown: "Hello" },
  };
}

describe("collapsed message body style", () => {
  it("clamps by height so overflow detection can show the toggle", () => {
    const css = readFileSync(join(process.cwd(), "src/styles.css"), "utf8");
    const rule = css.match(/\.message-body--collapsed\s*\{([^}]*)\}/)?.[1];
    expect(rule).toBeDefined();
    expect(rule).not.toMatch(/display:\s*-webkit-box/);
    expect(rule).toMatch(/max-height:\s*11\.2em/);
    expect(rule).toMatch(/overflow:\s*hidden/);
  });
});

describe("highlightMentions", () => {
  it("highlights only recognized display names at mention boundaries", () => {
    const { container } = render(
      <ExpandableMessageText
        messageId="message-mentions"
        body="@Lin please check email@lin, @lincoln, and (@Lin)."
        mentionedMembers={new Map([["lin", { id: "human-lin", kind: "human", display_name: "Lin" }]])} taskRefs={new Map()} spaceSlug="sumi-lab"
      />,
    );

    expect(container.querySelectorAll("mark.message-mention")).toHaveLength(2);
    expect(container.querySelectorAll("mark.message-mention")[0]).toHaveTextContent("@Lin");
    expect(container.querySelectorAll("mark.message-mention")[1]).toHaveTextContent("@Lin");
    expect(container).toHaveTextContent("@Lin please check email@lin, @lincoln, and (@Lin).");
  });

  it("does not infer a mention from text when no structured display name is present", () => {
    const { container } = render(
      <ExpandableMessageText
        messageId="message-unrecognized-mention"
        body="Please check @lin."
        mentionedMembers={new Map()} taskRefs={new Map()} spaceSlug="sumi-lab"
      />,
    );

    expect(container.querySelector("mark.message-mention")).not.toBeInTheDocument();
    expect(container).toHaveTextContent("Please check @lin.");
  });

  it("links a structured Agent mention to Agent management", () => {
    const { container } = render(
      <ExpandableMessageText
        messageId="message-agent-mention"
        body="Please check @Lin."
        mentionedMembers={new Map([["lin", { id: "agent-lin", kind: "agent", display_name: "Lin" }]])}
        taskRefs={new Map()}
        spaceSlug="sumi-lab"
      />,
    );

    const link = container.querySelector("a.message-mention--agent");
    expect(link).toHaveAttribute("href", "/s/sumi-lab/agents/agent-lin");
    expect(link).toHaveAccessibleName("Open Lin Agent management");
  });
});

describe("Agent mention style", () => {
  it("keeps Agent links at the compact message size and regular weight", () => {
    const css = readFileSync(join(process.cwd(), "src/styles.css"), "utf8");
    const rule = css.match(/\.message-mention--agent\s*\{([^}]*)\}/)?.[1];
    expect(rule).toBeDefined();
    expect(rule).toMatch(/font-size:\s*13px/);
    expect(rule).toMatch(/font-weight:\s*400/);
  });
});

describe("message body typography", () => {
  it("uses the same 14px base size in Channel and Thread messages", () => {
    const tokens = readFileSync(join(process.cwd(), "src/styles/tokens.css"), "utf8");
    const styles = readFileSync(join(process.cwd(), "src/styles.css"), "utf8");
    const channelStyles = readFileSync(join(process.cwd(), "src/components/channel/channel.css"), "utf8");
    expect(tokens).toMatch(/--text-message:\s*14px/);
    expect(styles).toMatch(/\.message-body\s*\{[^}]*font-size:\s*var\(--text-message\)/s);
    expect(styles).toMatch(/\.message-content p,\s*\.thread-message p \{\s*font-size:\s*var\(--text-message\);\s*\}/);
    expect(channelStyles).toMatch(/\.thread-message p\s*\{[^}]*font-size:\s*var\(--text-message\)/s);
  });
});

describe("inline code", () => {
  it("renders backtick spans as code and keeps mentions inside them unhighlighted", () => {
    const { container } = render(
      <ExpandableMessageText
        messageId="message-inline-code"
        body="Run `sumi server --help`, then check @lin and `@lin` inside code."
        mentionedMembers={new Map([["lin", { id: "human-lin", kind: "human", display_name: "lin" }]])} taskRefs={new Map()} spaceSlug="sumi-lab"
      />,
    );

    const codeNodes = container.querySelectorAll("code.message-inline-code");
    expect(codeNodes).toHaveLength(2);
    expect(codeNodes[0]).toHaveTextContent("sumi server --help");
    expect(codeNodes[1]).toHaveTextContent("@lin");
    expect(container.querySelectorAll("mark.message-mention")).toHaveLength(1);
    expect(container.querySelector("mark.message-mention")).toHaveTextContent("@lin");
    expect(container).toHaveTextContent("Run sumi server --help, then check @lin and @lin inside code.");
  });

  it("keeps unmatched backticks as plain text", () => {
    const { container } = render(
      <ExpandableMessageText
        messageId="message-unclosed-code"
        body="A lone `backtick stays plain"
        mentionedMembers={new Map()} taskRefs={new Map()} spaceSlug="sumi-lab"
      />,
    );

    expect(container.querySelector("code.message-inline-code")).not.toBeInTheDocument();
    expect(container).toHaveTextContent("A lone `backtick stays plain");
  });
});

describe("markdown rendering", () => {
  it("renders bold and italic", () => {
    const { container } = render(
      <ExpandableMessageText
        messageId="message-bold"
        body="Use **strong** and *emphasis* together."
        mentionedMembers={new Map()} taskRefs={new Map()} spaceSlug="sumi-lab"
      />,
    );

    expect(container.querySelector("strong")).toHaveTextContent("strong");
    expect(container.querySelector("em")).toHaveTextContent("emphasis");
    expect(container).toHaveTextContent("Use strong and emphasis together.");
  });

  it("renders h1 to h3 headings", () => {
    const { container } = render(
      <ExpandableMessageText
        messageId="message-headings"
        body={"# One\n## Two\n### Three\nbody text"}
        mentionedMembers={new Map()} taskRefs={new Map()} spaceSlug="sumi-lab"
      />,
    );

    expect(container.querySelector("h1.message-heading")).toHaveTextContent("One");
    expect(container.querySelector("h2.message-heading")).toHaveTextContent("Two");
    expect(container.querySelector("h3.message-heading")).toHaveTextContent("Three");
  });

  it("treats literal backslash-n sequences as Markdown line breaks", () => {
    const { container } = render(
      <ExpandableMessageText
        messageId="message-escaped-newlines"
        body={String.raw`> Received:\n\n1. First item\n2. Second item`}
        mentionedMembers={new Map()}
        taskRefs={new Map()}
        spaceSlug="sumi-lab"
      />,
    );

    expect(container.querySelector("blockquote p")).toHaveTextContent("Received:");
    expect(container.querySelectorAll("ol > li")).toHaveLength(2);
    expect(container.querySelector(".message-body")).not.toHaveTextContent("\\n");
  });

  it("renders unordered and ordered lists", () => {
    const { container } = render(
      <ExpandableMessageText
        messageId="message-lists"
        body={"- first\n- second\n\n1. one\n2. two"}
        mentionedMembers={new Map()} taskRefs={new Map()} spaceSlug="sumi-lab"
      />,
    );

    expect(container.querySelectorAll("ul li")).toHaveLength(2);
    expect(container.querySelectorAll("ol li")).toHaveLength(2);
  });

  it("renders links and rejects unsafe protocols", () => {
    const { container } = render(
      <ExpandableMessageText
        messageId="message-links"
        body="See [docs](https://example.test/docs) and [bad](javascript:alert(1))."
        mentionedMembers={new Map()} taskRefs={new Map()} spaceSlug="sumi-lab"
      />,
    );

    const link = container.querySelector("a");
    expect(link).toHaveAttribute("href", "https://example.test/docs");
    expect(container.querySelectorAll("a")).toHaveLength(1);
    expect(container).toHaveTextContent("See docs and [bad](javascript:alert(1)).");
  });

  it("renders fenced code blocks as pre with mono code", () => {
    const { container } = render(
      <ExpandableMessageText
        messageId="message-code-block"
        body={"```\nsumi server --help\n```"}
        mentionedMembers={new Map()} taskRefs={new Map()} spaceSlug="sumi-lab"
      />,
    );

    expect(container.querySelector("pre code")).toHaveTextContent("sumi server --help");
  });

  it("supports nested inline markup inside bold", () => {
    const { container } = render(
      <ExpandableMessageText
        messageId="message-nested"
        body="Run **`sumi server`** now."
        mentionedMembers={new Map()} taskRefs={new Map()} spaceSlug="sumi-lab"
      />,
    );

    const strong = container.querySelector("strong");
    expect(strong).not.toBeNull();
    expect(strong?.querySelector("code.message-inline-code")).toHaveTextContent("sumi server");
  });

  it("renders recognized task refs as links and leaves unknown refs plain", () => {
    const { container } = render(
      <ExpandableMessageText
        messageId="message-taskrefs"
        body="Work on !3 and mention !99 later."
        mentionedMembers={new Map()}
        taskRefs={new Map([[3, { seq: 3, task_id: "task-3", title: "Rebuild WebUI", status: "in_progress" }]])}
        spaceSlug="sumi-lab"
      />,
    );

    const link = container.querySelector("a.message-task-ref");
    expect(link).toHaveAttribute("href", "/s/sumi-lab/tasks/task-3");
    expect(link).toHaveTextContent("!3");
    expect(container).toHaveTextContent("!99");
    expect(container.querySelectorAll("a.message-task-ref")).toHaveLength(1);
  });
});
