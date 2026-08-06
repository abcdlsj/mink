import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { RouterProvider, createMemoryHistory } from "@tanstack/react-router";
import { cleanup, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";

import { createAppRouter } from "../router";
import type { Run, Task, ThreadReference } from "../api/client";

const ownerId = "019c0000-0000-7000-8000-000000000002";
const spaceId = "019c0000-0000-7000-8000-000000000001";

afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
});

describe("Task work index", () => {
  it("orders open statuses, filters history, and links to the Source Thread", async () => {
    vi.stubGlobal("fetch", vi.fn(async (input: RequestInfo | URL) => {
      const path = String(input);
      const shell = shellResponse(path);
      if (shell) return shell;
      if (path.endsWith("/agents")) return json([]);
      if (path.endsWith("/tasks")) return json([
        task("todo", "Design claim flow", 17),
        task("in_progress", "Wire Agent CLI", 18, "Lin"),
        task("in_review", "Review Web contract", 19, "Reviewer"),
        task("done", "Ship old slice", 20, "Lin"),
      ]);
      throw new Error(`Unexpected request: ${path}`);
    }));

    renderRoute("/s/sumi-lab/tasks");

    expect(await screen.findByRole("heading", { name: "Tasks", level: 1 })).toBeVisible();
    const tasksView = within(screen.getByRole("heading", { name: "Tasks", level: 1 }).closest(".tasks-workspace")!);
    expect(await tasksView.findByText(/Review Web contract/)).toBeVisible();
    expect(tasksView.getByRole("link", { name: /Review Web contract/ })).toBeVisible();
    expect(tasksView.getByRole("link", { name: /Wire Agent CLI/ })).toBeVisible();
    expect(tasksView.getByRole("link", { name: /Design claim flow/ })).toBeVisible();
    expect(tasksView.queryByText(/Ship old slice/)).not.toBeInTheDocument();
    expect(tasksView.getByRole("link", { name: "Source: #general:17" })).toHaveAttribute("href", "/s/sumi-lab/channels/general#message-message-todo");
    fireEvent.click(tasksView.getByRole("button", { name: "Done" }));
    expect(await tasksView.findByText(/Ship old slice/)).toBeVisible();
    expect(tasksView.queryByText(/Design claim flow/)).not.toBeInTheDocument();
    fireEvent.click(tasksView.getByRole("button", { name: "Closed" }));
    expect(await tasksView.findByText("No closed Tasks.")).toBeVisible();
  });

  it("shows Source, Related Threads, Run, Result continuity and reset action", async () => {
    const value = task("in_progress", "Rebuild Task detail", 21, "Lin");
    value.related_threads = [{ ...thread("related", 31), channel_slug: "design" }];
    value.current_run = run("working", value.source_thread);
    value.recent_runs = [run("yielded", value.source_thread)];
    value.session_continuity = { state: "warm", generation: 2 };
    const fetchMock = vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      const path = String(input);
      const shell = shellResponse(path);
      if (shell) return shell;
      if (path.endsWith("/agents")) return json([{ member_id: "agent", name: "Lin", desired_lifecycle: "active" }]);
      if (path.endsWith(`/tasks/${value.id}`) && !init?.method) return json(value);
      if (path.endsWith(`/tasks/${value.id}/reset-session`) && init?.method === "POST") return json({ ...value, session_continuity: { state: "cold", generation: 3 } });
      throw new Error(`Unexpected request: ${path}`);
    });
    vi.stubGlobal("fetch", fetchMock);

    renderRoute(`/s/sumi-lab/tasks/${value.id}`);

    expect(await screen.findByRole("heading", { name: value.title })).toBeVisible();
    expect(screen.getByRole("heading", { name: "Source Thread" })).toBeVisible();
    expect(screen.getByRole("link", { name: "source: #general:21" })).toBeVisible();
    expect(screen.getByText("working")).toBeVisible();
    expect(screen.getByText("yielded")).toBeVisible();
    expect(screen.getByText("Warm").closest("div")).toHaveTextContent("WarmGeneration 2");
    fireEvent.click(screen.getByRole("button", { name: /Reset continuity/i }));
    await waitFor(() => expect(fetchMock).toHaveBeenCalledWith(expect.stringContaining("/reset-session"), expect.objectContaining({ method: "POST" })));
    expect((await screen.findByText("Cold")).closest("div")).toHaveTextContent("ColdGeneration 3");
  });

  it("labels a DM Source Thread without a channel link", async () => {
    const value = task("in_progress", "DM Task", 21, "Lin");
    value.source_thread = { ...thread("source", 21), channel_slug: null };
    value.current_run = run("working", value.source_thread);
    vi.stubGlobal("fetch", vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      const path = String(input);
      const shell = shellResponse(path);
      if (shell) return shell;
      if (path.endsWith("/agents")) return json([{ member_id: "agent", name: "Lin", desired_lifecycle: "active" }]);
      if (path.endsWith(`/tasks/${value.id}`) && !init?.method) return json(value);
      throw new Error(`Unexpected request: ${path}`);
    }));

    renderRoute(`/s/sumi-lab/tasks/${value.id}`);

    expect(await screen.findByRole("heading", { name: "DM Task" })).toBeVisible();
    expect(screen.getByText("!21 · DM · message 21")).toBeVisible();
    expect(screen.getByLabelText("source: DM · message 21")).toBeVisible();
    expect(screen.getByLabelText("Focus: DM · message 21")).toBeVisible();
    expect(screen.queryAllByRole("link", { name: /#.*@21/ })).toHaveLength(0);
  });
});

