import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { RouterProvider, createMemoryHistory } from "@tanstack/react-router";
import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";

import { createAppRouter } from "../router";

const space = {
  id: "019c0000-0000-7000-8000-000000000001",
  name: "Sumi Lab",
  slug: "sumi-lab",
  accent: "#FFD447",
  owner_member_id: "019c0000-0000-7000-8000-000000000002",
  current_member_id: "019c0000-0000-7000-8000-000000000002",
  general_channel_id: "019c0000-0000-7000-8000-000000000003",
};

afterEach(() => {
  vi.unstubAllGlobals();
});

describe("Phase one Human flows", () => {
  it("renders the unified Member list and Owner controls", async () => {
    const fetchMock = vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      const path = String(input);
      if (path.includes("/spaces/by-slug/")) return json(space);
      if (path === "/api/v1/auth/me") {
        return json({
          id: "019c0000-0000-7000-8000-000000000010",
          display_name: "Ada Lovelace",
          email: "ada@example.test",
        });
      }
      if (path.endsWith("/channels") && !init?.method) {
        return json({
          can_create: true,
          channels: [
            {
              id: space.general_channel_id,
              space_id: space.id,
              kind: "public",
              name: "general",
              slug: "general",
              topic: null,
              created_by_member_id: space.owner_member_id,
              joined: true,
            },
          ],
        });
      }
      if (path.endsWith("/dms") && !init?.method) return json([]);
      if (path.endsWith("/members") && !init?.method) {
        return json([
          {
            id: space.owner_member_id,
            kind: "human",
            display_name: "Ada Lovelace",
            handle: "ada-lovelace",
            access_level: "owner",
            permissions: [],
          },
          {
            id: "019c0000-0000-7000-8000-000000000020",
            kind: "human",
            display_name: "Grace Hopper",
            handle: "grace-hopper",
            access_level: "member",
            permissions: ["channel:create"],
          },
        ]);
      }
      if (init?.method === "PATCH") {
        return json({
          id: "019c0000-0000-7000-8000-000000000020",
          kind: "human",
          display_name: "Grace Hopper",
          handle: "grace-hopper",
          access_level: "admin",
          permissions: [],
        });
      }
      if (path.endsWith("/dms") && init?.method === "POST") {
        return json(
          {
            channel_id: "019c0000-0000-7000-8000-000000000099",
            space_id: space.id,
            other_member: {
              id: "019c0000-0000-7000-8000-000000000020",
              kind: "human",
              display_name: "Grace Hopper",
              handle: "grace-hopper",
              access_level: "member",
              permissions: [],
            },
            created_at: "2026-07-25T00:00:00Z",
          },
          201,
        );
      }
      throw new Error(`Unexpected request: ${path}`);
    });
    vi.stubGlobal("fetch", fetchMock);
    renderRoute("/s/sumi-lab/members");

    expect(await screen.findByRole("heading", { name: "Members" })).toBeVisible();
    expect(await screen.findByText("Grace Hopper")).toBeVisible();
    const access = screen.getByRole("combobox", { name: "Access level for Grace Hopper" });
    expect(access).toHaveValue("member");

    fireEvent.change(access, { target: { value: "admin" } });

    await waitFor(() => {
      expect(fetchMock).toHaveBeenCalledWith(
        expect.stringContaining("/members/019c0000-0000-7000-8000-000000000020"),
        expect.objectContaining({
          method: "PATCH",
          body: JSON.stringify({ access_level: "admin" }),
        }),
      );
    });

    fireEvent.click(screen.getByRole("button", { name: /message/i }));
    await waitFor(() => {
      expect(fetchMock).toHaveBeenCalledWith(
        expect.stringContaining(`/spaces/${space.id}/dms`),
        expect.objectContaining({ method: "POST" }),
      );
    });
  });

  it("keeps the invitation redirect on registration and login links", async () => {
    const token = "BwcHBwcHBwcHBwcHBwcHBwcHBwcHBwcHBwcHBwcHBwc";
    vi.stubGlobal(
      "fetch",
      vi.fn(async (input: RequestInfo | URL) => {
        const path = String(input);
        if (path.includes("/api/v1/invites/")) {
          return json({
            id: "019c0000-0000-7000-8000-000000000030",
            space_id: space.id,
            space_name: space.name,
            space_slug: space.slug,
            email: "grace@example.test",
            expires_at: "2026-08-01T00:00:00Z",
          });
        }
        if (path === "/api/v1/auth/me") {
          return json(
            { error: { code: "unauthorized", message: "Authentication is required" } },
            401,
          );
        }
        throw new Error(`Unexpected request: ${path}`);
      }),
    );
    renderRoute(`/invite/${token}`);

    expect(await screen.findByRole("heading", { name: space.name })).toBeVisible();
    expect(screen.getByText("grace@example.test")).toBeVisible();
    expect(screen.getByRole("link", { name: /register/i })).toHaveAttribute(
      "href",
      `/?redirect=${encodeURIComponent(`/invite/${token}`)}`,
    );
    expect(screen.getByRole("link", { name: /sign in/i })).toHaveAttribute(
      "href",
      `/login?redirect=${encodeURIComponent(`/invite/${token}`)}`,
    );
  });

  it("shows Human Inbox attention and completes an item", async () => {
    const itemId = "019c0000-0000-7000-8000-000000000050";
    const fetchMock = vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      const path = String(input);
      if (path.includes("/spaces/by-slug/")) return json(space);
      if (path === "/api/v1/auth/me") {
        return json({ id: "user", display_name: "Ada Lovelace", email: "ada@example.test" });
      }
      if (path.endsWith("/channels") && !init?.method) {
        return json({ can_create: true, channels: [{ id: space.general_channel_id, space_id: space.id, kind: "public", name: "general", slug: "general", created_by_member_id: space.owner_member_id, joined: true }] });
      }
      if (path.endsWith("/dms") && !init?.method) return json([]);
      if (path.endsWith("/members") && !init?.method) {
        return json([{ id: space.owner_member_id, kind: "human", display_name: "Ada Lovelace", handle: "ada", access_level: "owner", permissions: [] }]);
      }
      if (path.endsWith(`/members/${space.owner_member_id}/inbox`) && !init?.method) {
        return json([{ id: itemId, member_id: space.owner_member_id, kind: "mention", priority: "hard", channel_id: space.general_channel_id, channel_slug: "general", message_id: "message", sender_member_id: "grace", sender_display_name: "Grace Hopper", summary: "Please review this boundary.", status: "pending", available_at: "2026-07-25T00:00:00Z", created_at: "2026-07-25T00:00:00Z" }]);
      }
      if (path.endsWith(`/inbox/${itemId}/ack`) && init?.method === "POST") {
        return json({ id: itemId, status: "handled" });
      }
      throw new Error(`Unexpected request: ${path}`);
    });
    vi.stubGlobal("fetch", fetchMock);
    renderRoute("/s/sumi-lab/inbox");

    expect(await screen.findByText("Please review this boundary.")).toBeVisible();
    fireEvent.click(screen.getByRole("button", { name: "Complete Inbox Item" }));
    await waitFor(() => {
      expect(fetchMock).toHaveBeenCalledWith(
        expect.stringContaining(`/inbox/${itemId}/ack`),
        expect.objectContaining({ method: "POST" }),
      );
    });
  });
});

function renderRoute(path: string) {
  const router = createAppRouter(createMemoryHistory({ initialEntries: [path] }));
  const queryClient = new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  });
  render(
    <QueryClientProvider client={queryClient}>
      <RouterProvider router={router} />
    </QueryClientProvider>,
  );
}

function json(body: unknown, status = 200): Response {
  return new Response(JSON.stringify(body), {
    status,
    headers: { "Content-Type": "application/json" },
  });
}
