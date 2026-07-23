import { cleanup, fireEvent, render, screen } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { PrimaryRail } from "../components/PrimaryRail";
import { initializeTheme } from "./theme";

afterEach(() => {
  cleanup();
  window.localStorage.clear();
  document.documentElement.removeAttribute("data-theme");
  document.documentElement.style.removeProperty("color-scheme");
  vi.unstubAllGlobals();
});

describe("theme", () => {
  it("follows the system preference before a Human chooses a theme", () => {
    vi.stubGlobal("matchMedia", vi.fn().mockReturnValue({ matches: true }));

    initializeTheme();
    render(
      <PrimaryRail
        active="conversation"
        factsAvailable={false}
        onSelect={() => undefined}
      />,
    );

    expect(document.documentElement).toHaveAttribute("data-theme", "dark");
    expect(window.localStorage.getItem("sumi.theme")).toBeNull();
    expect(
      screen.getByRole("button", { name: "Use light theme" }),
    ).toBeVisible();
  });

  it("persists an explicit theme toggle", () => {
    window.localStorage.setItem("sumi.theme", "light");
    render(
      <PrimaryRail
        active="conversation"
        factsAvailable={false}
        onSelect={() => undefined}
      />,
    );

    fireEvent.click(screen.getByRole("button", { name: "Use dark theme" }));

    expect(document.documentElement).toHaveAttribute("data-theme", "dark");
    expect(window.localStorage.getItem("sumi.theme")).toBe("dark");
    expect(
      screen.getByRole("button", { name: "Use light theme" }),
    ).toBeVisible();
  });
});
