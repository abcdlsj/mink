import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { RouterProvider, createMemoryHistory } from "@tanstack/react-router";
import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";

import { createAppRouter } from "../router";

const space = {
  id: "019c0000-0000-7000-8000-000000000001",
  name: "Sumi Lab",
  slug: "sumi-lab",
  accent: "#A9D877",
  owner_member_id: "019c0000-0000-7000-8000-000000000002",
  current_member_id: "019c0000-0000-7000-8000-000000000002",
  general_channel_id: "019c0000-0000-7000-8000-000000000003",
};
const agentId = "019c0000-0000-7000-8000-000000000090";

afterEach(() => vi.unstubAllGlobals());

describe("Agent detail", () => {
  it("edits Role and controls lifecycle while warning about local-only Memory", async () => {
    let current = agent("active", "ready");
    const fetchMock = vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      const path = String(input);
      if (path.includes("/spaces/by-slug/")) return json(space);
      if (path === "/api/v1/auth/me") return json({ id: "user", display_name: "Ada", email: "ada@example.test" });
      if (path.endsWith("/channels") && !init?.method) return json({ can_create: true, channels: [] });
      if (path.endsWith("/dms") && !init?.method) return json([]);
      if (path.endsWith("/members") && !init?.method) return json([{ id: space.owner_member_id, kind: "human", display_name: "Ada", handle: "ada", access_level: "owner", permissions: [] }, { id: agentId, kind: "agent", display_name: "Lin", handle: "lin", access_level: "member", permissions: ["channel.create"] }]);
      if (path.endsWith(`/agents/${agentId}/runs/current`) && !init?.method) return json({
        current_task: { id: "task", title: "Rebuild WebUI" },
        focus: { id: "thread", channel_id: space.general_channel_id, channel_slug: "general", root_message_id: "message", root_message_seq: 42, relation: "source" },
        current_run: { id: "run", agent_member_id: agentId, agent_name: "Lin", focus: { id: "thread", channel_id: space.general_channel_id, channel_slug: "general", root_message_id: "message", root_message_seq: 42, relation: "source" }, status: "running" },
        another_item_waiting: true,
        session_continuity: { state: "warm", generation: 2 },
      });
      if (path.endsWith(`/agents/${agentId}`) && !init?.method) return json(current);
      if (path.endsWith(`/agents/${agentId}`) && init?.method === "DELETE") {
        current = { ...current, computer_id: null, desired_lifecycle: "retired", activity_status: "suspended", retired_at: new Date().toISOString() };
        return json(current);
      }
      if (path.endsWith(`/agents/${agentId}`) && init?.method === "PATCH") {
        const body = JSON.parse(String(init.body));
        current = {
          ...current,
          role_text: body.role_text ?? current.role_text,
          role_revision: body.role_text ? current.role_revision + 1 : current.role_revision,
          desired_lifecycle: body.lifecycle?.action === "suspend" ? "suspended" : body.lifecycle?.action === "retry" ? "active" : current.desired_lifecycle,
          provision_status: body.lifecycle?.action === "suspend" ? "error" : body.lifecycle?.action === "retry" ? "provisioning" : current.provision_status,
          activity_status: body.lifecycle?.action === "suspend" ? "error" : body.lifecycle?.action === "retry" ? "idle" : current.activity_status,
          last_error_code: body.lifecycle?.action === "suspend" ? "driver_unavailable" : undefined,
        };
        return json(current);
      }
      if (path.endsWith(`/agents/${agentId}/memory/read`) && init?.method === "POST") {
        return json({ ...current.memory_files[0], content: "# Memory\n\nKeep the boundary explicit.\n" });
      }
      if (path.includes(`/members/${agentId}/permissions/channel.create`) && init?.method === "DELETE") {
        return json({ id: agentId, kind: "agent", display_name: "Lin", handle: "lin", access_level: "member", permissions: [] });
      }
      throw new Error(`Unexpected request: ${path}`);
    });
    vi.stubGlobal("fetch", fetchMock);
    renderRoute(`/s/sumi-lab/agents/${agentId}`);

    expect(await screen.findByRole("heading", { name: "Lin" })).toBeVisible();
    expect(screen.getAllByRole("img", { name: "Lin avatar" })[0]).toHaveAttribute("data-agent-identicon");
    expect(screen.getAllByRole("status").find((status) => status.textContent === "Running")).toBeVisible();
    expect(screen.getByRole("link", { name: "Message Lin" })).toHaveAttribute("href", `/s/sumi-lab/dm/${agentId}`);
    expect(await screen.findByRole("link", { name: "Rebuild WebUI" })).toHaveAttribute("href", "/s/sumi-lab/tasks/task");
    expect(screen.getByText("Another item is waiting. It is not part of the current Focus.")).toBeVisible();
    const channelPermission = screen.getByRole("button", { name: "channel.create: Granted" });
    fireEvent.click(channelPermission);
    await waitFor(() => expect(fetchMock).toHaveBeenCalledWith(expect.stringContaining(`/permissions/channel.create`), expect.objectContaining({ method: "DELETE" })));
    fireEvent.click(screen.getByRole("button", { name: "Memory" }));
    expect(screen.getByText(/cannot recover it/i)).toBeVisible();
    expect(screen.getByText("MEMORY.md")).toBeVisible();
    fireEvent.click(screen.getByRole("button", { name: "Read MEMORY.md" }));
    expect(await screen.findByText(/Keep the boundary explicit/)).toBeVisible();
    fireEvent.click(screen.getByRole("button", { name: "Settings" }));
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
    fireEvent.click(screen.getByRole("button", { name: /retire permanently/i }));
    await waitFor(() => expect(fetchMock).toHaveBeenCalledWith(
      expect.stringContaining(`/agents/${agentId}`),
      expect.objectContaining({ method: "DELETE" }),
    ));
  });
});

function agent(
  desiredLifecycle: "active" | "suspended" | "retired",
  provisionStatus: "provisioning" | "ready" | "error",
) {
  return {
    member_id: agentId,
    space_id: space.id,
    computer_id: desiredLifecycle === "retired" ? null : "computer",
    name: "Lin",
    handle: "lin",
    access_level: "member",
    role_text: "Review boundaries.",
    role_revision: 1,
    desired_lifecycle: desiredLifecycle,
    provision_status: provisionStatus,
    activity_status: provisionStatus === "error" ? "error" : desiredLifecycle === "active" ? "running" : "suspended",
    driver_kind: "codex",
    attention_config: { dm_immediate: true, mention_immediate: true, ambient_enabled: true, ambient_debounce_seconds: 5, ambient_max_wait_seconds: 30, max_retry_count: 3 },
    created_at: "2026-07-25T00:00:00Z",
    updated_at: "2026-07-25T00:00:00Z",
    retired_at: desiredLifecycle === "retired" ? "2026-07-25T00:00:00Z" : null,
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
