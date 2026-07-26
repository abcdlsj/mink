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
const computerId = "019c0000-0000-7000-8000-000000000090";

afterEach(() => vi.unstubAllGlobals());

describe("Computer flows", () => {
  it("shows machine identity before confirming a pairing", async () => {
    const fetchMock = vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      const path = String(input);
      if (path.endsWith("/api/v1/spaces") && !init?.method) return json([space]);
      if (path.includes("/api/v1/computer-pairings/") && !path.includes("/confirm")) {
        return json({ pairing_id: "pairing", hostname: "studio.local", os: "macos", daemon_version: "0.1.0", public_key_fingerprint: "aa:bb:cc", expires_at: "2026-07-25T10:00:00Z", status: "pending" });
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

  it("lists status and lets a Human Owner revoke a Computer", async () => {
    const fetchMock = vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      const path = String(input);
      if (path.includes("/spaces/by-slug/")) return json(space);
      if (path === "/api/v1/auth/me") return json({ id: "user", display_name: "Ada", email: "ada@example.test" });
      if (path.endsWith("/channels") && !init?.method) return json({ can_create: true, channels: [] });
      if (path.endsWith("/dms") && !init?.method) return json([]);
      if (path.endsWith("/members") && !init?.method) return json([{ id: space.owner_member_id, kind: "human", display_name: "Ada", handle: "ada", access_level: "owner", permissions: [] }]);
      if (path.endsWith("/computers") && !init?.method) return json([{ id: computerId, space_id: space.id, name: "Studio", hostname: "studio.local", os: "macos", status: "online", daemon_version: "0.1.0", last_seen_at: "2026-07-25T00:00:00Z", created_at: "2026-07-25T00:00:00Z" }]);
      if (path.endsWith("/agents") && init?.method === "POST") return json({ member_id: "agent", space_id: space.id, computer_id: computerId, name: "Lin", handle: "lin", access_level: "member", role_text: "Review boundaries.", role_revision: 1, status: "provisioning", driver_kind: "codex", created_at: "2026-07-25T00:00:00Z" }, 201);
      if (path.endsWith(`/computers/${computerId}`) && init?.method === "DELETE") return json({ id: computerId, space_id: space.id, name: "Studio", hostname: "studio.local", os: "macos", status: "revoked", daemon_version: "0.1.0", last_seen_at: "2026-07-25T00:00:00Z", created_at: "2026-07-25T00:00:00Z" });
      throw new Error(`Unexpected request: ${path}`);
    });
    vi.stubGlobal("fetch", fetchMock);
    renderRoute("/s/sumi-lab/computers#create-agent");

    expect((await screen.findAllByText("Studio")).length).toBeGreaterThan(0);
    expect(screen.getByText("online")).toBeVisible();
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
    fireEvent.click(screen.getByRole("button", { name: "Revoke" }));
    await waitFor(() => expect(screen.getByText("revoked")).toBeVisible());
  });
});

function renderRoute(path: string) {
  const router = createAppRouter(createMemoryHistory({ initialEntries: [path] }));
  const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } });
  render(<QueryClientProvider client={queryClient}><RouterProvider router={router} /></QueryClientProvider>);
}

function json(body: unknown, status = 200): Response {
  return new Response(JSON.stringify(body), { status, headers: { "Content-Type": "application/json" } });
}
