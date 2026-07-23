import { render, screen } from "@testing-library/react";
import { describe, expect, it } from "vitest";
import { IconButton } from "./IconButton";

describe("IconButton", () => {
  it("keeps the icon action named and exposes a visible-tooltip contract", () => {
    render(
      <IconButton
        label="Refresh conversations"
        tooltip="Refresh the conversation list"
        tooltipPlacement="left"
      >
        <span aria-hidden="true">↻</span>
      </IconButton>,
    );

    const button = screen.getByRole("button", {
      name: "Refresh conversations",
    });
    expect(button).toHaveAttribute(
      "data-tooltip",
      "Refresh the conversation list",
    );
    expect(button).toHaveAttribute("data-tooltip-placement", "left");
    expect(button).not.toHaveAttribute("title");
  });
});
