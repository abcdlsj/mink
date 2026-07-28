import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { RouterProvider, createMemoryHistory } from "@tanstack/react-router";
import { render, screen, within } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";

import { createAppRouter } from "../router";

const ownerId = "019c0000-0000-7000-8000-000000000002";
const spaceId = "019c0000-0000-7000-8000-000000000001";

afterEach(() => vi.unstubAllGlobals());

describe("Task board", () => {
  it("groups the minimal statuses and links each Task to its source root Message", async () => {
    vi.stubGlobal("fetch", vi.fn(async (input: RequestInfo | URL) => {
      const path = String(input);
      if (path.includes("/spaces/by-slug/")) return json({ id: spaceId, name: "Sumi Lab", slug: "sumi-lab", accent: "#FE7DA8", owner_member_id: ownerId, current_member_id: ownerId, general_channel_id: "channel" });
      if (path === "/api/v1/auth/me") return json({ id: "user", display_name: "Ada", email: "ada@example.test" });
      if (path.endsWith("/channels")) return json({ can_create: true, channels: [] });
      if (path.endsWith("/dms")) return json([]);
      if (path.endsWith("/members")) return json([{ id: ownerId, kind: "human", display_name: "Ada", handle: "ada", access_level: "owner", permissions: [] }]);
      if (path.endsWith("/agents")) return json([]);
      if (path.endsWith("/tasks")) return json([
        task("open", "Design claim flow", 17),
        task("in_progress", "Wire Agent CLI", 18, "Lin"),
        task("done", "Add board", 19, "Reviewer"),
      ]);
      throw new Error(`Unexpected request: ${path}`);
    }));

    renderRoute("/s/sumi-lab/tasks");

    expect(await screen.findByRole("heading", { name: "Tasks", level: 1 })).toBeVisible();
    const board = (await screen.findByText("Design claim flow")).closest<HTMLElement>(".task-board")!;
    expect(within(board).getByRole("heading", { name: "Open" })).toBeVisible();
    expect(within(board).getByRole("heading", { name: "In progress" })).toBeVisible();
    expect(within(board).getByRole("heading", { name: "Done" })).toBeVisible();
    expect(within(board).getByRole("heading", { name: "Canceled" })).toBeVisible();
    expect(screen.getByRole("link", { name: "#general @17" })).toHaveAttribute(
      "href",
      "/s/sumi-lab/channels/general#message-message-open",
    );
    expect(screen.getByText("Assigned to Lin")).toBeVisible();
    expect(screen.getByText("Unassigned")).toBeVisible();
  });
});

function task(status: string, title: string, seq: number, assignee?: string) {
  return {
    id: `task-${status}`,
    space_id: spaceId,
    source_message_id: `message-${status}`,
    channel_id: "channel",
    channel_slug: "general",
    source_seq: seq,
    title,
    status,
    created_by_member_id: "agent",
    creator_name: "PM",
    assigned_agent_member_id: assignee ? "assignee" : undefined,
    assignee_name: assignee,
    created_at: "2026-07-28T00:00:00Z",
    updated_at: "2026-07-28T00:00:00Z",
  };
}

function renderRoute(path: string) {
  const router = createAppRouter(createMemoryHistory({ initialEntries: [path] }));
  const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  render(<QueryClientProvider client={queryClient}><RouterProvider router={router} /></QueryClientProvider>);
}

function json(body: unknown): Response {
  return new Response(JSON.stringify(body), { status: 200, headers: { "Content-Type": "application/json" } });
}
