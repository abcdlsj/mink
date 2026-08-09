import { beforeEach, describe, expect, it } from "vitest";

import {
  AGENT_GRAPH_FEATURE_ID,
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
    expect(REGISTERED_FEATURES.map((feature) => feature.id)).toContain(AGENT_GRAPH_FEATURE_ID);
    expect(registeredFeature(AGENT_GRAPH_FEATURE_ID)?.kind).toBe("experimental");
  });

  it("is off by default", () => {
    expect(featureEnabled(AGENT_GRAPH_FEATURE_ID)).toBe(false);
  });

  it("persists per-feature state to localStorage", () => {
    setFeatureEnabled(AGENT_GRAPH_FEATURE_ID, true);
    expect(featureEnabled(AGENT_GRAPH_FEATURE_ID)).toBe(true);
    expect(window.localStorage.getItem("sumi.feature.agent-graph")).toBe("1");

    setFeatureEnabled(AGENT_GRAPH_FEATURE_ID, false);
    expect(featureEnabled(AGENT_GRAPH_FEATURE_ID)).toBe(false);
  });

  it("ignores unknown feature ids", () => {
    expect(featureEnabled("missing")).toBe(false);
    setFeatureEnabled("missing", true);
    expect(window.localStorage.getItem("sumi.feature.missing")).toBeNull();
  });
});
