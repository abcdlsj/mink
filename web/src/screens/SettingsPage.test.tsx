import { cleanup, fireEvent, render, screen } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it } from "vitest";

import { setFeatureEnabled } from "../featureRegistry";
import { SettingsWorkspace } from "./SettingsPage";

beforeEach(() => {
  window.localStorage.clear();
  setFeatureEnabled("agent-insights", false);
});

afterEach(() => {
  cleanup();
});

describe("SettingsWorkspace", () => {
  it("shows a placeholder when no feature is selected", () => {
    render(<SettingsWorkspace />);

    expect(screen.getByRole("heading", { name: "Settings" })).toBeVisible();
    expect(screen.getByText(/Choose a feature from the list/)).toBeVisible();
  });

  it("shows the experimental enable toggle and persists it per feature", () => {
    render(<SettingsWorkspace selectedFeatureId="agent-insights" />);

    const toggle = screen.getByRole("checkbox", { name: "Enabled" });
    expect(toggle).not.toBeChecked();
    fireEvent.click(toggle);

    expect(screen.getByRole("checkbox", { name: "Enabled" })).toBeChecked();
    expect(window.localStorage.getItem("sumi.feature.agent-insights")).toBe("1");
  });

  it("labels the feature kind", () => {
    render(<SettingsWorkspace selectedFeatureId="agent-insights" />);

    expect(screen.getByText("experimental")).toBeVisible();
    expect(screen.getByRole("heading", { name: "Agent insights" })).toBeVisible();
  });
});
