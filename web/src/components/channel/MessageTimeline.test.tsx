import { fireEvent, render, screen } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";

import { ExpandableMessageText } from "./MessageTimeline";

describe("ExpandableMessageText", () => {
  afterEach(() => vi.restoreAllMocks());

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
