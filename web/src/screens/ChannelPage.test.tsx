import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { RouterProvider, createMemoryHistory } from "@tanstack/react-router";
import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";

import { createAppRouter } from "../router";

afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
});

describe("ChannelPage", () => {
  it("opens an initialized general Channel with no Messages", async () => {
    const channelId = "019c0000-0000-7000-8000-000000000003";
    const spaceId = "019c0000-0000-7000-8000-000000000001";
    const ownerId = "019c0000-0000-7000-8000-000000000002";
    const fetchMock = vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      const path = String(input);
      if (path.includes("/spaces/by-slug/")) {
        return json({ id: spaceId, name: "Sumi Lab", slug: "sumi-lab", accent: "#FFD447", owner_member_id: ownerId, current_member_id: ownerId, general_channel_id: channelId });
      }
      if (path === "/api/v1/auth/me") return json({ id: "user", display_name: "Ada", email: "ada@example.test" });
      if (path.endsWith("/channels") && !init?.method) {
        return json({ can_create: true, channels: [{ id: channelId, space_id: spaceId, kind: "public", name: "general", slug: "general", created_by_member_id: ownerId, joined: true }] });
      }
      if (path.endsWith("/dms") && !init?.method) return json([]);
      if (path.endsWith("/members") && !init?.method) {
        return json([{ id: ownerId, kind: "human", display_name: "Ada", handle: "ada", access_level: "owner", permissions: [] }]);
      }
      if (path.endsWith(`/channels/${channelId}/messages`) && !init?.method) {
        return json({ channel_id: channelId, snapshot_channel_seq: 0, messages: [], has_more_before: false, has_more_after: false });
      }
      throw new Error(`Unexpected request: ${path}`);
    });
    vi.stubGlobal("fetch", fetchMock);
    renderRoute("/s/sumi-lab/channels/general");

    expect(await screen.findByRole("heading", { name: "#general starts here." })).toBeVisible();
    expect(screen.getByLabelText("Message")).toHaveAttribute("placeholder", "Message #general");
  });

  it("renders API Messages and sends structured mentions", async () => {
    const channelId = "019c0000-0000-7000-8000-000000000003";
    const graceId = "019c0000-0000-7000-8000-000000000020";
    const fetchMock = vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      const path = String(input);
      if (path.includes("/spaces/by-slug/")) {
        return json({
          id: "019c0000-0000-7000-8000-000000000001",
          name: "Sumi Lab",
          slug: "sumi-lab",
          accent: "#FFD447",
          owner_member_id: "019c0000-0000-7000-8000-000000000002",
          current_member_id: "019c0000-0000-7000-8000-000000000002",
          general_channel_id: channelId,
        });
      }
      if (path === "/api/v1/auth/me") {
        return json({
          id: "019c0000-0000-7000-8000-000000000010",
          display_name: "Ada Lovelace",
          email: "ada@example.test",
        });
      }
      if (path.endsWith("/channels") && !init?.method) {
        return json({
          can_create: true,
          channels: [
            {
              id: channelId,
              space_id: "019c0000-0000-7000-8000-000000000001",
              kind: "public",
              name: "general",
              slug: "general",
              topic: "Shared work",
              created_by_member_id: "019c0000-0000-7000-8000-000000000002",
              joined: true,
            },
          ],
        });
      }
      if (path.endsWith("/dms") && !init?.method) return json([]);
      if (path.endsWith("/members")) {
        return json([
          {
            id: "019c0000-0000-7000-8000-000000000002",
            kind: "human",
            display_name: "Ada Lovelace",
            handle: "ada-lovelace",
            access_level: "owner",
            permissions: [],
          },
          {
            id: graceId,
            kind: "human",
            display_name: "Grace Hopper",
            handle: "grace-hopper",
            access_level: "member",
            permissions: [],
          },
        ]);
      }
      if (path.endsWith(`/channels/${channelId}/messages`) && !init?.method) {
        return json({
          channel_id: channelId,
          snapshot_channel_seq: 1,
          has_more_before: false,
          has_more_after: false,
          messages: [{ ...message(channelId, 1, "First Message"), thread_id: 1, reply_count: 1 }],
        });
      }
      if (path.endsWith(`/channels/${channelId}/threads/1`) && !init?.method) {
        return json({
          channel_id: channelId,
          thread_id: 1,
          snapshot_channel_seq: 2,
          root: { ...message(channelId, 1, "First Message"), thread_id: 1, reply_count: 1 },
          replies: [message(channelId, 2, "Existing reply")],
          is_following: false,
        });
      }
      if (path.endsWith(`/channels/${channelId}/threads/1/subscription`) && init?.method === "PUT") {
        return json({ channel_id: channelId, thread_id: 1, is_following: true });
      }
      if (path.endsWith(`/channels/${channelId}/threads/1/messages`) && init?.method === "POST") {
        return json(message(channelId, 3, "New Thread reply"), 201);
      }
      if (path.endsWith(`/channels/${channelId}/messages`) && init?.method === "POST") {
        return json(message(channelId, 2, "@grace-hopper Please review"), 201);
      }
      if (path === "/api/v1/attachments/uploads" && init?.method === "POST") {
        return json({
          id: "019c0000-0000-7000-8000-000000000040",
          space_id: "019c0000-0000-7000-8000-000000000001",
          uploader_member_id: "019c0000-0000-7000-8000-000000000002",
          original_name: "notes.txt",
          media_type: "text/plain",
          status: "uploading",
          upload_path: "/api/v1/attachments/019c0000-0000-7000-8000-000000000040/content",
          created_at: "2026-07-25T00:00:00Z",
        }, 201);
      }
      if (path.endsWith("/attachments/019c0000-0000-7000-8000-000000000040/content") && init?.method === "PUT") {
        return new Response(null, { status: 204 });
      }
      if (path.endsWith("/attachments/019c0000-0000-7000-8000-000000000040/complete") && init?.method === "POST") {
        return json({
          id: "019c0000-0000-7000-8000-000000000040",
          space_id: "019c0000-0000-7000-8000-000000000001",
          uploader_member_id: "019c0000-0000-7000-8000-000000000002",
          original_name: "notes.txt",
          media_type: "text/plain",
          size: 11,
          sha256: "digest",
          status: "ready",
          download_path: "/api/v1/attachments/019c0000-0000-7000-8000-000000000040/download",
          created_at: "2026-07-25T00:00:00Z",
        });
      }
      throw new Error(`Unexpected request: ${path}`);
    });
    vi.stubGlobal("fetch", fetchMock);
    renderRoute("/s/sumi-lab/channels/general");

    expect(await screen.findByText("First Message")).toBeVisible();
    const attachmentFile = new File(["pixel notes"], "notes.txt", { type: "text/plain" });
    Object.defineProperty(attachmentFile, "arrayBuffer", {
      value: async () => new TextEncoder().encode("pixel notes").buffer,
    });
    fireEvent.change(screen.getByLabelText("Choose Attachment"), {
      target: { files: [attachmentFile] },
    });
    expect(await screen.findByText("notes.txt")).toBeVisible();
    const input = screen.getByLabelText("Message");
    fireEvent.change(input, { target: { value: "@grace-hopper Please review" } });
    fireEvent.submit(input.closest("form")!);

    expect(await screen.findByText("@grace-hopper Please review")).toBeVisible();
    expect(input).toHaveValue("");
    await waitFor(() => {
      const call = fetchMock.mock.calls.find(
        ([path, init]) => String(path).endsWith("/messages") && init?.method === "POST",
      );
      expect(JSON.parse(String(call?.[1]?.body))).toEqual({
        body_markdown: "@grace-hopper Please review",
        mentions: [graceId],
        attachment_ids: ["019c0000-0000-7000-8000-000000000040"],
      });
    });

    fireEvent.click(screen.getByRole("button", { name: "1 reply" }));
    expect(await screen.findByText("Existing reply")).toBeVisible();
    fireEvent.click(screen.getByRole("button", { name: "Follow Thread" }));
    expect(await screen.findByRole("button", { name: "Unfollow Thread" })).toBeVisible();
    const threadInput = screen.getByLabelText("Thread reply");
    fireEvent.change(threadInput, { target: { value: "New Thread reply" } });
    fireEvent.submit(threadInput.closest("form")!);
    expect(await screen.findByText("New Thread reply")).toBeVisible();
    expect(threadInput).toHaveValue("");
  });

  it("archives a managed Channel and returns to general", async () => {
    const designId = "019c0000-0000-7000-8000-000000000030";
    const fetchMock = vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      const path = String(input);
      if (path.includes("/spaces/by-slug/")) {
        return json({
          id: "019c0000-0000-7000-8000-000000000001",
          name: "Sumi Lab",
          slug: "sumi-lab",
          accent: "#FFD447",
          owner_member_id: "019c0000-0000-7000-8000-000000000002",
          current_member_id: "019c0000-0000-7000-8000-000000000002",
          general_channel_id: "019c0000-0000-7000-8000-000000000003",
        });
      }
      if (path === "/api/v1/auth/me") {
        return json({ id: "user", display_name: "Ada Lovelace", email: "ada@example.test" });
      }
      if (path.endsWith("/members")) {
        return json([{ id: "019c0000-0000-7000-8000-000000000002", kind: "human", display_name: "Ada Lovelace", handle: "ada", access_level: "owner", permissions: [] }]);
      }
      if (path.endsWith("/dms") && !init?.method) return json([]);
      if (path.endsWith("/channels") && !init?.method) {
        return json({ can_create: true, channels: [{ id: designId, space_id: "019c0000-0000-7000-8000-000000000001", kind: "public", name: "Design", slug: "design", created_by_member_id: "019c0000-0000-7000-8000-000000000002", joined: true }] });
      }
      if (path.endsWith(`/channels/${designId}/messages`) && !init?.method) {
        return json({ channel_id: designId, snapshot_channel_seq: 0, messages: [], has_more_before: false, has_more_after: false });
      }
      if (path.endsWith(`/channels/${designId}/archive`) && init?.method === "POST") {
        return json({ id: designId, space_id: "019c0000-0000-7000-8000-000000000001", kind: "public", name: "Design", slug: "design", created_by_member_id: "019c0000-0000-7000-8000-000000000002", joined: true, archived_at: "2026-07-25T00:00:00Z" });
      }
      throw new Error(`Unexpected request: ${path}`);
    });
    vi.stubGlobal("fetch", fetchMock);
    renderRoute("/s/sumi-lab/channels/design");

    const archive = await screen.findByRole("button", { name: "Archive Channel" });
    fireEvent.click(archive);
    await waitFor(() => {
      expect(fetchMock).toHaveBeenCalledWith(
        expect.stringContaining(`/channels/${designId}/archive`),
        expect.objectContaining({ method: "POST" }),
      );
    });
  });
});

function renderRoute(path: string) {
  const router = createAppRouter(createMemoryHistory({ initialEntries: [path] }));
  const queryClient = new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  });
  render(
    <QueryClientProvider client={queryClient}>
      <RouterProvider router={router} />
    </QueryClientProvider>,
  );
}

function message(channelId: string, seq: number, body: string) {
  return {
    id: `019c0000-0000-7000-8000-00000000010${seq}`,
    channel_id: channelId,
    seq,
    author: {
      id: "019c0000-0000-7000-8000-000000000002",
      kind: "human",
      display_name: "Ada Lovelace",
      handle: "ada-lovelace",
    },
    body_markdown: body,
    mentions: [],
    attachments: [],
    created_at: "2026-07-25T00:00:00Z",
    reply_count: 0,
  };
}

function json(body: unknown, status = 200): Response {
  return new Response(JSON.stringify(body), {
    status,
    headers: { "Content-Type": "application/json" },
  });
}
