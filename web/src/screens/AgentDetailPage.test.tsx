import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { RouterProvider, createMemoryHistory } from "@tanstack/react-router";
import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";

import { createAppRouter } from "../router";

const space = {
  id: "019c0000-0000-7000-8000-000000000001",
  name: "Sumi Lab",
  slug: "sumi-lab",
  accent: "#64D9E8",
  owner_member_id: "019c0000-0000-7000-8000-000000000002",
  current_member_id: "019c0000-0000-7000-8000-000000000002",
  general_channel_id: "019c0000-0000-7000-8000-000000000003",
};
const agentId = "019c0000-0000-7000-8000-000000000090";

afterEach(() => vi.unstubAllGlobals());

describe("Agent detail", () => {
  it("edits Role and controls lifecycle while warning about local-only Memory", async () => {
    let current = agent("active");
    const fetchMock = vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      const path = String(input);
      if (path.includes("/spaces/by-slug/")) return json(space);
      if (path === "/api/v1/auth/me") return json({ id: "user", display_name: "Ada", email: "ada@example.test" });
      if (path.endsWith("/channels") && !init?.method) return json({ can_create: true, channels: [] });
      if (path.endsWith("/dms") && !init?.method) return json([]);
      if (path.endsWith("/members") && !init?.method) return json([{ id: space.owner_member_id, kind: "human", display_name: "Ada", handle: "ada", access_level: "owner", permissions: [] }]);
      if (path.endsWith(`/agents/${agentId}`) && !init?.method) return json(current);
      if (path.endsWith(`/agents/${agentId}`) && init?.method === "PATCH") {
        const body = JSON.parse(String(init.body));
        current = {
          ...current,
          role_text: body.role_text ?? current.role_text,
          role_revision: body.role_text ? current.role_revision + 1 : current.role_revision,
          status: body.lifecycle?.action === "suspend" ? "error" : body.lifecycle?.action === "retry" ? "provisioning" : current.status,
          last_error_code: body.lifecycle?.action === "suspend" ? "driver_unavailable" : undefined,
        };
        return json(current);
      }
      if (path.endsWith(`/agents/${agentId}/memory/read`) && init?.method === "POST") {
        return json({ ...current.memory_files[0], content: "# Memory\n\nKeep the boundary explicit.\n" });
      }
      throw new Error(`Unexpected request: ${path}`);
    });
    vi.stubGlobal("fetch", fetchMock);
    renderRoute(`/s/sumi-lab/agents/${agentId}`);

    expect(await screen.findByRole("heading", { name: "Lin" })).toBeVisible();
    expect(screen.getByText(/cannot recover it/i)).toBeVisible();
    expect(screen.getByText("MEMORY.md")).toBeVisible();
    fireEvent.click(screen.getByRole("button", { name: "Read MEMORY.md" }));
    expect(await screen.findByText(/Keep the boundary explicit/)).toBeVisible();
    fireEvent.change(screen.getByLabelText("Role"), { target: { value: "Enforce the specification." } });
    fireEvent.click(screen.getByRole("button", { name: /save configuration/i }));
    await waitFor(() => expect(fetchMock).toHaveBeenCalledWith(
      expect.stringContaining(`/agents/${agentId}`),
      expect.objectContaining({ method: "PATCH", body: expect.stringContaining("Enforce the specification") }),
    ));
    fireEvent.click(screen.getByLabelText(/cancel the active run now/i));
    fireEvent.click(screen.getByRole("button", { name: "Suspend" }));
    await waitFor(() => expect(fetchMock).toHaveBeenCalledWith(
      expect.stringContaining(`/agents/${agentId}`),
      expect.objectContaining({ body: JSON.stringify({ lifecycle: { action: "suspend", mode: "cancel_now" } }) }),
    ));
    expect(await screen.findByText("driver_unavailable")).toBeVisible();
    fireEvent.click(screen.getByRole("button", { name: /retry provision/i }));
    await waitFor(() => expect(fetchMock).toHaveBeenCalledWith(
      expect.stringContaining(`/agents/${agentId}`),
      expect.objectContaining({ body: JSON.stringify({ lifecycle: { action: "retry" } }) }),
    ));
  });
});

function agent(status: "provisioning" | "active" | "suspended" | "error" | "retired") {
  return {
    member_id: agentId,
    space_id: space.id,
    computer_id: "computer",
    name: "Lin",
    handle: "lin",
    access_level: "member",
    role_text: "Review boundaries.",
    role_revision: 1,
    status,
    driver_kind: "codex",
    attention_config: { dm_immediate: true, mention_immediate: true, ambient_enabled: true, ambient_debounce_seconds: 5, ambient_max_wait_seconds: 30, max_retry_count: 3 },
    created_at: "2026-07-25T00:00:00Z",
    updated_at: "2026-07-25T00:00:00Z",
    last_error_code: undefined as string | undefined,
    memory_files: [{ path: "MEMORY.md", size: 9, sha256: "d7870cdadd1a", updated_at: "2026-07-25T00:00:00Z" }],
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
