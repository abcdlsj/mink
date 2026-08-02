import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { RouterProvider, createMemoryHistory } from "@tanstack/react-router";
import { fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";

import { createAppRouter } from "../router";

const space = {
  id: "019c0000-0000-7000-8000-000000000001",
  name: "Sumi Lab",
  slug: "sumi-lab",
  accent: "#FE7DA8",
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
      if (path.endsWith("/computers") && !init?.method) return json([]);
      if (path.endsWith("/agents") && !init?.method) {
        return json([{
          member_id: "019c0000-0000-7000-8000-000000000030",
          activity_status: "idle",
        }]);
      }
      if (path.endsWith("/members") && !init?.method) {
        return json([
          {
            id: space.owner_member_id,
            kind: "human",
            display_name: "Ada Lovelace",
            access_level: "owner",
            permissions: [],
          },
          {
            id: "019c0000-0000-7000-8000-000000000020",
            kind: "human",
            display_name: "Grace Hopper",
            access_level: "member",
            permissions: ["channel.create"],
          },
          {
            id: "019c0000-0000-7000-8000-000000000030",
            kind: "agent",
            display_name: "Lin",
            access_level: "member",
            permissions: [],
          },
        ]);
      }
      if (init?.method === "PATCH") {
        return json({
          id: "019c0000-0000-7000-8000-000000000020",
          kind: "human",
          display_name: "Grace Hopper",
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

    expect(await screen.findByRole("heading", { name: "Members", level: 1 })).toBeVisible();
    expect((await screen.findAllByText("Grace Hopper"))[0]).toBeVisible();
    expect(within(screen.getByRole("complementary", { name: "Space tools" })).getByRole("link", { name: "Members" })).toHaveAttribute("aria-current", "page");
    expect(within(screen.getByRole("complementary", { name: "Space navigation" })).getByRole("link", { name: "Members" })).toHaveAttribute("aria-current", "page");
    expect(within(screen.getByRole("complementary", { name: "Space navigation" })).getByRole("heading", { name: /Agents/ })).toHaveTextContent("1");
    expect(within(screen.getByRole("complementary", { name: "Space navigation" })).getByRole("link", { name: /Lin avatar.*Lin is Idle.*Lin/i })).toHaveAttribute("href", "/s/sumi-lab/agents/019c0000-0000-7000-8000-000000000030");
    const linIdenticons = screen.getAllByRole("img", { name: "Lin avatar" }).map((avatar) => avatar.getAttribute("data-agent-identicon"));
    expect(linIdenticons.every(Boolean)).toBe(true);
    expect(new Set(linIdenticons)).toHaveLength(1);
    expect(screen.getByRole("link", { name: "Create Agent" })).toHaveAttribute("href", "/s/sumi-lab/computers#create-agent");
    const access = screen.getByRole("combobox", { name: "Access level for Grace Hopper" });
    expect(access).toHaveValue("member");
    // Permissions are managed in Agent detail; the Members list stays clean.
    const graceRow = screen.getAllByRole("article").find((row) => row.textContent?.includes("Grace Hopper"))!;
    expect(within(graceRow).queryByRole("checkbox")).not.toBeInTheDocument();

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

    fireEvent.click(within(graceRow!).getByRole("button", { name: /message/i }));
    await waitFor(() => {
      expect(fetchMock).toHaveBeenCalledWith(
        expect.stringContaining(`/spaces/${space.id}/dms`),
        expect.objectContaining({ method: "POST" }),
      );
    });

    fireEvent.click(screen.getAllByRole("link", { name: "Computers" })[0]);
    expect(await screen.findByRole("heading", { name: "Computers" })).toBeVisible();
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

  it("shows Human Inbox attention and opens the source", async () => {
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
        return json([{ id: space.owner_member_id, kind: "human", display_name: "Ada Lovelace", access_level: "owner", permissions: [] }]);
      }
      if (path.endsWith(`/members/${space.owner_member_id}/inbox`) && !init?.method) {
        return json([{ id: itemId, member_id: space.owner_member_id, kind: "mention", priority: "hard", channel_id: space.general_channel_id, channel_slug: "general", message_id: "message", sender_member_id: "grace", sender_display_name: "Grace Hopper", summary: "Please review this boundary.", status: "pending", available_at: "2026-07-25T00:00:00Z", created_at: "2026-07-25T00:00:00Z" }]);
      }
      if (path.endsWith(`/channels/${space.general_channel_id}/messages`) && !init?.method) {
        return json({ messages: [], has_more: false });
      }
      throw new Error(`Unexpected request: ${path}`);
    });
    vi.stubGlobal("fetch", fetchMock);
    renderRoute("/s/sumi-lab/inbox");

    expect(await screen.findByText("Please review this boundary.")).toBeVisible();
    expect(screen.queryByRole("button", { name: "Complete Inbox Item" })).toBeNull();
    fireEvent.click(screen.getByRole("button", { name: /Open #general/ }));
    await waitFor(() => {
      expect(fetchMock).toHaveBeenCalledWith(
        expect.stringContaining(`/channels/${space.general_channel_id}/messages`),
        expect.anything(),
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
