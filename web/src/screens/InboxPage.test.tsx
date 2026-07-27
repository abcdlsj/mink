import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { RouterProvider, createMemoryHistory } from "@tanstack/react-router";
import { fireEvent, render, screen, waitFor, within } from "@testing-library/react";
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
        return json([
          { id: ownerId, kind: "human", display_name: "Ada", handle: "ada", access_level: "owner", permissions: [] },
          { id: "agent", kind: "agent", display_name: "Lin", handle: "lin", access_level: "member", permissions: [] },
        ]);
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

    expect(await screen.findByRole("heading", { name: "Approvals" })).toBeVisible();
    expect(screen.getByRole("img", { name: "Lin avatar" })).toBeVisible();
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

  it("groups attention in product priority order and identifies each sender", async () => {
    const fetchMock = vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      const path = String(input);
      if (path.includes("/spaces/by-slug/")) return json(space);
      if (path === "/api/v1/auth/me") return json({ id: "user", display_name: "Ada", email: "ada@example.test" });
      if (path.endsWith("/channels") && !init?.method) return json({ can_create: true, channels: [] });
      if (path.endsWith("/dms") && !init?.method) return json([]);
      if (path.endsWith("/members") && !init?.method) {
        return json([
          { id: ownerId, kind: "human", display_name: "Ada", handle: "ada", access_level: "owner", permissions: [] },
          { id: "grace", kind: "human", display_name: "Grace", handle: "grace", access_level: "member", permissions: [] },
          { id: "lin", kind: "agent", display_name: "Lin", handle: "lin", access_level: "member", permissions: [] },
        ]);
      }
      if (path.endsWith(`/members/${ownerId}/inbox`)) {
        return json([
          inboxItem("ambient", "channel_activity", "lin", "Lin", "Ambient update"),
          inboxItem("reply", "reply", "grace", "Grace", "A reply"),
          inboxItem("mention", "mention", "lin", "Lin", "Please review"),
        ]);
      }
      if (path.endsWith(`/spaces/${space.id}/approvals`)) return json([]);
      throw new Error(`Unexpected request: ${path}`);
    });
    vi.stubGlobal("fetch", fetchMock);
    renderRoute("/s/sumi-lab/inbox");

    expect(await screen.findByText("Please review")).toBeVisible();
    const workspace = screen.getAllByRole("heading", { name: "Inbox", level: 1 }).at(-1)!.closest(".inbox-workspace")!;
    const view = within(workspace as HTMLElement);
    const headings = view.getAllByRole("heading", { level: 2 }).map((heading) => heading.textContent);
    expect(headings).toEqual(["DM & mentions", "Replies", "Channel activity"]);
    expect(view.getAllByRole("img", { name: "Lin avatar" })).toHaveLength(2);
    expect(view.getByRole("img", { name: "Grace avatar" })).toBeVisible();
    expect(view.getAllByRole("button", { name: "Open #general from Lin" })).toHaveLength(2);
  });
});

function inboxItem(id: string, kind: string, senderId: string, senderName: string, summary: string) {
  return {
    id,
    member_id: ownerId,
    kind,
    priority: kind === "channel_activity" ? "ambient" : "hard",
    channel_id: space.general_channel_id,
    channel_slug: "general",
    message_id: `${id}-message`,
    sender_member_id: senderId,
    sender_display_name: senderName,
    summary,
    status: "pending",
    available_at: "2026-07-25T00:00:00Z",
    created_at: "2026-07-25T00:00:00Z",
  };
}

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
