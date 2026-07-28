import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { RouterProvider, createMemoryHistory } from "@tanstack/react-router";
import { cleanup, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
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
    const linId = "019c0000-0000-7000-8000-000000000020";
    const fetchMock = vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      const path = String(input);
      if (path.includes("/spaces/by-slug/")) {
        return json({ id: spaceId, name: "Sumi Lab", slug: "sumi-lab", accent: "#FE7DA8", owner_member_id: ownerId, current_member_id: ownerId, general_channel_id: channelId });
      }
      if (path === "/api/v1/auth/me") return json({ id: "user", display_name: "Ada", email: "ada@example.test" });
      if (path.endsWith("/channels") && !init?.method) {
        return json({ can_create: true, channels: [{ id: channelId, space_id: spaceId, kind: "public", name: "general", slug: "general", created_by_member_id: ownerId, joined: true }] });
      }
      if (path.endsWith("/channels") && init?.method === "POST") {
        return json({ id: "019c0000-0000-7000-8000-000000000030", space_id: spaceId, kind: "private", name: "Design", slug: "design", topic: "Decisions", created_by_member_id: ownerId, joined: true }, 201);
      }
      if (path.endsWith("/dms") && !init?.method) {
        return json([{ channel_id: "dm", space_id: spaceId, other_member: { id: linId, kind: "agent", display_name: "Lin", handle: "lin", access_level: "member", permissions: [] }, created_at: "2026-07-25T00:00:00Z" }]);
      }
      if (path.endsWith("/agents") && !init?.method) {
        return json([{ member_id: linId, activity_status: "busy" }]);
      }
      if (path.endsWith("/computers") && !init?.method) return json([]);
      if (path.endsWith(`/channels/${channelId}/members`) && !init?.method) {
        return json({
          members: [
            { id: ownerId, kind: "human", display_name: "Ada", handle: "ada", access_level: "owner", permissions: [] },
            { id: linId, kind: "agent", display_name: "Lin", handle: "lin", access_level: "member", permissions: [] },
          ],
          can_manage: true,
        });
      }
      if (path.endsWith("/members") && !init?.method) {
        return json([
          { id: ownerId, kind: "human", display_name: "Ada", handle: "ada", access_level: "owner", permissions: [] },
          { id: linId, kind: "agent", display_name: "Lin", handle: "lin", access_level: "member", permissions: [] },
        ]);
      }
      if (path.endsWith(`/channels/${channelId}/messages`) && !init?.method) {
        return json({ channel_id: channelId, snapshot_channel_seq: 0, messages: [], has_more_before: false, has_more_after: false });
      }
      throw new Error(`Unexpected request: ${path}`);
    });
    vi.stubGlobal("fetch", fetchMock);
    renderRoute("/s/sumi-lab/channels/general");

    expect(await screen.findByRole("heading", { name: "#general starts here." })).toBeVisible();
    expect(screen.getByRole("link", { name: "Sumi home" })).toHaveTextContent("S");
    expect(screen.getByRole("region", { name: "Finish your Space setup" })).toBeVisible();
    expect(screen.getByRole("link", { name: "Pair" })).toHaveAttribute(
      "href",
      "/s/sumi-lab/computers#pair-computer",
    );
    expect(screen.getByLabelText("Message")).toHaveAttribute("placeholder", "Message #general");
    expect(screen.getAllByLabelText("Lin is Busy").length).toBeGreaterThanOrEqual(2);
    expect(screen.getByRole("link", { name: /Lin avatar.*Lin is Busy.*Lin.*@lin.*Busy/ })).toHaveAttribute("href", `/s/sumi-lab/dm/${linId}`);

    const shell = screen.getByRole("main");
    const navigation = screen.getByRole("complementary", { name: "Space navigation" });
    fireEvent.click(within(navigation).getByRole("button", { name: "Close navigation" }));
    expect(shell).toHaveClass("space-shell--navigation-collapsed");
    const railOpen = within(screen.getByRole("complementary", { name: "Space tools" })).getByRole("button", { name: "Open navigation" });
    fireEvent.click(railOpen);
    expect(shell).not.toHaveClass("space-shell--navigation-collapsed");
    expect(navigation).toHaveClass("space-navigation--open");

    fireEvent.click(screen.getByRole("button", { name: "Create Channel" }));
    const dialog = screen.getByRole("dialog", { name: "Create Channel" });
    fireEvent.change(within(dialog).getByLabelText("Name"), { target: { value: "Design" } });
    fireEvent.change(within(dialog).getByLabelText("Slug"), { target: { value: "design" } });
    fireEvent.change(within(dialog).getByLabelText("Visibility"), { target: { value: "private" } });
    fireEvent.change(within(dialog).getByLabelText("Topic"), { target: { value: "Decisions" } });
    fireEvent.click(within(dialog).getByRole("checkbox", { name: /Lin/ }));
    fireEvent.click(within(dialog).getByRole("button", { name: "Create Channel" }));
    await waitFor(() => {
      expect(fetchMock).toHaveBeenCalledWith(
        expect.stringContaining(`/spaces/${spaceId}/channels`),
        expect.objectContaining({
          method: "POST",
          body: JSON.stringify({
            name: "Design",
            slug: "design",
            kind: "private",
            topic: "Decisions",
            agent_member_ids: [linId],
          }),
        }),
      );
    });
  });

  it("renders API Messages and sends structured mentions", async () => {
    const channelId = "019c0000-0000-7000-8000-000000000003";
    const linId = "019c0000-0000-7000-8000-000000000020";
    const reviewerId = "019c0000-0000-7000-8000-000000000021";
    const fetchMock = vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      const path = String(input);
      if (path.includes("/spaces/by-slug/")) {
        return json({
          id: "019c0000-0000-7000-8000-000000000001",
          name: "Sumi Lab",
          slug: "sumi-lab",
          accent: "#FE7DA8",
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
      if (path.endsWith(`/channels/${channelId}/members`) && !init?.method) {
        return json({
          members: [
            {
              id: "019c0000-0000-7000-8000-000000000002",
              kind: "human",
              display_name: "Ada Lovelace",
              handle: "ada-lovelace",
              access_level: "owner",
              permissions: [],
            },
            {
              id: linId,
              kind: "agent",
              display_name: "Lin",
              handle: "lin",
              access_level: "member",
              permissions: [],
            },
          ],
          can_manage: true,
        });
      }
      if (path.endsWith(`/channels/${channelId}/members`) && init?.method === "POST") {
        return json({
          members: [
            { id: "019c0000-0000-7000-8000-000000000002", kind: "human", display_name: "Ada Lovelace", handle: "ada-lovelace", access_level: "owner", permissions: [] },
            { id: linId, kind: "agent", display_name: "Lin", handle: "lin", access_level: "member", permissions: [] },
            { id: reviewerId, kind: "agent", display_name: "Reviewer", handle: "reviewer", access_level: "member", permissions: [] },
          ],
          can_manage: true,
        });
      }
      if (path.endsWith("/members") && !init?.method) {
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
            id: linId,
            kind: "agent",
            display_name: "Lin",
            handle: "lin",
            access_level: "member",
            permissions: [],
          },
          {
            id: reviewerId,
            kind: "agent",
            display_name: "Reviewer",
            handle: "reviewer",
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
          messages: [{ ...message(channelId, 1, "First Message"), thread_id: 1, reply_count: 1, task: taskSummary() }],
        });
      }
      if (path.endsWith(`/channels/${channelId}/threads/1`) && !init?.method) {
        return json({
          channel_id: channelId,
          thread_id: 1,
          snapshot_channel_seq: 2,
          root: { ...message(channelId, 1, "First Message"), thread_id: 1, reply_count: 1, task: taskSummary() },
          replies: [message(channelId, 2, "Existing reply")],
          is_following: false,
        });
      }
      if (path.endsWith(`/channels/${channelId}/threads/1/subscription`) && init?.method === "PUT") {
        return json({ channel_id: channelId, thread_id: 1, is_following: true });
      }
      if (path.endsWith(`/channels/${channelId}/threads/1/subscription`) && init?.method === "DELETE") {
        return json({ channel_id: channelId, thread_id: 1, is_following: false });
      }
      if (path.endsWith(`/channels/${channelId}/threads/1/messages`) && init?.method === "POST") {
        return json(message(channelId, 3, "New Thread reply"), 201);
      }
      if (path.endsWith(`/channels/${channelId}/messages`) && init?.method === "POST") {
        const input = JSON.parse(String(init.body)) as { body_markdown: string };
        const seq = input.body_markdown === "Channel moved" ? 4 : 2;
        return json(message(channelId, seq, input.body_markdown), 201);
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
    const taskBadge = screen.getByLabelText("Task: Ship message metadata · in progress · Lin");
    expect(taskBadge).toHaveTextContent("TASK");
    expect(taskBadge).toHaveAttribute("data-tooltip", "Ship message metadata · in progress · Lin");
    const attachmentFile = new File(["pixel notes"], "notes.txt", { type: "text/plain" });
    Object.defineProperty(attachmentFile, "arrayBuffer", {
      value: async () => new TextEncoder().encode("pixel notes").buffer,
    });
    fireEvent.change(screen.getByLabelText("Choose Attachment"), {
      target: { files: [attachmentFile] },
    });
    expect(await screen.findByText("notes.txt")).toBeVisible();
    const input = screen.getByLabelText("Message");
    fireEvent.change(input, { target: { value: "@li", selectionStart: 3 } });
    const suggestions = await screen.findByRole("listbox", { name: "Mention suggestions" });
    expect(within(suggestions).getByText("Lin")).toBeVisible();
    fireEvent.keyDown(input, { key: "Enter" });
    expect(input).toHaveValue("@lin ");
    fireEvent.change(input, { target: { value: "@lin Please review", selectionStart: 18 } });
    fireEvent.submit(input.closest("form")!);

    expect(await screen.findByText("@lin Please review")).toBeVisible();
    expect(input).toHaveValue("");
    await waitFor(() => {
      const call = fetchMock.mock.calls.find(
        ([path, init]) => String(path).endsWith("/messages") && init?.method === "POST",
      );
      expect(JSON.parse(String(call?.[1]?.body))).toEqual({
        body_markdown: "@lin Please review",
        mentions: [linId],
        attachment_ids: ["019c0000-0000-7000-8000-000000000040"],
      });
    });

    fireEvent.click(screen.getByRole("button", { name: "Add Agents to Channel" }));
    const addDialog = screen.getByRole("dialog", { name: "Add Agents" });
    fireEvent.click(within(addDialog).getByRole("checkbox", { name: /Reviewer/ }));
    fireEvent.click(within(addDialog).getByRole("button", { name: "Add selected" }));
    await waitFor(() => {
      expect(fetchMock).toHaveBeenCalledWith(
        expect.stringContaining(`/channels/${channelId}/members`),
        expect.objectContaining({
          method: "POST",
          body: JSON.stringify({ agent_member_ids: [reviewerId] }),
        }),
      );
    });
    expect(await screen.findByText("3 Members")).toBeVisible();

    const preview = await screen.findByRole("region", { name: "1 Thread reply" });
    expect(within(preview).getByText("Existing reply")).toBeVisible();
    fireEvent.click(within(preview).getByRole("button", { name: "1 reply" }));
    const threadPane = await screen.findByRole("complementary", { name: /Thread #general:1/ });
    expect(within(threadPane).getByText("Existing reply")).toBeVisible();
    expect(within(threadPane).getByLabelText("Task: Ship message metadata · in progress · Lin")).toBeVisible();
    fireEvent.click(within(threadPane).getByRole("button", { name: "Close Thread" }));
    await waitFor(() => expect(screen.queryByRole("complementary", { name: /Thread #general:1/ })).not.toBeInTheDocument());
    await waitFor(() => expect(within(preview).getByRole("button", { name: "1 reply" })).toHaveFocus());
    fireEvent.click(within(preview).getByRole("button", { name: "1 reply" }));
    const reopenedThreadPane = await screen.findByRole("complementary", { name: /Thread #general:1/ });
    const channelInput = screen.getByLabelText("Message");
    fireEvent.change(channelInput, { target: { value: "Channel moved" } });
    fireEvent.submit(channelInput.closest("form")!);
    const contextUpdate = await within(reopenedThreadPane).findByRole("button", { name: /New messages in #general.*Return to latest/ });
    fireEvent.click(contextUpdate);
    await waitFor(() => expect(screen.queryByRole("complementary", { name: /Thread #general:1/ })).not.toBeInTheDocument());
    expect(screen.getByRole("heading", { name: "#general" })).toHaveFocus();

    fireEvent.click(within(preview).getByRole("button", { name: "1 reply" }));
    const latestThreadPane = await screen.findByRole("complementary", { name: /Thread #general:1/ });
    fireEvent.click(within(latestThreadPane).getByRole("button", { name: "Follow Thread" }));
    const unfollow = await screen.findByRole("button", { name: "Unfollow Thread" });
    fireEvent.click(unfollow);
    expect(await screen.findByRole("button", { name: "Follow Thread" })).toBeVisible();
    const threadInput = screen.getByLabelText("Thread reply");
    fireEvent.change(threadInput, { target: { value: "New Thread reply" } });
    fireEvent.submit(threadInput.closest("form")!);
    expect(await screen.findByText("New Thread reply")).toBeVisible();
    expect(threadInput).toHaveValue("");
    fireEvent.keyDown(window, { key: "Escape" });
    await waitFor(() => expect(screen.queryByRole("complementary", { name: /Thread #general:1/ })).not.toBeInTheDocument());
    await waitFor(() => expect(within(preview).getByRole("button", { name: "1 reply" })).toHaveFocus());
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
          accent: "#FE7DA8",
          owner_member_id: "019c0000-0000-7000-8000-000000000002",
          current_member_id: "019c0000-0000-7000-8000-000000000002",
          general_channel_id: "019c0000-0000-7000-8000-000000000003",
        });
      }
      if (path === "/api/v1/auth/me") {
        return json({ id: "user", display_name: "Ada Lovelace", email: "ada@example.test" });
      }
      if (path.endsWith(`/channels/${designId}/members`) && !init?.method) {
        return json({ members: [{ id: "019c0000-0000-7000-8000-000000000002", kind: "human", display_name: "Ada Lovelace", handle: "ada", access_level: "owner", permissions: [] }], can_manage: true });
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

function taskSummary() {
  return {
    id: "019c0000-0000-7000-8000-000000000090",
    title: "Ship message metadata",
    status: "in_progress",
    assigned_agent_member_id: "019c0000-0000-7000-8000-000000000020",
    assignee_name: "Lin",
  };
}

function json(body: unknown, status = 200): Response {
  return new Response(JSON.stringify(body), {
    status,
    headers: { "Content-Type": "application/json" },
  });
}
