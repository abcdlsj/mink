import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { RouterProvider, createMemoryHistory } from "@tanstack/react-router";
import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";

import { createAppRouter } from "../router";

const space = {
  id: "019c0000-0000-7000-8000-000000000010",
  name: "Sumi Lab",
  slug: "sumi-lab",
  accent: "#5065D8",
  owner_member_id: "019c0000-0000-7000-8000-000000000011",
  current_member_id: "019c0000-0000-7000-8000-000000000011",
  general_channel_id: "019c0000-0000-7000-8000-000000000012",
};

describe("RegisterPage", () => {
  afterEach(() => {
    cleanup();
    vi.unstubAllGlobals();
  });

  it("renders the Human registration fields", async () => {
    const router = createAppRouter(createMemoryHistory({ initialEntries: ["/"] }));
    await router.load();
    render(
      <QueryClientProvider client={new QueryClient()}>
        <RouterProvider router={router} />
      </QueryClientProvider>,
    );

    expect(await screen.findByRole("heading", { name: "Join the room." })).toBeVisible();
    expect(screen.getByLabelText("Display name")).toBeRequired();
    expect(screen.getByLabelText("Email")).toHaveAttribute("type", "email");
    expect(screen.getByLabelText("Password")).toHaveAttribute("minLength", "10");
    expect(screen.getByRole("button", { name: /continue/i })).toBeVisible();
  });

  it("preserves a safe Space deep link through authentication", async () => {
    const deepLink = "/s/sumi-lab/members?focus=latest";
    let authenticated = false;
    const fetchMock = vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      const path = String(input);
      if (path === "/api/v1/auth/login" && init?.method === "POST") {
        authenticated = true;
        return new Response(JSON.stringify({ user: {
          id: "019c0000-0000-7000-8000-000000000001",
          display_name: "Ada Lovelace",
          email: "ada@example.test",
        } }), { status: 200, headers: { "Content-Type": "application/json" } });
      }
      if (!authenticated) {
        return new Response(
          JSON.stringify({ error: { code: "unauthorized", message: "Authentication is required" } }),
          { status: 401, headers: { "Content-Type": "application/json" } },
        );
      }
      if (path === "/api/v1/auth/me") {
        return json({
          id: "019c0000-0000-7000-8000-000000000001",
          display_name: "Ada Lovelace",
          email: "ada@example.test",
        });
      }
      if (path.includes("/spaces/by-slug/")) return json(space);
      if (path.endsWith("/channels")) return json({ can_create: true, channels: [] });
      if (path.endsWith("/dms")) return json([]);
      if (path.endsWith("/members")) return json([{
        id: space.owner_member_id,
        kind: "human",
        display_name: "Ada Lovelace",
        handle: "ada-lovelace",
        access_level: "owner",
        permissions: [],
      }]);
      throw new Error(`Unexpected request: ${path}`);
    });
    vi.stubGlobal("fetch", fetchMock);
    const router = createAppRouter(createMemoryHistory({ initialEntries: [deepLink] }));
    await router.load();
    render(
      <QueryClientProvider client={new QueryClient({ defaultOptions: { queries: { retry: false } } })}>
        <RouterProvider router={router} />
      </QueryClientProvider>,
    );

    await waitFor(() => {
      expect(router.state.location.pathname).toBe("/login");
    });
    expect(router.state.location.search.redirect).toBe(deepLink);
    expect(screen.getByRole("link", { name: "Register" })).toHaveAttribute(
      "href",
      `/?redirect=${encodeURIComponent(deepLink)}`,
    );

    fireEvent.change(screen.getByLabelText("Email"), { target: { value: "ada@example.test" } });
    fireEvent.change(screen.getByLabelText("Password"), { target: { value: "correct-horse-battery" } });
    fireEvent.click(screen.getByRole("button", { name: "Sign in" }));

    await waitFor(() => {
      expect(router.state.location.href).toBe(deepLink);
    });
  });
});

function json(body: unknown, status = 200): Response {
  return new Response(JSON.stringify(body), {
    status,
    headers: { "Content-Type": "application/json" },
  });
}
