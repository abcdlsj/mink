import { render } from "@testing-library/react";
import { describe, expect, it } from "vitest";

import { PixelWord } from "./PixelWord";

describe("PixelWord", () => {
  it("stays on one line for short names", () => {
    const { container } = render(<PixelWord text="Sumi Dev" variant="bold" />);
    expect(container.querySelectorAll("svg")).toHaveLength(1);
  });

  it("wraps long names into readable pixel lines", () => {
    const { container } = render(<PixelWord text="A Very Long Space Name Indeed" variant="bold" />);
    const svgs = container.querySelectorAll("svg");
    expect(svgs.length).toBeGreaterThan(1);
    expect(svgs.length).toBeLessThanOrEqual(2);
    const height = Number(svgs[0].getAttribute("height"));
    expect(height).toBeGreaterThanOrEqual(14);
  });

  it("clips names that cannot fit two lines", () => {
    const { container } = render(<PixelWord text="First Second Third Fourth Fifth Sixth Seventh" variant="bold" />);
    expect(container.querySelectorAll("svg").length).toBeLessThanOrEqual(2);
  });
});
