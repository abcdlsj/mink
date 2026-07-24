import { render } from "@testing-library/react";
import { describe, expect, it } from "vitest";
import { PixelAvatar } from "./PixelAvatar";

describe("PixelAvatar", () => {
  it("keeps the same visual identity for the same stable seed", () => {
    const { container } = render(
      <>
        <PixelAvatar seed="agent-release-coordinator" kind="agent" />
        <PixelAvatar seed="agent-release-coordinator" kind="agent" />
      </>,
    );

    const avatars = container.querySelectorAll(".pixel-avatar");
    expect(avatars).toHaveLength(2);
    expect(avatars[0]).toHaveAttribute(
      "data-palette",
      avatars[1].getAttribute("data-palette"),
    );
    expect(avatars[0]).toHaveAttribute(
      "data-variant",
      avatars[1].getAttribute("data-variant"),
    );
  });

  it("renders distinct typed sprites without duplicating nearby labels", () => {
    const { container } = render(
      <>
        <PixelAvatar seed="human-owner" kind="human" size="xs" />
        <PixelAvatar seed="agent-ops" kind="agent" size="lg" />
      </>,
    );

    expect(container.querySelector('[data-kind="human"]')).toHaveClass(
      "pixel-avatar-xs",
    );
    expect(container.querySelector('[data-kind="agent"]')).toHaveClass(
      "pixel-avatar-lg",
    );
    expect(container.querySelectorAll('[aria-hidden="true"]')).toHaveLength(2);
  });
});
