import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { RouterProvider, createMemoryHistory } from "@tanstack/react-router";
import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { readFileSync } from "node:fs";
import { join } from "node:path";
import { afterEach, describe, expect, it, vi } from "vitest";

import { clearAgentActivity, recordAgentActivity } from "../agentActivity";
import { createAppRouter } from "../router";

const space = {
  id: "019c0000-0000-7000-8000-000000000001",
  name: "Sumi Lab",
  slug: "sumi-lab",
  accent: "#3C9E8F",
  owner_member_id: "019c0000-0000-7000-8000-000000000002",
  current_member_id: "019c0000-0000-7000-8000-000000000002",
  general_channel_id: "019c0000-0000-7000-8000-000000000003",
};
const agentId = "019c0000-0000-7000-8000-000000000090";

afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
  clearAgentActivity();
});

describe("Agent detail", () => {
  it("keeps Agent DMs and permissions in separate Overview rows", () => {
    const css = readFileSync(join(process.cwd(), "src/styles.css"), "utf8");
    const start = css.indexOf(".agent-overview-grid {\n  width: min(100%, 1080px);");
    const end = css.indexOf("@media (max-width: 780px)", start);
    const desktopOverviewRules = css.slice(start, end);

    expect(start).toBeGreaterThanOrEqual(0);
    expect(end).toBeGreaterThan(start);
    expect(desktopOverviewRules).toMatch(/\.agent-overview-grid > \.agent-direct-messages\s*\{[^}]*grid-row:\s*3/s);
    expect(desktopOverviewRules).toMatch(/\.agent-overview-grid > \.agent-permissions\s*\{[^}]*grid-row:\s*4/s);
  });

  it("edits Role and controls lifecycle while warning about local-only Memory", async () => {
    let current = agent("active", "ready");
    const fetchMock = vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      const path = String(input);
      if (path.includes("/spaces/by-slug/")) return json(space);
      if (path === "/api/v1/auth/me") return json({ id: "user", display_name: "Ada", email: "ada@example.test" });
      if (path.endsWith("/channels") && !init?.method) return json({ can_create: true, channels: [] });
      if (path.endsWith("/dms") && !init?.method) return json([]);
      if (path.endsWith("/members") && !init?.method) return json([{ id: space.owner_member_id, kind: "human", display_name: "Ada", access_level: "owner", permissions: [] }, { id: agentId, kind: "agent", display_name: "Lin", access_level: "member", permissions: ["channel.create"] }]);
      if (path.endsWith(`/agents/${agentId}/runs/current`) && !init?.method) return json({
        current_task: { id: "task", title: "Rebuild WebUI" },
        focus: { id: "thread", channel_id: space.general_channel_id, channel_slug: "general", root_message_id: "message", root_message_seq: 42, relation: "source" },
        current_run: { id: "run", agent_member_id: agentId, agent_name: "Lin", focus: { id: "thread", channel_id: space.general_channel_id, channel_slug: "general", root_message_id: "message", root_message_seq: 42, relation: "source" }, status: "working" },
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
          last_error_code: body.lifecycle?.action === "suspend" ? "driver_unavailable" : current.last_error_code,
        };
        return json(current);
      }
      if (path.endsWith(`/agents/${agentId}/memory/read`) && init?.method === "POST") {
        return json({ ...current.memory_files[0], content: "# Memory\n\nKeep the boundary explicit.\n" });
      }
      if (path.includes(`/members/${agentId}/permissions/channel.create`) && init?.method === "DELETE") {
        return json({ id: agentId, kind: "agent", display_name: "Lin", access_level: "member", permissions: [] });
      }
      throw new Error(`Unexpected request: ${path}`);
    });
    vi.stubGlobal("fetch", fetchMock);
    renderRoute(`/s/sumi-lab/agents/${agentId}`);

    expect(await screen.findByRole("heading", { name: "Lin" })).toBeVisible();
    expect(screen.getAllByRole("img", { name: "Lin avatar" })[0]).toHaveAttribute("data-agent-identicon");
    expect(screen.getAllByRole("status").find((status) => status.textContent === "Working")).toBeVisible();
    fireEvent.click(screen.getByRole("tab", { name: "Overview" }));
    expect(await screen.findByRole("link", { name: "Rebuild WebUI" })).toHaveAttribute("href", "/s/sumi-lab/tasks/task");
    expect(screen.getByText("Another item is waiting. It is not part of the current Focus.")).toBeVisible();
    const channelPermission = screen.getByRole("checkbox", { name: "channel.create permission" });
    expect(channelPermission).toBeChecked();
    fireEvent.click(channelPermission);
    await waitFor(() => expect(fetchMock).toHaveBeenCalledWith(expect.stringContaining(`/permissions/channel.create`), expect.objectContaining({ method: "DELETE" })));
    fireEvent.click(screen.getByRole("tab", { name: "Memory" }));
    expect(screen.getByText(/cannot recover it/i)).toBeVisible();
    expect(screen.getByText("MEMORY.md")).toBeVisible();
    fireEvent.click(screen.getByRole("button", { name: "Read MEMORY.md" }));
    expect(await screen.findByText(/Keep the boundary explicit/)).toBeVisible();
    fireEvent.click(screen.getByRole("tab", { name: "Settings" }));
    fireEvent.click(screen.getByRole("button", { name: "Restart Agent" }));
    await waitFor(() => expect(fetchMock).toHaveBeenCalledWith(
      expect.stringContaining(`/agents/${agentId}`),
      expect.objectContaining({ body: JSON.stringify({ lifecycle: { action: "restart" } }) }),
    ));
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
    fireEvent.click(screen.getByRole("button", { name: "Restart Agent" }));
    await waitFor(() => expect(fetchMock.mock.calls.filter(([, init]) => init?.body === JSON.stringify({ lifecycle: { action: "restart" } }))).toHaveLength(2));
    expect(await screen.findByRole("status", { name: "Agent error: driver_unavailable" })).toBeVisible();
    fireEvent.click(screen.getByRole("button", { name: /retry provision/i }));
    await waitFor(() => expect(fetchMock).toHaveBeenCalledWith(
      expect.stringContaining(`/agents/${agentId}`),
      expect.objectContaining({ body: JSON.stringify({ lifecycle: { action: "retry" } }) }),
    ));
    fireEvent.click(screen.getByRole("button", { name: /retire permanently/i }));
    expect(screen.getByRole("dialog", { name: "Retire Lin?" })).toBeVisible();
    const deleteCallsBeforeCancel = fetchMock.mock.calls.filter(([, init]) => init?.method === "DELETE").length;
    fireEvent.click(screen.getByRole("button", { name: "Cancel" }));
    expect(screen.queryByRole("dialog", { name: "Retire Lin?" })).not.toBeInTheDocument();
    expect(fetchMock.mock.calls.filter(([, init]) => init?.method === "DELETE")).toHaveLength(deleteCallsBeforeCancel);
    fireEvent.click(screen.getByRole("button", { name: /retire permanently/i }));
    fireEvent.click(screen.getByRole("button", { name: "Retire Lin" }));
    await waitFor(() => expect(fetchMock).toHaveBeenCalledWith(
      expect.stringContaining(`/agents/${agentId}`),
      expect.objectContaining({ method: "DELETE" }),
    ));
  });

  it("shows Activity in its default tab with command arguments", async () => {
    const channel = {
      id: space.general_channel_id,
      space_id: space.id,
      slug: "general",
      kind: "public",
      name: "General",
      joined: true,
    };
    const fetchMock = vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      const path = String(input);
      if (path.includes("/spaces/by-slug/")) return json(space);
      if (path === "/api/v1/auth/me") return json({ id: "user", display_name: "Ada", email: "ada@example.test" });
      if (path.endsWith("/channels") && !init?.method) return json({ can_create: true, channels: [channel] });
      if (path.endsWith("/dms") && !init?.method) return json([]);
      if (path.endsWith("/members") && !init?.method) return json([{ id: space.owner_member_id, kind: "human", display_name: "Ada", access_level: "owner", permissions: [] }, { id: agentId, kind: "agent", display_name: "Lin", access_level: "member", permissions: [] }]);
      if (path.endsWith(`/agents/${agentId}/runs/current`) && !init?.method) return json({
        current_task: null,
        focus: null,
        current_run: null,
        another_item_waiting: false,
        session_continuity: { state: "unavailable", generation: 0 },
      });
      if (path.endsWith(`/agents/${agentId}`) && !init?.method) return json(agent("active", "ready"));
      throw new Error(`Unexpected request: ${path}`);
    });
    vi.stubGlobal("fetch", fetchMock);
    recordAgentActivity({
      event_id: "evt-2",
      occurred_at: "2026-08-03T00:01:00Z",
      data: { member_id: agentId, kind: "task.done", task_id: "task" },
    });
    recordAgentActivity({
      event_id: "evt-1",
      occurred_at: "2026-08-03T00:00:00Z",
      data: {
        member_id: agentId,
        kind: "message.send",
        run_id: "run-1",
        channel_id: space.general_channel_id,
        thread_id: "thread-1",
        message_id: "message-1",
        arguments: [
          { name: "target", value: "#general:12" },
          { name: "attachment_count", value: "0" },
        ],
        message_preview: "The Agent sent this message.",
      },
    });
    renderRoute(`/s/sumi-lab/agents/${agentId}`);

    expect(await screen.findByRole("heading", { name: "Lin" })).toBeVisible();
    expect(screen.getByRole("tab", { name: "Activity" })).toHaveAttribute("aria-selected", "true");
    expect(screen.getByRole("heading", { name: "Activity" })).toBeVisible();
    expect(screen.getByText("Completed task")).toBeVisible();
    expect(screen.getByRole("link", { name: "View task" })).toHaveAttribute("href", "/s/sumi-lab/tasks/task");
    expect(screen.getByText("Sent a message")).toHaveClass("agent-activity-kind");
    expect(screen.getByRole("link", { name: "#general" })).toHaveAttribute("href", "/s/sumi-lab/channels/general#message-message-1");
    expect(screen.getByText("target")).toBeVisible();
    expect(screen.getByText("#general:12")).toBeVisible();
    expect(screen.getByText("The Agent sent this message.")).toBeVisible();
    expect(screen.getByRole("list", { name: "Arguments" })).toHaveClass("agent-activity-arguments");
    expect(screen.getByText("The Agent sent this message.").closest("pre")).toHaveClass("agent-activity-message");
    expect(screen.queryByText("run-1")).not.toBeInTheDocument();
    const activityList = screen.getByRole("list", { name: "Agent activity" });
    expect(activityList).toHaveClass("agent-activity-list");
    expect(activityList.textContent!.indexOf("Completed task")).toBeLessThan(activityList.textContent!.indexOf("Sent a message"));

    fireEvent.click(screen.getByRole("tab", { name: "Overview" }));
    expect(screen.getByRole("tab", { name: "Overview" })).toHaveAttribute("aria-selected", "true");
    expect(screen.queryByRole("heading", { name: "Activity" })).not.toBeInTheDocument();
    expect(screen.getByRole("heading", { name: "Current work" })).toBeVisible();

    fireEvent.click(screen.getByRole("tab", { name: "Activity" }));
    expect(screen.getByRole("heading", { name: "Activity" })).toBeVisible();
  });

  it("renders a DM Focus without a channel link", async () => {
    const fetchMock = vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      const path = String(input);
      if (path.includes("/spaces/by-slug/")) return json(space);
      if (path === "/api/v1/auth/me") return json({ id: "user", display_name: "Ada", email: "ada@example.test" });
      if (path.endsWith("/channels") && !init?.method) return json({ can_create: true, channels: [] });
      if (path.endsWith("/dms") && !init?.method) return json([]);
      if (path.endsWith("/members") && !init?.method) return json([{ id: space.owner_member_id, kind: "human", display_name: "Ada", access_level: "owner", permissions: [] }, { id: agentId, kind: "agent", display_name: "Lin", access_level: "member", permissions: [] }]);
      if (path.endsWith(`/agents/${agentId}/runs/current`) && !init?.method) return json({
        current_task: null,
        focus: { id: "thread", channel_id: "dm-channel", channel_slug: null, root_message_id: "message", root_message_seq: 7, relation: "source" },
        current_run: { id: "run", agent_member_id: agentId, agent_name: "Lin", focus: { id: "thread", channel_id: "dm-channel", channel_slug: null, root_message_id: "message", root_message_seq: 7, relation: "source" }, status: "working" },
        another_item_waiting: false,
        session_continuity: { state: "warm", generation: 1 },
      });
      if (path.endsWith(`/agents/${agentId}`) && !init?.method) return json(agent("active", "ready"));
      throw new Error(`Unexpected request: ${path}`);
    });
    vi.stubGlobal("fetch", fetchMock);
    renderRoute(`/s/sumi-lab/agents/${agentId}`);

    fireEvent.click(await screen.findByRole("tab", { name: "Overview" }));
    expect(await screen.findByText("DM · message 7")).toBeVisible();
    expect(screen.queryByRole("link", { name: /#.*:7/ })).not.toBeInTheDocument();
  });

  it("shows Agent-Agent DM metadata only in the Agent management view", async () => {
    const peerId = "019c0000-0000-7000-8000-000000000091";
    const fetchMock = vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      const path = String(input);
      if (path.includes("/spaces/by-slug/")) return json(space);
      if (path === "/api/v1/auth/me") return json({ id: "user", display_name: "Ada", email: "ada@example.test" });
      if (path.endsWith("/channels") && !init?.method) return json({ can_create: true, channels: [] });
      if (path.endsWith("/dms") && !init?.method) {
        if (path.endsWith(`/members/${agentId}/dms`)) {
          return json([{
            channel_id: "019c0000-0000-7000-8000-000000000092",
            space_id: space.id,
            other_member: { id: peerId, kind: "agent", display_name: "Mira", access_level: "member", permissions: [] },
            created_at: "2026-08-03T00:00:00Z",
          }]);
        }
        return json([]);
      }
      if (path.endsWith("/members") && !init?.method) return json([
        { id: space.owner_member_id, kind: "human", display_name: "Ada", access_level: "owner", permissions: [] },
        { id: agentId, kind: "agent", display_name: "Lin", access_level: "member", permissions: [] },
        { id: peerId, kind: "agent", display_name: "Mira", access_level: "member", permissions: [] },
      ]);
      if (path.endsWith(`/agents/${agentId}/runs/current`) && !init?.method) return json({
        current_task: null,
        focus: null,
        current_run: null,
        another_item_waiting: false,
        session_continuity: { state: "unavailable", generation: 0 },
      });
      if (path.endsWith(`/agents/${agentId}`) && !init?.method) return json(agent("active", "ready"));
      throw new Error(`Unexpected request: ${path}`);
    });
    vi.stubGlobal("fetch", fetchMock);
    renderRoute(`/s/sumi-lab/agents/${agentId}`);

    expect(await screen.findByRole("heading", { name: "Lin" })).toBeVisible();
    fireEvent.click(screen.getByRole("tab", { name: "Overview" }));
    expect(await screen.findByRole("heading", { name: "Agent DMs" })).toBeVisible();
    expect(screen.getByRole("link", { name: "Mira" })).toHaveAttribute("href", `/s/sumi-lab/agents/${peerId}`);
    expect(screen.getByText("Private Agent DM")).toBeVisible();
    expect(document.querySelectorAll(".dm-nav-item")).toHaveLength(0);
    expect(fetchMock).toHaveBeenCalledWith(`/api/v1/members/${agentId}/dms`, expect.anything());
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
    access_level: "member",
    role_text: "Review boundaries.",
    role_revision: 1,
    desired_lifecycle: desiredLifecycle,
    provision_status: provisionStatus,
    activity_status: provisionStatus === "error" ? "error" : desiredLifecycle === "active" ? "working" : "suspended",
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
