import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { RouterProvider, createMemoryHistory } from "@tanstack/react-router";
import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";

import { createAppRouter } from "../router";

const ownerId = "019c0000-0000-7000-8000-000000000002";
const space = {
  id: "019c0000-0000-7000-8000-000000000001",
  name: "Sumi Lab",
  slug: "sumi-lab",
  accent: "#5065D8",
  owner_member_id: ownerId,
  current_member_id: ownerId,
  general_channel_id: "019c0000-0000-7000-8000-000000000003",
};
const approvalId = "019c0000-0000-7000-8000-000000000090";

afterEach(() => vi.unstubAllGlobals());

describe("Approval governance", () => {
  it("shows pending Agent creation and lets a Human Owner approve it", async () => {
    let status = "pending";
    const fetchMock = vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      const path = String(input);
      if (path.includes("/spaces/by-slug/")) return json(space);
      if (path === "/api/v1/auth/me") {
        return json({ id: "user", display_name: "Ada", email: "ada@example.test" });
      }
      if (path.endsWith("/channels") && !init?.method) return json({ can_create: true, channels: [] });
      if (path.endsWith("/dms") && !init?.method) return json([]);
      if (path.endsWith("/members") && !init?.method) {
        return json([{ id: ownerId, kind: "human", display_name: "Ada", handle: "ada", access_level: "owner", permissions: [] }]);
      }
      if (path.endsWith(`/members/${ownerId}/inbox`)) {
        return json([{ id: "inbox", member_id: ownerId, kind: "approval", priority: "hard", approval_id: approvalId, status: "pending", available_at: "2026-07-25T00:00:00Z", created_at: "2026-07-25T00:00:00Z" }]);
      }
      if (path.endsWith(`/spaces/${space.id}/approvals`)) return json([approval(status)]);
      if (path.endsWith(`/approvals/${approvalId}/approve`) && init?.method === "POST") {
        status = "approved";
        return json(approval(status));
      }
      throw new Error(`Unexpected request: ${path}`);
    });
    vi.stubGlobal("fetch", fetchMock);
    renderRoute("/s/sumi-lab/inbox");

    expect(await screen.findByRole("heading", { name: "Agent creation" })).toBeVisible();
    expect(screen.getByText("Reviewer")).toBeVisible();
    fireEvent.click(screen.getByRole("button", { name: "Approve Reviewer" }));
    await waitFor(() => {
      expect(fetchMock).toHaveBeenCalledWith(
        expect.stringContaining(`/approvals/${approvalId}/approve`),
        expect.objectContaining({ method: "POST" }),
      );
    });
    await waitFor(() => expect(screen.queryByText("Reviewer")).not.toBeInTheDocument());
  });

  it("explains an empty Inbox instead of looking like missing content", async () => {
    const fetchMock = vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      const path = String(input);
      if (path.includes("/spaces/by-slug/")) return json(space);
      if (path === "/api/v1/auth/me") {
        return json({ id: "user", display_name: "Ada", email: "ada@example.test" });
      }
      if (path.endsWith("/channels") && !init?.method) return json({ can_create: true, channels: [] });
      if (path.endsWith("/dms") && !init?.method) return json([]);
      if (path.endsWith("/members") && !init?.method) {
        return json([{ id: ownerId, kind: "human", display_name: "Ada", handle: "ada", access_level: "owner", permissions: [] }]);
      }
      if (path.endsWith(`/members/${ownerId}/inbox`)) return json([]);
      if (path.endsWith(`/spaces/${space.id}/approvals`)) return json([]);
      throw new Error(`Unexpected request: ${path}`);
    });
    vi.stubGlobal("fetch", fetchMock);
    renderRoute("/s/sumi-lab/inbox");

    expect(await screen.findByRole("heading", { name: "Nothing needs your attention" })).toBeVisible();
    expect(screen.getByText(/not your Message history/i)).toBeVisible();
    const groups = screen.getByRole("list", { name: "Empty Inbox groups" });
    expect(groups).toHaveTextContent("Approvals");
    expect(groups).toHaveTextContent("DM & mentions");
    expect(groups).toHaveTextContent("Replies");
    expect(groups).toHaveTextContent("Channel activity");
    expect(screen.getAllByText("0")).toHaveLength(4);
  });
});

function approval(status: string) {
  return {
    id: approvalId,
    space_id: space.id,
    type: "agent.create",
    requested_by_member_id: "agent",
    requester_name: "Lin",
    payload: { computer_id: "computer", name: "Reviewer", role_text: "Review changes.", driver_kind: "codex", access_level: "member", permissions: [] },
    status,
    created_at: "2026-07-25T00:00:00Z",
  };
}

function renderRoute(path: string) {
  const router = createAppRouter(createMemoryHistory({ initialEntries: [path] }));
  const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } });
  render(<QueryClientProvider client={queryClient}><RouterProvider router={router} /></QueryClientProvider>);
}

function json(body: unknown): Response {
  return new Response(JSON.stringify(body), { status: 200, headers: { "Content-Type": "application/json" } });
}
