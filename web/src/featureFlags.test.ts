import { describe, expect, it } from "vitest";

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
