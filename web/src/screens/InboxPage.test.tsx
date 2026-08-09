import { readFileSync } from "node:fs";
import { join } from "node:path";

import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { RouterProvider, createMemoryHistory } from "@tanstack/react-router";
import { fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";

import { createAppRouter } from "../router";

const ownerId = "019c0000-0000-7000-8000-000000000002";
const space = {
  id: "019c0000-0000-7000-8000-000000000001",
  name: "Sumi Lab",
  slug: "sumi-lab",
  accent: "#F0602F",
  owner_member_id: ownerId,
  current_member_id: ownerId,
  general_channel_id: "019c0000-0000-7000-8000-000000000003",
};

afterEach(() => vi.unstubAllGlobals());

describe("Human Inbox", () => {
  it("explains an empty Inbox instead of looking like missing content", async () => {
    const fetchMock = vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      const path = String(input);
      if (path.includes("/spaces/by-slug/")) return json(space);
      if (path === "/api/v1/auth/me") {
        return json({ id: "user", display_name: "Ada", email: "ada@example.test" });
      }
      if (path.endsWith("/channels") && !init?.method) return json({ can_create: true, channels: [] });
      if (path.endsWith("/dms") && !init?.method) return json([]);
      if (path.endsWith("/members") && !init?.method) {
        return json([{ id: ownerId, kind: "human", display_name: "Ada", access_level: "owner", permissions: [] }]);
      }
      if (path.endsWith(`/members/${ownerId}/inbox`)) return json([]);
      throw new Error(`Unexpected request: ${path}`);
    });
    vi.stubGlobal("fetch", fetchMock);
    renderRoute("/s/sumi-lab/inbox");

    expect(await screen.findByRole("heading", { name: "Nothing needs your attention" })).toBeVisible();
    expect(screen.getByText(/not your Message history/i)).toBeVisible();
    const groups = screen.getByRole("list", { name: "Empty Inbox groups" });
    expect(groups).toHaveTextContent("Direct messages");
    expect(groups).toHaveTextContent("Threads");
    expect(screen.getAllByText("0")).toHaveLength(2);
    expect(screen.queryByRole("button", { name: "Mark all read" })).toBeNull();
  });

  it("groups attention in product priority order and identifies each sender", async () => {
    const fetchMock = vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      const path = String(input);
      if (path.includes("/spaces/by-slug/")) return json(space);
      if (path === "/api/v1/auth/me") return json({ id: "user", display_name: "Ada", email: "ada@example.test" });
      if (path.endsWith("/channels") && !init?.method) return json({ can_create: true, channels: [] });
      if (path.endsWith("/dms") && !init?.method) return json([]);
      if (path.endsWith("/members") && !init?.method) {
        return json([
          { id: ownerId, kind: "human", display_name: "Ada", access_level: "owner", permissions: [] },
          { id: "grace", kind: "human", display_name: "Grace", access_level: "member", permissions: [] },
          { id: "lin", kind: "agent", display_name: "Lin", access_level: "member", permissions: [] },
        ]);
      }
      if (path.endsWith(`/members/${ownerId}/inbox`)) {
        return json([
          inboxItem("ambient", "channel_activity", "lin", "Lin", "Ambient update"),
          inboxItem("dm", "direct", "grace", "Grace", "A DM", "A DM", "dm-channel"),
          inboxItem("reply", "reply", "grace", "Grace", "A reply", "A reply", "thread-1"),
          inboxItem("mention", "mention", "lin", "Lin", "Please review", "Please review the release checklist before continuing", "thread-1", "2026-07-25T00:00:01Z"),
        ]);
      }
      throw new Error(`Unexpected request: ${path}`);
    });
    vi.stubGlobal("fetch", fetchMock);
    renderRoute("/s/sumi-lab/inbox");

    expect(await screen.findByText("Please review the release checklist before continuing")).toBeVisible();
    const workspace = screen.getAllByRole("heading", { name: "Inbox", level: 1 }).at(-1)!.closest(".inbox-workspace")!;
    const view = within(workspace as HTMLElement);
    const headings = view.getAllByRole("heading", { level: 2 }).map((heading) => heading.textContent);
    expect(headings).toEqual(["Direct messages", "Threads"]);
    const linIdenticons = view.getAllByRole("img", { name: "Lin avatar" });
    expect(linIdenticons).toHaveLength(1);
    expect(view.getAllByRole("img", { name: "Grace avatar" })).toHaveLength(1);
    expect(view.getByRole("button", { name: "Open DM from Grace; 1 new message" })).toBeVisible();
    expect(view.getByRole("button", { name: "Open #general from Lin; 2 new messages" })).toBeVisible();
    expect(view.getByText("Please review the release checklist before continuing")).toHaveClass("inbox-message-preview");
    const styles = readFileSync(join(process.cwd(), "src/styles.css"), "utf8");
    expect(styles).toMatch(/\.inbox-message-preview\s*\{[^}]*overflow:\s*hidden/s);
    expect(styles).toMatch(/\.inbox-message-preview\s*\{[^}]*-webkit-line-clamp:\s*1/s);
  });

  it("marks an Item read when its source is opened", async () => {
    const reads: string[] = [];
    const fetchMock = vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      const path = String(input);
      if (path.includes("/spaces/by-slug/")) return json(space);
      if (path === "/api/v1/auth/me") return json({ id: "user", display_name: "Ada", email: "ada@example.test" });
      if (path.endsWith("/channels") && !init?.method) return json({ can_create: true, channels: [] });
      if (path.endsWith("/dms") && !init?.method) return json([]);
      if (path.endsWith("/members") && !init?.method) {
        return json([{ id: ownerId, kind: "human", display_name: "Ada", access_level: "owner", permissions: [] }]);
      }
      if (path.endsWith(`/members/${ownerId}/inbox`)) {
        return json([inboxItem("reply", "reply", "grace", "Grace", "A reply")]);
      }
      if (path.endsWith("/inbox-items/reply/read") && init?.method === "POST") {
        reads.push(path);
        return json({ ...inboxItem("reply", "reply", "grace", "Grace", "A reply"), status: "handled" });
      }
      throw new Error(`Unexpected request: ${path}`);
    });
    vi.stubGlobal("fetch", fetchMock);
    renderRoute("/s/sumi-lab/inbox");

    fireEvent.click(await screen.findByRole("button", { name: "Open #general from Grace; 1 new message" }));
    await waitFor(() => expect(reads).toEqual(["/api/v1/inbox-items/reply/read"]));
  });

  it("marks every pending Item read with one request", async () => {
    const reads: string[] = [];
    const fetchMock = vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      const path = String(input);
      if (path.includes("/spaces/by-slug/")) return json(space);
      if (path === "/api/v1/auth/me") return json({ id: "user", display_name: "Ada", email: "ada@example.test" });
      if (path.endsWith("/channels") && !init?.method) return json({ can_create: true, channels: [] });
      if (path.endsWith("/dms") && !init?.method) return json([]);
      if (path.endsWith("/members") && !init?.method) {
        return json([{ id: ownerId, kind: "human", display_name: "Ada", access_level: "owner", permissions: [] }]);
      }
      if (path.endsWith(`/members/${ownerId}/inbox/read`) && init?.method === "POST") {
        reads.push(path);
        return json({ count: 2 });
      }
      if (path.endsWith(`/members/${ownerId}/inbox`)) {
        return json([
          inboxItem("one", "direct", "grace", "Grace", "A DM", "A DM", "dm-channel"),
          inboxItem("two", "reply", "lin", "Lin", "A reply"),
        ]);
      }
      throw new Error(`Unexpected request: ${path}`);
    });
    vi.stubGlobal("fetch", fetchMock);
    renderRoute("/s/sumi-lab/inbox");

    const markAll = await screen.findByRole("button", { name: "Mark all read" });
    fireEvent.click(markAll);
    await waitFor(() =>
      expect(reads).toEqual([`/api/v1/members/${ownerId}/inbox/read`]),
    );
  });
});

function inboxItem(
  id: string,
  kind: string,
  senderId: string,
  senderName: string,
  summary: string,
  messagePreview = summary,
  threadId = id,
  createdAt = "2026-07-25T00:00:00Z",
) {
  return {
    id,
    member_id: ownerId,
    space_id: space.id,
    kind,
    priority: kind === "channel_activity" ? "ambient" : "hard",
    channel_id: space.general_channel_id,
    channel_slug: "general",
    message_id: `${id}-message`,
    sender_member_id: senderId,
    sender_display_name: senderName,
    message_preview: messagePreview,
    summary,
    status: "pending",
    available_at: "2026-07-25T00:00:00Z",
    created_at: createdAt,
    thread_id: threadId,
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
