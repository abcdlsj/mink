import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { RouterProvider, createMemoryHistory } from "@tanstack/react-router";
import { cleanup, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";

import { createAppRouter } from "../router";

const space = {
  id: "019c0000-0000-7000-8000-000000000001",
  name: "Sumi Lab",
  slug: "sumi-lab",
  accent: "#6B8F71",
  owner_member_id: "019c0000-0000-7000-8000-000000000002",
  current_member_id: "019c0000-0000-7000-8000-000000000002",
  general_channel_id: "019c0000-0000-7000-8000-000000000003",
};
const computerId = "019c0000-0000-7000-8000-000000000090";

afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
});

describe("Computer flows", () => {
  it("shows machine identity before confirming a pairing", async () => {
    const fetchMock = vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      const path = String(input);
      if (path.endsWith("/api/v1/spaces") && !init?.method) return json([space]);
      if (path.includes("/api/v1/computer-pairings/") && !path.includes("/confirm")) {
        return json({ pairing_id: "pairing", hostname: "studio.local", os: "macos", daemon_version: "0.1.0", token_fingerprint: "aa:bb:cc", expires_at: "2026-07-25T10:00:00Z", status: "pending" });
      }
      if (path.endsWith("/confirm") && init?.method === "POST") {
        return json({ id: computerId, space_id: space.id, name: "Studio", hostname: "studio.local", os: "macos", status: "offline", daemon_version: "0.1.0", created_at: "2026-07-25T00:00:00Z" }, 201);
      }
      throw new Error(`Unexpected request: ${path}`);
    });
    vi.stubGlobal("fetch", fetchMock);
    renderRoute("/pair-computer/pairing?code=secret-code");

    expect(await screen.findByRole("heading", { name: "studio.local" })).toBeVisible();
    expect(screen.getByText("aa:bb:cc")).toBeVisible();
    fireEvent.change(screen.getByLabelText("Space"), { target: { value: space.id } });
    fireEvent.change(screen.getByLabelText("Computer name"), { target: { value: "Studio" } });
    fireEvent.click(screen.getByRole("button", { name: "Pair Computer" }));
    await waitFor(() => {
      expect(fetchMock).toHaveBeenCalledWith(
        expect.stringContaining("/confirm"),
        expect.objectContaining({
          method: "POST",
          body: JSON.stringify({ space_id: space.id, name: "Studio", code: "secret-code" }),
        }),
      );
    });
  });

  it("creates an Agent in a dialog and lets a Human Owner delete a Computer", async () => {
    const fetchMock = vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      const path = String(input);
      if (path.includes("/spaces/by-slug/")) return json(space);
      if (path === "/api/v1/auth/me") return json({ id: "user", display_name: "Ada", email: "ada@example.test" });
      if (path.endsWith("/channels") && !init?.method) return json({ can_create: true, channels: [] });
      if (path.endsWith("/dms") && !init?.method) return json([]);
      if (path.endsWith("/members") && !init?.method) return json([{ id: space.owner_member_id, kind: "human", display_name: "Ada", handle: "ada", access_level: "owner", permissions: [] }]);
      if (path.endsWith("/computers") && !init?.method) return json([{ id: computerId, space_id: space.id, name: "Studio", hostname: "studio.local", os: "macos", status: "online", daemon_version: "0.1.0", last_seen_at: "2026-07-25T00:00:00Z", created_at: "2026-07-25T00:00:00Z" }]);
      if (path.endsWith("/agents") && !init?.method) return json([hostedAgent]);
      if (path.endsWith("/agents") && init?.method === "POST") return json({ member_id: "agent", space_id: space.id, computer_id: computerId, name: "Lin", handle: "lin", access_level: "member", role_text: "Review boundaries.", role_revision: 1, status: "provisioning", driver_kind: "codex", created_at: "2026-07-25T00:00:00Z" }, 201);
      if (path.endsWith(`/computers/${computerId}`) && init?.method === "DELETE") return json({ id: computerId, space_id: space.id, name: "Studio", hostname: "studio.local", os: "macos", status: "revoked", daemon_version: "0.1.0", last_seen_at: "2026-07-25T00:00:00Z", created_at: "2026-07-25T00:00:00Z" });
      throw new Error(`Unexpected request: ${path}`);
    });
    vi.stubGlobal("fetch", fetchMock);
    renderRoute("/s/sumi-lab/computers#create-agent");

    expect((await screen.findAllByText("Studio")).length).toBeGreaterThan(0);
    expect(screen.getAllByText("online")[0]).toBeVisible();
    expect(screen.getByRole("link", { name: "Pair Computer" })).toBeVisible();
    expect(screen.getAllByRole("heading", { name: "Computers" })).toHaveLength(1);
    fireEvent.change(screen.getByLabelText("Agent name"), { target: { value: "Lin" } });
    fireEvent.change(screen.getByLabelText("Driver"), { target: { value: "builtin" } });
    fireEvent.change(screen.getByLabelText("Role"), { target: { value: "Review boundaries." } });
    fireEvent.click(screen.getByRole("button", { name: "Create Agent" }));
    await waitFor(() => {
      expect(fetchMock).toHaveBeenCalledWith(
        expect.stringContaining(`/spaces/${space.id}/agents`),
        expect.objectContaining({
          method: "POST",
          body: expect.stringContaining('"driver_kind":"builtin"'),
        }),
      );
    });
    fireEvent.click(screen.getByRole("button", { name: "Delete" }));
    const deleteDialog = screen.getByRole("dialog", { name: "Delete Studio?" });
    expect(within(deleteDialog).getByText("Rin")).toBeVisible();
    expect(within(deleteDialog).getByText(/retired Agents are not restored/i)).toBeVisible();
    expect(within(deleteDialog).getByRole("button", { name: "Delete Studio" })).toBeDisabled();
    fireEvent.click(within(deleteDialog).getByRole("checkbox", { name: /will be retired/i }));
    fireEvent.click(screen.getByRole("button", { name: "Delete Studio" }));
    await waitFor(() => expect(fetchMock).toHaveBeenCalledWith(
      expect.stringContaining(`/computers/${computerId}`),
      expect.objectContaining({ method: "DELETE" }),
    ));
  });

  it("opens pairing instructions from the Computer list header", async () => {
    const fetchMock = vi.fn(async (input: RequestInfo | URL) => {
      const path = String(input);
      if (path.includes("/spaces/by-slug/")) return json(space);
      if (path === "/api/v1/auth/me") return json({ id: "user", display_name: "Ada", email: "ada@example.test" });
      if (path.endsWith("/channels")) return json({ can_create: true, channels: [] });
      if (path.endsWith("/dms")) return json([]);
      if (path.endsWith("/members")) return json([{ id: space.owner_member_id, kind: "human", display_name: "Ada", handle: "ada", access_level: "owner", permissions: [] }]);
      if (path.endsWith("/computers")) return json([{ id: computerId, space_id: space.id, name: "Studio", hostname: "studio.local", os: "macos", status: "online", daemon_version: "0.1.0", last_seen_at: "2026-07-25T00:00:00Z", created_at: "2026-07-25T00:00:00Z" }]);
      if (path.endsWith("/agents")) return json([]);
      throw new Error(`Unexpected request: ${path}`);
    });
    vi.stubGlobal("fetch", fetchMock);
    renderRoute("/s/sumi-lab/computers");

    fireEvent.click(await screen.findByRole("link", { name: "Pair Computer" }));
    const dialog = await screen.findByRole("dialog", { name: "Pair Computer" });
    expect(dialog).toBeVisible();
    expect(within(dialog).getByText(`sumi computer --server ${window.location.origin}`)).toBeVisible();
    expect(within(dialog).getByText("Verify the machine identity, then confirm this Space.")).toBeVisible();
    expect(within(dialog).getByRole("button", { name: "Close Pair Computer" })).toHaveFocus();
    fireEvent.keyDown(document, { key: "Tab", shiftKey: true });
    expect(within(dialog).getByRole("button", { name: "Done" })).toHaveFocus();
    fireEvent.keyDown(document, { key: "Escape" });
    await waitFor(() => expect(screen.queryByRole("dialog", { name: "Pair Computer" })).not.toBeInTheDocument());
  });

  it("does not expose Computer governance to a regular Human Member", async () => {
    const member = { id: "019c0000-0000-7000-8000-000000000099", kind: "human", display_name: "Bea", handle: "bea", access_level: "member", permissions: [] };
    const memberSpace = { ...space, current_member_id: member.id };
    const fetchMock = vi.fn(async (input: RequestInfo | URL) => {
      const path = String(input);
      if (path.includes("/spaces/by-slug/")) return json(memberSpace);
      if (path === "/api/v1/auth/me") return json({ id: "user-bea", display_name: "Bea", email: "bea@example.test" });
      if (path.endsWith("/channels")) return json({ can_create: false, channels: [] });
      if (path.endsWith("/dms")) return json([]);
      if (path.endsWith("/members")) return json([member]);
      if (path.endsWith("/computers")) return json([{ id: computerId, space_id: space.id, name: "Studio", hostname: "studio.local", os: "macos", status: "online", daemon_version: "0.1.0", last_seen_at: "2026-07-25T00:00:00Z", created_at: "2026-07-25T00:00:00Z" }]);
      if (path.endsWith("/agents")) return json([]);
      throw new Error(`Unexpected request: ${path}`);
    });
    vi.stubGlobal("fetch", fetchMock);
    renderRoute("/s/sumi-lab/computers#pair-computer");

    expect(await screen.findByRole("heading", { name: "Studio" })).toBeVisible();
    expect(screen.queryByRole("link", { name: "Pair Computer" })).not.toBeInTheDocument();
    expect(screen.queryByRole("dialog", { name: "Pair Computer" })).not.toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Delete" })).not.toBeInTheDocument();
  });
});

const hostedAgent = {
  member_id: "019c0000-0000-7000-8000-000000000091",
  space_id: space.id,
  computer_id: computerId,
  name: "Rin",
  handle: "rin",
  access_level: "member",
  role_text: "Review changes.",
  role_revision: 1,
  status: "active",
  activity_status: "busy",
  driver_kind: "builtin",
  attention_config: { dm_immediate: true, mention_immediate: true, ambient_enabled: true, ambient_debounce_seconds: 5, ambient_max_wait_seconds: 30, max_retry_count: 3 },
  memory_files: [],
  created_at: "2026-07-25T00:00:00Z",
  updated_at: "2026-07-25T00:00:00Z",
};

function renderRoute(path: string) {
  const router = createAppRouter(createMemoryHistory({ initialEntries: [path] }));
  const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } });
  render(<QueryClientProvider client={queryClient}><RouterProvider router={router} /></QueryClientProvider>);
}

function json(body: unknown, status = 200): Response {
  return new Response(JSON.stringify(body), { status, headers: { "Content-Type": "application/json" } });
}
