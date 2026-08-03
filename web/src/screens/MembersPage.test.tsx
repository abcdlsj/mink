import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { RouterProvider, createMemoryHistory } from "@tanstack/react-router";
import { cleanup, render, screen } from "@testing-library/react";
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
const linId = "019c0000-0000-7000-8000-000000000020";
const reviewerId = "019c0000-0000-7000-8000-000000000021";
const computerId = "019c0000-0000-7000-8000-000000000040";

afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
});

describe("Agent directory", () => {
  it("shows Agent summary facts and links each Agent to management", async () => {
    const fetchMock = vi.fn(async (input: RequestInfo | URL) => {
      const path = String(input);
      if (path.includes("/spaces/by-slug/")) return json(space);
      if (path === "/api/v1/auth/me") return json({ id: "user", display_name: "Ada", email: "ada@example.test" });
      if (path.endsWith("/channels")) return json({ can_create: true, channels: [] });
      if (path.endsWith("/dms")) return json([]);
      if (path.endsWith("/members")) return json([
        { id: space.owner_member_id, kind: "human", display_name: "Ada", access_level: "owner", permissions: [] },
        { id: linId, kind: "agent", display_name: "Lin", access_level: "member", permissions: [] },
        { id: reviewerId, kind: "agent", display_name: "Reviewer", access_level: "admin", permissions: [] },
      ]);
      if (path.endsWith("/agents")) return json([
        agent({ member_id: linId, name: "Lin", activity_status: "working", computer_reachable: true }),
        agent({ member_id: reviewerId, name: "Reviewer", activity_status: "idle", computer_reachable: false, computer_id: null, last_error_code: "computer_offline" }),
      ]);
      throw new Error(`Unexpected request: ${path}`);
    });
    vi.stubGlobal("fetch", fetchMock);
    renderRoute("/s/sumi-lab/agents");

    expect(await screen.findByRole("heading", { name: "Agents", level: 1 })).toBeVisible();
    const summary = screen.getByLabelText("Agent directory summary");
    expect(summary).toHaveTextContent("Agents2");
    expect(summary).toHaveTextContent("Working1");
    expect(summary).toHaveTextContent("Computers online1");
    expect(summary).toHaveTextContent("Needs attention1");

    const manageLinks = screen.getAllByRole("link", { name: "Manage" });
    expect(manageLinks).toHaveLength(2);
    expect(manageLinks[0]).toHaveAttribute("href", `/s/sumi-lab/agents/${linId}`);
    expect(manageLinks[1]).toHaveAttribute("href", `/s/sumi-lab/agents/${reviewerId}`);
    expect(screen.getByText("Computer online")).toBeVisible();
    expect(screen.getByText("No Computer assigned")).toBeVisible();
  });

  it("keeps Members grouped while exposing Agent management links", async () => {
    const fetchMock = vi.fn(async (input: RequestInfo | URL) => {
      const path = String(input);
      if (path.includes("/spaces/by-slug/")) return json(space);
      if (path === "/api/v1/auth/me") return json({ id: "user", display_name: "Ada", email: "ada@example.test" });
      if (path.endsWith("/channels")) return json({ can_create: true, channels: [] });
      if (path.endsWith("/dms")) return json([]);
      if (path.endsWith("/members")) return json([
        { id: space.owner_member_id, kind: "human", display_name: "Ada", access_level: "owner", permissions: [] },
        { id: linId, kind: "agent", display_name: "Lin", access_level: "member", permissions: [] },
      ]);
      if (path.endsWith("/agents")) return json([agent({ member_id: linId, name: "Lin", activity_status: "idle", computer_reachable: true })]);
      throw new Error(`Unexpected request: ${path}`);
    });
    vi.stubGlobal("fetch", fetchMock);
    renderRoute("/s/sumi-lab/members");

    expect(await screen.findByRole("heading", { name: "Members", level: 1 })).toBeVisible();
    expect(screen.getByRole("heading", { name: "Agents", level: 2 })).toBeVisible();
    expect(screen.getByRole("heading", { name: "Humans", level: 2 })).toBeVisible();
    expect(screen.getByRole("link", { name: "Manage" })).toHaveAttribute("href", `/s/sumi-lab/agents/${linId}`);
  });
});

function agent(overrides: Record<string, unknown>) {
  return {
    member_id: linId,
    space_id: space.id,
    computer_id: computerId,
    name: "Lin",
    access_level: "member",
    role_text: "Review boundaries.",
    role_revision: 1,
    desired_lifecycle: "active",
    provision_status: "ready",
    activity_status: "idle",
    driver_kind: "codex",
    computer_reachable: true,
    attention_config: { dm_immediate: true, mention_immediate: true, ambient_enabled: true, ambient_debounce_seconds: 5, ambient_max_wait_seconds: 30, max_retry_count: 3 },
    created_at: "2026-07-25T00:00:00Z",
    updated_at: "2026-07-25T00:00:00Z",
    retired_at: null,
    last_error_code: undefined,
    memory_files: [],
    ...overrides,
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
