import { afterEach, describe, expect, it, vi } from "vitest";
import { getSession, logoutSession } from "./session";

afterEach(() => {
  vi.unstubAllGlobals();
});

describe("browser session", () => {
  it("returns the authenticated Human", async () => {
    const fetch = vi.fn().mockResolvedValue(
      new Response(
        JSON.stringify({
          human: {
            id: "33333333-3333-4333-8333-333333333333",
            name: "Owner",
          },
        }),
        { status: 200, headers: { "Content-Type": "application/json" } },
      ),
    );
    vi.stubGlobal("fetch", fetch);

    await expect(getSession()).resolves.toEqual({
      id: "33333333-3333-4333-8333-333333333333",
      name: "Owner",
    });
    expect(fetch).toHaveBeenCalledWith("/auth/session", {
      credentials: "same-origin",
      cache: "no-store",
      headers: { Accept: "application/json" },
    });
  });

  it("treats 401 and disabled browser auth as signed out", async () => {
    const fetch = vi
      .fn()
      .mockResolvedValueOnce(new Response(null, { status: 401 }))
      .mockResolvedValueOnce(new Response(null, { status: 404 }));
    vi.stubGlobal("fetch", fetch);

    await expect(getSession()).resolves.toBeUndefined();
    await expect(getSession()).resolves.toBeUndefined();
  });

  it("rejects malformed or unavailable session responses", async () => {
    const fetch = vi
      .fn()
      .mockResolvedValueOnce(
        new Response(JSON.stringify({ human: { id: "", name: "" } }), {
          status: 200,
        }),
      )
      .mockResolvedValueOnce(new Response(null, { status: 500 }));
    vi.stubGlobal("fetch", fetch);

    await expect(getSession()).rejects.toThrow("Session response invalid");
    await expect(getSession()).rejects.toThrow("Session status unavailable");
  });

  it("logs out only through the same-origin cookie endpoint", async () => {
    const fetch = vi
      .fn()
      .mockResolvedValueOnce(new Response(null, { status: 204 }))
      .mockResolvedValueOnce(new Response(null, { status: 403 }));
    vi.stubGlobal("fetch", fetch);

    await expect(logoutSession()).resolves.toBeUndefined();
    expect(fetch).toHaveBeenCalledWith("/auth/logout", {
      method: "POST",
      credentials: "same-origin",
      cache: "no-store",
    });
    await expect(logoutSession()).rejects.toThrow("Logout unavailable");
  });
});
