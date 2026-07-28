import { render, screen } from "@testing-library/react";
import { describe, expect, it } from "vitest";

import { buildAgentIdenticon } from "./agentIdenticon";
import { PixelIdentity } from "./PixelIdentity";

const linId = "019c0000-0000-7000-8000-000000000020";
const reviewerId = "019c0000-0000-7000-8000-000000000021";

describe("PixelIdentity", () => {
  it("derives a stable, distinguishable Agent seal from Member ID only", () => {
    const first = buildAgentIdenticon(linId);
    const renamed = buildAgentIdenticon(linId);
    const reviewer = buildAgentIdenticon(reviewerId);

    expect(renamed).toEqual(first);
    expect(reviewer.signature).not.toBe(first.signature);
  });

  it("keeps every Agent pixel inside the inset 8x8 canvas and mirrors it horizontally", () => {
    const identicon = buildAgentIdenticon(linId);
    const cells = new Set(identicon.cells.map(([x, y]) => `${x},${y}`));

    expect(identicon.cells.length).toBeGreaterThanOrEqual(12);
    for (const [x, y] of identicon.cells) {
      expect(x).toBeGreaterThanOrEqual(1);
      expect(x).toBeLessThanOrEqual(6);
      expect(y).toBeGreaterThanOrEqual(1);
      expect(y).toBeLessThanOrEqual(6);
      expect(cells).toContain(`${7 - x},${y}`);
    }
  });

  it("keeps an Agent seal across renames while Humans retain seeded initials", () => {
    const { rerender } = render(<PixelIdentity name="Lin" kind="agent" seed={linId} />);
    const original = screen.getByRole("img", { name: "Lin avatar" }).getAttribute("data-agent-identicon");
    expect(original).toBeTruthy();

    rerender(<PixelIdentity name="Architect" kind="agent" seed={linId} />);
    expect(screen.getByRole("img", { name: "Architect avatar" })).toHaveAttribute("data-agent-identicon", original);

    rerender(<PixelIdentity name="Ada" kind="human" seed={linId} />);
    const human = screen.getByRole("img", { name: "Ada avatar" });
    expect(human).not.toHaveAttribute("data-agent-identicon");
    expect(human).toHaveTextContent("A");
  });
});
