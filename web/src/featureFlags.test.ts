import { beforeEach, describe, expect, it, vi } from "vitest";

import { designLabEnabled } from "./featureFlags";

describe("designLabEnabled", () => {
  it("is off unless explicitly set to true or 1", () => {
    expect(designLabEnabled(undefined)).toBe(false);
    expect(designLabEnabled("false")).toBe(false);
    expect(designLabEnabled("yes")).toBe(false);
    expect(designLabEnabled("true")).toBe(true);
    expect(designLabEnabled("1")).toBe(true);
  });
});

describe("experimental features", () => {
  beforeEach(() => {
    window.localStorage.clear();
    vi.resetModules();
  });

  it("is off by default", async () => {
    const mod = await import("./featureFlags");
    expect(mod.experimentalFeaturesEnabled()).toBe(false);
  });

  it("persists the toggle across module reloads", async () => {
    const mod = await import("./featureFlags");
    mod.setExperimentalFeaturesEnabled(true);
    expect(mod.experimentalFeaturesEnabled()).toBe(true);
    expect(window.localStorage.getItem("sumi.experimental_features")).toBe("1");

    vi.resetModules();
    const reloaded = await import("./featureFlags");
    expect(reloaded.experimentalFeaturesEnabled()).toBe(true);
  });

  it("reads a previously stored enabled value", async () => {
    window.localStorage.setItem("sumi.experimental_features", "1");
    const mod = await import("./featureFlags");
    expect(mod.experimentalFeaturesEnabled()).toBe(true);
  });
});
