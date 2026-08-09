import { beforeEach, describe, expect, it } from "vitest";

import {
  AGENT_INSIGHTS_FEATURE_ID,
  COMPANY_OFFICE_FEATURE_ID,
  REGISTERED_FEATURES,
  featureEnabled,
  registeredFeature,
  setFeatureEnabled,
} from "./featureRegistry";

beforeEach(() => {
  window.localStorage.clear();
});

describe("feature registry", () => {
  it("registers the Agent graph as an experimental feature", () => {
    expect(REGISTERED_FEATURES.map((feature) => feature.id)).toContain(AGENT_INSIGHTS_FEATURE_ID);
    expect(registeredFeature(AGENT_INSIGHTS_FEATURE_ID)?.kind).toBe("experimental");
  });

  it("registers Company office as an experimental feature", () => {
    expect(REGISTERED_FEATURES.map((feature) => feature.id)).toContain(COMPANY_OFFICE_FEATURE_ID);
    expect(registeredFeature(COMPANY_OFFICE_FEATURE_ID)?.kind).toBe("experimental");
    expect(featureEnabled(COMPANY_OFFICE_FEATURE_ID)).toBe(false);
  });

  it("is off by default", () => {
    expect(featureEnabled(AGENT_INSIGHTS_FEATURE_ID)).toBe(false);
  });

  it("persists per-feature state to localStorage", () => {
    setFeatureEnabled(AGENT_INSIGHTS_FEATURE_ID, true);
    expect(featureEnabled(AGENT_INSIGHTS_FEATURE_ID)).toBe(true);
    expect(window.localStorage.getItem("sumi.feature.agent-insights")).toBe("1");

    setFeatureEnabled(AGENT_INSIGHTS_FEATURE_ID, false);
    expect(featureEnabled(AGENT_INSIGHTS_FEATURE_ID)).toBe(false);
  });

  it("ignores unknown feature ids", () => {
    expect(featureEnabled("missing")).toBe(false);
    setFeatureEnabled("missing", true);
    expect(window.localStorage.getItem("sumi.feature.missing")).toBeNull();
  });
});
