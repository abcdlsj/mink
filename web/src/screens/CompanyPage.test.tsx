import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { RouterProvider, createMemoryHistory } from "@tanstack/react-router";
import { cleanup, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";

import { createAppRouter } from "../router";

const ownerId = "019c0000-0000-7000-8000-000000000002";
const spaceId = "019c0000-0000-7000-8000-000000000001";

afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
});

describe("Company hub", () => {
  it("shows HQ, the shared Drive, and the Office", async () => {
    vi.stubGlobal("fetch", vi.fn(async (input: RequestInfo | URL) => {
      const path = String(input);
      const shell = shellResponse(path);
      if (shell) return shell;
      if (path.endsWith("/agents")) return json([agent("idle")]);
      if (path.endsWith("/company/files")) {
        return json([
          {
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
          },
        ]);
      }
      throw new Error(`Unexpected request: ${path}`);
    }));

    renderRoute("/s/sumi-lab/company");

    expect(await screen.findByRole("heading", { name: "Company", level: 1 })).toBeVisible();
    expect(screen.getByRole("heading", { name: "HQ Channel" })).toBeVisible();
    expect(screen.getByRole("link", { name: /Open #hq/ })).toHaveAttribute("href", "/s/sumi-lab/channels/hq");
    expect(screen.getByRole("heading", { name: "Company Drive" })).toBeVisible();
    const drive = within(screen.getByRole("heading", { name: "Company Drive" }).closest(".company-card")!);
    expect(drive.getByRole("link", { name: "brief.pdf" })).toHaveAttribute("download", "brief.pdf");
    expect(drive.getByText(/Ada/)).toBeVisible();
    expect(drive.getByRole("button", { name: "Delete brief.pdf" })).toBeVisible();
    expect(screen.getByRole("heading", { name: "Office" })).toBeVisible();
    expect(within(screen.getByRole("heading", { name: "Office" }).closest(".company-card")!).getByText("1")).toBeVisible();
  });

  it("shows an empty Drive state and lets a member upload", async () => {
    const fetchMock = vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      const path = String(input);
      const shell = shellResponse(path);
      if (shell) return shell;
      if (path.endsWith("/agents")) return json([]);
      if (path.endsWith("/company/files") && !init?.method) return json([]);
      if (path.endsWith("/company/files") && init?.method === "POST") {
        return new Response(JSON.stringify({ id: "file-2" }), {
          status: 201,
          headers: { "Content-Type": "application/json" },
        });
      }
      throw new Error(`Unexpected request: ${path} ${init?.method ?? ""}`);
    });
    vi.stubGlobal("fetch", fetchMock);

    renderRoute("/s/sumi-lab/company");

    const drive = within((await screen.findByRole("heading", { name: "Company Drive" })).closest(".company-card")!);
    expect(drive.getByText(/No shared files yet/)).toBeVisible();
    const file = new File(["hello"], "hello.txt", { type: "text/plain" });
    const input = drive.getByLabelText("Share a file") as HTMLInputElement;
    Object.defineProperty(input, "files", { value: [file], configurable: true });
    fireEvent.change(input);
    expect(input.files?.[0]).toBe(file);
    const uploadButton = drive.getByRole("button", { name: /Upload/ });
    expect(uploadButton).toBeEnabled();
    fireEvent.submit(input.closest("form")!);
    await waitFor(() => expect(fetchMock).toHaveBeenCalledWith(
      expect.stringContaining("/company/files?name=hello.txt"),
      expect.objectContaining({ method: "POST" }),
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
