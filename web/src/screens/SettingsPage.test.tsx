import { cleanup, fireEvent, render, screen } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it } from "vitest";

import { setExperimentalFeaturesEnabled } from "../featureFlags";
import { SettingsWorkspace } from "./SettingsPage";

const toggleName = "Enable experimental features";

beforeEach(() => {
  window.localStorage.clear();
  setExperimentalFeaturesEnabled(false);
});

afterEach(() => {
  cleanup();
});

describe("SettingsWorkspace", () => {
  it("shows experimental features disabled by default", () => {
    render(<SettingsWorkspace />);

    expect(screen.getByRole("checkbox", { name: toggleName })).not.toBeChecked();
  });

  it("persists the toggle when enabled", () => {
    render(<SettingsWorkspace />);

    fireEvent.click(screen.getByRole("checkbox", { name: toggleName }));

    expect(screen.getByRole("checkbox", { name: toggleName })).toBeChecked();
    expect(window.localStorage.getItem("sumi.experimental_features")).toBe("1");
  });
});
