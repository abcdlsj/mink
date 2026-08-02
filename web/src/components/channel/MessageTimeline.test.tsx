import { cleanup, fireEvent, render, screen } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";

import { ExpandableMessageText } from "./MessageTimeline";

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
