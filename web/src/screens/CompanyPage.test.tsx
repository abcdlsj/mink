import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { RouterProvider, createMemoryHistory } from "@tanstack/react-router";
import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";

import { createAppRouter } from "../router";

const ownerId = "019c0000-0000-7000-8000-000000000002";
const spaceId = "019c0000-0000-7000-8000-000000000001";

afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
});

describe("Company hub", () => {
  it("opens the Office by default with Company navigation and paired Agents", async () => {
    const fetchMock = vi.fn(async (input: RequestInfo | URL) => {
      const path = String(input);
      const shell = shellResponse(path);
      if (shell) return shell;
      if (path.endsWith("/agents")) return json([agent("idle")]);
      throw new Error(`Unexpected request: ${path}`);
    });
    vi.stubGlobal("fetch", fetchMock);

    renderRoute("/s/sumi-lab/company");

    expect(await screen.findByRole("heading", { name: "Office", level: 1 })).toBeVisible();
    expect(screen.getByRole("link", { name: "Drive" })).toHaveAttribute("href", "/s/sumi-lab/company/drive");
    expect(screen.getByRole("link", { name: "HQ" })).toHaveAttribute("href", "/s/sumi-lab/company/hq");
    expect(screen.getByRole("link", { name: "Office" })).toHaveAttribute("href", "/s/sumi-lab/company/office");
    const agentLink = await screen.findByRole("link", { name: /Lin/ });
    expect(agentLink).toHaveAttribute("href", "/s/sumi-lab/agents/agent-1");
    expect(await screen.findByText(/Idle/)).toBeVisible();
  });

  it("renders the HQ conversation inside the Company context", async () => {
    vi.stubGlobal("fetch", vi.fn(async (input: RequestInfo | URL) => {
      const path = String(input);
      const shell = shellResponse(path);
      if (shell) return shell;
      if (path.endsWith("/agents")) return json([agent("idle")]);
      if (path.endsWith("/members")) return json([member(ownerId, "Ada")]);
      if (path.endsWith("/channels/hq/members")) {
        return json({ can_manage: true, members: [member(ownerId, "Ada")] });
      }
      if (path.endsWith("/messages")) {
        return json({
          channel_id: "hq",
          snapshot_channel_seq: 0,
          messages: [],
          has_more_before: false,
          has_more_after: false,
        });
      }
      throw new Error(`Unexpected request: ${path}`);
    }));

    renderRoute("/s/sumi-lab/company/hq");

    expect(await screen.findByRole("heading", { name: "#hq starts here." })).toBeVisible();
    expect(screen.getByRole("heading", { name: "#hq" })).toBeVisible();
    expect(screen.getByPlaceholderText("Message #hq")).toBeVisible();
    expect(screen.getByRole("link", { name: "Drive" })).toHaveAttribute("href", "/s/sumi-lab/company/drive");
  });

  it("shows the Drive as a cloud list with aligned columns and upload/delete", async () => {
    const fetchMock = vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      const path = String(input);
      const shell = shellResponse(path);
      if (shell) return shell;
      if (path.endsWith("/agents")) return json([]);
      if (path.endsWith("/company/files") && !init?.method) {
        return json([{
          id: "file-1",
          space_id: spaceId,
          name: "brief.pdf",
          media_type: "application/pdf",
          size: 2048,
          sha256: "a".repeat(64),
          uploader_member_id: ownerId,
          uploader_name: "Ada",
          download_path: "/api/v1/spaces/s/company/files/file-1",
          created_at: "2026-07-28T00:00:00Z",
        }]);
      }
      if (path.endsWith("/company/files") && init?.method === "POST") {
        return new Response(JSON.stringify({ id: "file-2" }), {
          status: 201,
          headers: { "Content-Type": "application/json" },
        });
      }
      if (path.includes("/company/files/file-1") && init?.method === "DELETE") {
        return new Response(null, { status: 204 });
      }
      throw new Error(`Unexpected request: ${path} ${init?.method ?? ""}`);
    });
    vi.stubGlobal("fetch", fetchMock);

    renderRoute("/s/sumi-lab/company/drive");

    expect(await screen.findByRole("heading", { name: "Company Drive", level: 1 })).toBeVisible();
    const download = await screen.findByRole("link", { name: "brief.pdf" });
    expect(screen.getByRole("columnheader", { name: "Name" })).toBeVisible();
    expect(screen.getByRole("columnheader", { name: "Size" })).toBeVisible();
    expect(screen.getByRole("columnheader", { name: "Uploaded by" })).toBeVisible();
    expect(download).toHaveAttribute("download", "brief.pdf");
    expect(await screen.findByText(/Ada/)).toBeVisible();
    expect(screen.getByRole("button", { name: "Delete brief.pdf" })).toBeVisible();

    const file = new File(["hello"], "hello.txt", { type: "text/plain" });
    const input = screen.getByLabelText("Upload files to Company Drive") as HTMLInputElement;
    Object.defineProperty(input, "files", { value: [file], configurable: true });
    fireEvent.change(input);
    await waitFor(() => expect(fetchMock).toHaveBeenCalledWith(
      expect.stringContaining("/company/files?name=hello.txt"),
      expect.objectContaining({ method: "POST" }),
    ));

    fireEvent.click(screen.getByRole("button", { name: "Delete brief.pdf" }));
    await waitFor(() => expect(fetchMock).toHaveBeenCalledWith(
      expect.stringContaining("/company/files/file-1"),
      expect.objectContaining({ method: "DELETE" }),
    ));
  });
});

function agent(status: "idle" | "working"): Record<string, unknown> {
  return {
    member_id: "agent-1",
    space_id: spaceId,
    computer_id: "computer",
    name: "Lin",
    access_level: "member",
    role_text: "Implement",
    role_revision: 1,
    desired_lifecycle: "active",
    provision_status: "ready",
    activity_status: status,
    computer_reachable: true,
    driver_kind: "builtin",
    attention_config: { dm_immediate: true, mention_immediate: true, ambient_enabled: true, ambient_debounce_seconds: 30, ambient_max_wait_seconds: 300, max_retry_count: 3 },
    activity: status === "working" ? { kind: "run", label: "Working", status } : undefined,
    last_error_code: undefined,
    memory_files: [],
    created_at: "2026-07-28T00:00:00Z",
    updated_at: "2026-07-28T00:00:00Z",
  };
}

function member(id: string, name: string): Record<string, unknown> {
  return {
    id,
    kind: "human",
    display_name: name,
    access_level: "owner",
    permissions: [],
  };
}

function shellResponse(path: string): Response | undefined {
  if (path.includes("/spaces/by-slug/")) return json({ id: spaceId, name: "Sumi Lab", slug: "sumi-lab", accent: "#F0602F", owner_member_id: ownerId, current_member_id: ownerId, general_channel_id: "channel" });
  if (path === "/api/v1/auth/me") return json({ id: "user", display_name: "Ada", email: "ada@example.test" });
  if (path.endsWith("/channels")) return json({ can_create: true, channels: [{ id: "hq", space_id: spaceId, slug: "hq", topic: "Company-wide", kind: "public", created_by_member_id: ownerId, joined: true, archived_at: null }] });
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