function task(status: "todo" | "in_progress" | "in_review" | "done" | "closed", title: string, seq: number, assignee?: string): Task {
  return {
    id: `task-${status}`,
    seq,
    space_id: spaceId,
    title,
    status,
    creator_member_id: ownerId,
    creator_name: "Ada",
    assignee_agent_member_id: assignee ? "agent" : undefined,
    assignee_name: assignee,
    source_thread: thread("source", seq),
    related_threads: [],
    result_message: undefined,
    close_reason_code: undefined,
    close_reason_note: undefined,
    current_run: undefined,
    recent_runs: [],
    session_continuity: { state: "cold" },
    runtime_issue_code: undefined,
    created_at: "2026-07-28T00:00:00Z",
    updated_at: `2026-07-28T0${seq % 9}:00:00Z`,
    finished_at: status === "done" || status === "closed" ? "2026-07-28T10:00:00Z" : undefined,
  };
}

function thread(relation: "source" | "related", seq: number): ThreadReference {
  return { id: `thread-${seq}`, channel_id: "channel", channel_slug: "general", root_message_id: seq === 17 ? "message-todo" : `message-${seq}`, root_message_seq: seq, relation };
}

function run(status: "working" | "yielded", focus: ThreadReference): Run {
  return { id: `run-${status}`, task_id: "task", agent_member_id: "agent", agent_name: "Lin", focus, status, outcome: status === "yielded" ? "yielded" : undefined, started_at: "2026-07-28T01:00:00Z" };
}

function shellResponse(path: string): Response | undefined {
  if (path.includes("/spaces/by-slug/")) return json({ id: spaceId, name: "Sumi Lab", slug: "sumi-lab", accent: "#F0602F", owner_member_id: ownerId, current_member_id: ownerId, general_channel_id: "channel" });
  if (path === "/api/v1/auth/me") return json({ id: "user", display_name: "Ada", email: "ada@example.test" });
  if (path.endsWith("/channels")) return json({ can_create: true, channels: [] });
  if (path.endsWith("/dms")) return json([]);
  if (path.endsWith("/members")) return json([{ id: ownerId, kind: "human", display_name: "Ada", access_level: "owner", permissions: [] }]);
  return undefined;
}

function renderRoute(path: string) {
  const router = createAppRouter(createMemoryHistory({ initialEntries: [path] }));
  const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } });
  render(<QueryClientProvider client={queryClient}><RouterProvider router={router} /></QueryClientProvider>);
}

function json(body: unknown): Response {
  return new Response(JSON.stringify(body), { status: 200, headers: { "Content-Type": "application/json" } });
}
