/// <reference types="node" />

import { cleanup, fireEvent, render, screen } from "@testing-library/react";
import { readFileSync } from "node:fs";
import { join } from "node:path";
import { afterEach, describe, expect, it, vi } from "vitest";

import { ExpandableMessageText } from "./MessageTimeline";

afterEach(() => { cleanup(); vi.restoreAllMocks(); });

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

    expect(container.querySelectorAll("mark.message-mention")).toHaveLength(1);
    expect(container.querySelector("mark.message-mention")).toHaveTextContent("@Lin");
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
