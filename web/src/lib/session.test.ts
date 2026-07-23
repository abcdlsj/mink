import { afterEach, describe, expect, it, vi } from "vitest";
import {
  getLocalSetupRequired,
  getSession,
  loginLocalAccount,
  logoutSession,
  setupLocalAccount,
} from "./session";

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

  it("reads one-time setup state without inventing a default", async () => {
    const fetch = vi
      .fn()
      .mockResolvedValueOnce(
        new Response(JSON.stringify({ setup_required: true }), {
          status: 200,
        }),
      )
      .mockResolvedValueOnce(
        new Response(JSON.stringify({ setup_required: "yes" }), {
          status: 200,
        }),
      );
    vi.stubGlobal("fetch", fetch);

    await expect(getLocalSetupRequired()).resolves.toBe(true);
    expect(fetch).toHaveBeenCalledWith("/auth/local", {
      credentials: "same-origin",
      cache: "no-store",
      headers: { Accept: "application/json" },
    });
    await expect(getLocalSetupRequired()).rejects.toThrow(
      "Local authentication status invalid",
    );
  });

  it("submits local setup and login only to same-origin JSON endpoints", async () => {
    const payload = JSON.stringify({
      human: {
        id: "33333333-3333-4333-8333-333333333333",
        name: "Owner",
      },
    });
    const fetch = vi
      .fn()
      .mockResolvedValueOnce(new Response(payload, { status: 201 }))
      .mockResolvedValueOnce(new Response(payload, { status: 200 }));
    vi.stubGlobal("fetch", fetch);

    await expect(
      setupLocalAccount({
        username: "owner",
        password: "correct horse battery staple",
        bootstrapCredential: "A".repeat(43),
      }),
    ).resolves.toEqual({
      id: "33333333-3333-4333-8333-333333333333",
      name: "Owner",
    });
    expect(fetch).toHaveBeenNthCalledWith(1, "/auth/local/setup", {
      method: "POST",
      credentials: "same-origin",
      cache: "no-store",
      headers: {
        Accept: "application/json",
        "Content-Type": "application/json",
      },
      body: JSON.stringify({
        username: "owner",
        password: "correct horse battery staple",
        bootstrap_credential: "A".repeat(43),
      }),
    });

    await expect(
      loginLocalAccount({
        username: "owner",
        password: "correct horse battery staple",
      }),
    ).resolves.toEqual({
      id: "33333333-3333-4333-8333-333333333333",
      name: "Owner",
    });
  });

  it("maps local auth failures without exposing response bodies", async () => {
    const fetch = vi
      .fn()
      .mockResolvedValueOnce(
        new Response("sensitive server detail", { status: 401 }),
      )
      .mockResolvedValueOnce(new Response(null, { status: 429 }))
      .mockResolvedValueOnce(new Response(null, { status: 409 }));
    vi.stubGlobal("fetch", fetch);

    await expect(
      loginLocalAccount({ username: "owner", password: "wrong password" }),
    ).rejects.toThrow("Username or password is incorrect.");
    await expect(
      loginLocalAccount({ username: "owner", password: "wrong password" }),
    ).rejects.toThrow("Too many attempts");
    await expect(
      setupLocalAccount({
        username: "owner",
        password: "correct horse battery staple",
        bootstrapCredential: "A".repeat(43),
      }),
    ).rejects.toThrow("Local setup is already complete");
  });
});
