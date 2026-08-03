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
        return json([{ channel_id: "dm", space_id: spaceId, other_member: { id: linId, kind: "agent", display_name: "Lin", access_level: "member", permissions: [] }, created_at: "2026-07-25T00:00:00Z" }]);
      }
      if (path.endsWith("/agents") && !init?.method) {
        return json([{ member_id: linId, activity_status: "working" }]);
      }
      if (path.endsWith("/computers") && !init?.method) return json([]);
      if (path.endsWith(`/channels/${channelId}/members`) && !init?.method) {
        return json({
          members: [
            { id: ownerId, kind: "human", display_name: "Ada", access_level: "owner", permissions: [] },
            { id: linId, kind: "agent", display_name: "Lin", access_level: "member", permissions: [] },
          ],
          can_manage: true,
        });
      }
      if (path.endsWith("/members") && !init?.method) {
        return json([
          { id: ownerId, kind: "human", display_name: "Ada", access_level: "owner", permissions: [] },
          { id: linId, kind: "agent", display_name: "Lin", access_level: "member", permissions: [] },
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
    expect(screen.getByRole("link", { name: "Sumi home" })).toBeVisible();
    expect(screen.getByRole("region", { name: "Finish your Space setup" })).toBeVisible();
    expect(screen.getByRole("link", { name: "Pair" })).toHaveAttribute(
      "href",
      "/s/sumi-lab/computers#pair-computer",
    );
    const channelComposer = screen.getByLabelText("Message");
    expect(channelComposer).toHaveAttribute("placeholder", "Message #general");
    expect(channelComposer).toHaveAttribute("rows", "1");
    expect(channelComposer.closest("form")).toHaveClass("composer");
    expect(screen.getAllByLabelText("Lin is Working").length).toBeGreaterThanOrEqual(2);
    const linIdenticons = screen.getAllByRole("img", { name: "Lin avatar" }).map((avatar) => avatar.getAttribute("data-agent-identicon"));
    expect(linIdenticons.every(Boolean)).toBe(true);
    expect(new Set(linIdenticons)).toHaveLength(1);
    expect(screen.getByRole("link", { name: /Lin avatar.*Lin is Working.*Lin.*Working/ })).toHaveAttribute("href", `/s/sumi-lab/dm/${linId}`);

    const shell = screen.getByRole("main");
    const navigation = screen.getByRole("complementary", { name: "Space navigation" });
    fireEvent.click(within(navigation).getByRole("button", { name: "Close navigation" }));
    expect(shell).toHaveClass("space-shell--navigation-collapsed");
    const railOpen = within(screen.getByRole("complementary", { name: "Space tools" })).getByRole("button", { name: "Open navigation" });
    expect(within(screen.getByRole("complementary", { name: "Space tools" })).queryByRole("img", { name: "Ada avatar" })).not.toBeInTheDocument();
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

  it("creates and opens a DM from conversation navigation", async () => {
    const spaceId = "019c0000-0000-7000-8000-000000000101";
    const ownerId = "019c0000-0000-7000-8000-000000000102";
    const channelId = "019c0000-0000-7000-8000-000000000103";
    const graceId = "019c0000-0000-7000-8000-000000000104";
    const linId = "019c0000-0000-7000-8000-000000000105";
    const dmChannelId = "019c0000-0000-7000-8000-000000000106";
    const owner = { id: ownerId, kind: "human", display_name: "Ada", access_level: "owner", permissions: [] };
    const grace = { id: graceId, kind: "human", display_name: "Grace Hopper", access_level: "member", permissions: [] };
    const lin = { id: linId, kind: "agent", display_name: "Lin", access_level: "member", permissions: [] };
    const createdDirectMessage = { channel_id: dmChannelId, space_id: spaceId, other_member: grace, created_at: "2026-07-28T00:00:00Z" };
    let directMessageCreated = false;
    const fetchMock = vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      const path = String(input);
      if (path.includes("/spaces/by-slug/")) return json({ id: spaceId, name: "Sumi Lab", slug: "sumi-lab", accent: "#FE7DA8", owner_member_id: ownerId, current_member_id: ownerId, general_channel_id: channelId });
      if (path === "/api/v1/auth/me") return json({ id: "user", display_name: "Ada", email: "ada@example.test" });
      if (path.endsWith("/channels") && !init?.method) return json({ can_create: true, channels: [{ id: channelId, space_id: spaceId, kind: "public", name: "general", slug: "general", created_by_member_id: ownerId, joined: true }] });
      if (path.endsWith("/dms") && !init?.method) return json(directMessageCreated ? [createdDirectMessage] : []);
      if (path.endsWith("/dms") && init?.method === "POST") {
        directMessageCreated = true;
        return json(createdDirectMessage, 201);
      }
      if (path.endsWith("/agents") && !init?.method) return json([{ member_id: linId, activity_status: "idle" }]);
      if (path.endsWith("/computers") && !init?.method) return json([]);
      if (path.endsWith(`/channels/${channelId}/members`) && !init?.method) return json({ members: [owner, grace, lin], can_manage: true });
      if (path.endsWith(`/channels/${channelId}/messages`) && !init?.method) return json({ channel_id: channelId, snapshot_channel_seq: 0, messages: [], has_more_before: false, has_more_after: false });
      if (path.endsWith(`/channels/${dmChannelId}/members`) && !init?.method) return json({ members: [owner, grace], can_manage: false });
      if (path.endsWith(`/channels/${dmChannelId}/messages`) && !init?.method) return json({ channel_id: dmChannelId, snapshot_channel_seq: 0, messages: [], has_more_before: false, has_more_after: false });
      if (path.endsWith(`/channels/${dmChannelId}/messages`) && init?.method === "POST") {
        const input = JSON.parse(String(init.body)) as { body_markdown: string; mentions?: string[]; mention_all?: boolean };
        expect(input.mentions).toEqual([]);
        expect(input.mention_all).toBe(false);
        return json(message(dmChannelId, 1, input.body_markdown), 201);
      }
      if (path.endsWith("/members") && !init?.method) return json([owner, grace, lin]);
      throw new Error(`Unexpected request: ${path}`);
    });
    vi.stubGlobal("fetch", fetchMock);
    renderRoute("/s/sumi-lab/channels/general");

    expect(await screen.findByRole("heading", { name: "#general starts here." })).toBeVisible();
    fireEvent.click(screen.getByRole("button", { name: "Start DM" }));
    const dialog = screen.getByRole("dialog", { name: "Start DM" });
    expect(within(dialog).queryByText("@ada")).not.toBeInTheDocument();
    fireEvent.change(within(dialog).getByLabelText("Find a Member"), { target: { value: "grace" } });
    expect(within(dialog).queryByText("@lin")).not.toBeInTheDocument();
    fireEvent.click(within(dialog).getByRole("button", { name: /Grace Hopper.*Start/i }));

    await waitFor(() => {
      expect(fetchMock).toHaveBeenCalledWith(
        expect.stringContaining(`/spaces/${spaceId}/dms`),
        expect.objectContaining({ method: "POST", body: JSON.stringify({ member_id: graceId }) }),
      );
    });
    await waitFor(() => expect(screen.getByRole("heading", { name: "Grace Hopper" })).toBeVisible());
    const dmComposer = screen.getByRole("textbox", { name: "Message" });
    expect(dmComposer).toHaveAttribute("placeholder", "Message Grace Hopper");
    fireEvent.change(dmComposer, { target: { value: "@gra" } });
    expect(screen.queryByRole("listbox", { name: "Mention suggestions" })).not.toBeInTheDocument();
    fireEvent.change(dmComposer, { target: { value: "hello @grace" } });
    fireEvent.click(screen.getByRole("button", { name: "Send message" }));
    await waitFor(() => expect(fetchMock).toHaveBeenCalledWith(
      expect.stringContaining(`/channels/${dmChannelId}/messages`),
      expect.objectContaining({ method: "POST" }),
    ));
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
              access_level: "owner",
              permissions: [],
            },
            {
              id: linId,
              kind: "agent",
              display_name: "Lin",
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
            { id: "019c0000-0000-7000-8000-000000000002", kind: "human", display_name: "Ada Lovelace", access_level: "owner", permissions: [] },
            { id: linId, kind: "agent", display_name: "Lin", access_level: "member", permissions: [] },
            { id: reviewerId, kind: "agent", display_name: "Reviewer", access_level: "member", permissions: [] },
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
            access_level: "owner",
            permissions: [],
          },
          {
            id: linId,
            kind: "agent",
            display_name: "Lin",
            access_level: "member",
            permissions: [],
          },
          {
            id: reviewerId,
            kind: "agent",
            display_name: "Reviewer",
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
          messages: [{
            ...message(channelId, 1, "First Message"),
            thread_id: "1",
            reply_count: 1,
            task: taskSummary(),
            attention_failures: [
              { agent_member_id: linId, error_code: "run_claim_unavailable", retrying: true },
              { agent_member_id: reviewerId, error_code: "computer_offline", retrying: false },
            ],
          }],
        });
      }
      if (path.endsWith("/threads/1") && !init?.method) {
        return json({
          channel_id: channelId,
          thread_id: "1",
          snapshot_channel_seq: 2,
          root: { ...message(channelId, 1, "First Message"), thread_id: "1", reply_count: 1, task: taskSummary() },
          replies: [agentCreatedMessage(channelId, 2, reviewerId)],
          is_following: false,
          task: taskSummary(),
          task_relation: "related",
        });
      }
      if (path.endsWith("/threads/1/subscription") && init?.method === "PUT") {
        return json({ channel_id: channelId, thread_id: 1, is_following: true });
      }
      if (path.endsWith("/threads/1/subscription") && init?.method === "DELETE") {
        return json({ channel_id: channelId, thread_id: 1, is_following: false });
      }
      if (path.endsWith("/threads/1/messages") && init?.method === "POST") {
        return json(message(channelId, 3, "New Thread reply"), 201);
      }
      if (path.includes("/root-messages/") && path.endsWith("/task") && init?.method === "POST") {
        const created = taskSummary();
        return json({
          ...created,
          space_id: "019c0000-0000-7000-8000-000000000001",
          creator_member_id: "019c0000-0000-7000-8000-000000000002",
          creator_name: "Ada Lovelace",
          source_thread: { id: "1", channel_id: channelId, channel_slug: "general", root_message_id: "019c0000-0000-7000-8000-000000000102", root_message_seq: 2, relation: "source" },
          related_threads: [],
          recent_runs: [],
          session_continuity: { state: "cold" },
          created_at: "2026-07-25T00:00:00Z",
          updated_at: "2026-07-25T00:00:00Z",
          status: "todo",
          title: "@lin Please review",
          assignee_agent_member_id: undefined,
          assignee_name: undefined,
        }, 201);
      }
      if (path.endsWith(`/channels/${channelId}/messages`) && init?.method === "POST") {
        const input = JSON.parse(String(init.body)) as { body_markdown: string; mention_all?: boolean };
        const seq = input.body_markdown === "Channel moved" ? 6 : input.body_markdown === "@all, review" ? 5 : 2;
        return json({ ...message(channelId, seq, input.body_markdown), mentions: input.body_markdown.includes("@lin") ? [linId] : [], mention_all: input.mention_all ?? false }, 201);
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
    const systemErrors = screen.getByRole("alert");
    expect(systemErrors).toHaveTextContent("2 system errors · Show details");
    expect(systemErrors).not.toHaveAttribute("open");
    fireEvent.click(screen.getByText("2 system errors · Show details"));
    expect(systemErrors).toHaveAttribute("open");
    expect(within(systemErrors).getByText((_, element) => element?.tagName === "P" && element.textContent?.includes("Could not start Lin") === true)).toBeVisible();
    const taskBadge = screen.getByLabelText("Task: !7 Ship message metadata · in progress · Lin");
    expect(taskBadge).toHaveTextContent("Ship message metadata");
    expect(taskBadge).toHaveAttribute("title", "!7 Ship message metadata · in progress · Lin");
    const attachmentFile = new File(["pixel notes"], "notes.txt", { type: "text/plain" });
    Object.defineProperty(attachmentFile, "arrayBuffer", {
      value: async () => new TextEncoder().encode("pixel notes").buffer,
    });
    fireEvent.change(screen.getByLabelText("Choose Attachment"), {
      target: { files: [attachmentFile] },
    });
    expect(await screen.findByText("notes.txt")).toBeVisible();
    const input = screen.getByLabelText("Message");
    fireEvent.change(input, { target: { value: "@a", selectionStart: 2 } });
    const allSuggestions = await screen.findByRole("listbox", { name: "Mention suggestions" });
    expect(within(allSuggestions).getByText("Everyone")).toBeVisible();
    fireEvent.click(within(allSuggestions).getByRole("option", { name: /Everyone.*@all/ }));
    expect(input).toHaveValue("@all ");
    fireEvent.change(input, { target: { value: "@li", selectionStart: 3 } });
    const suggestions = await screen.findByRole("listbox", { name: "Mention suggestions" });
    expect(within(suggestions).getByText("Lin")).toBeVisible();
    fireEvent.keyDown(input, { key: "Enter" });
    expect(input).toHaveValue("@Lin ");
    fireEvent.change(input, { target: { value: "@lin Please review", selectionStart: 18 } });
    fireEvent.submit(input.closest("form")!);

    await waitFor(() => expect(input).toHaveValue(""));
    const agentMention = await screen.findByRole("link", { name: "Open Lin Agent management" });
    expect(agentMention).toHaveClass("message-mention", "message-mention--agent");
    expect(agentMention).toHaveAttribute("href", "/s/sumi-lab/agents/019c0000-0000-7000-8000-000000000020");
    await waitFor(() => {
      const call = fetchMock.mock.calls.find(
        ([path, init]) => String(path).endsWith("/messages") && init?.method === "POST",
      );
      expect(JSON.parse(String(call?.[1]?.body))).toEqual({
        body_markdown: "@lin Please review",
        mentions: [linId],
        mention_all: false,
        attachment_ids: ["019c0000-0000-7000-8000-000000000040"],
      });
    });
    fireEvent.change(input, { target: { value: "@all, review", selectionStart: 12 } });
    fireEvent.submit(input.closest("form")!);
    await waitFor(() => expect(input).toHaveValue(""));
    await waitFor(() => {
      const call = fetchMock.mock.calls.find(
        ([path, init]) => String(path).endsWith("/messages") && init?.method === "POST" && String(init.body).includes("@all, review"),
      );
      expect(JSON.parse(String(call?.[1]?.body))).toEqual({
        body_markdown: "@all, review",
        mentions: [],
        mention_all: true,
        attachment_ids: [],
      });
    });
    expect(await screen.findByText("@all", { selector: "mark" })).toHaveClass("message-mention");
    fireEvent.click(screen.getAllByRole("button", { name: "Create Task" })[0]);
    await waitFor(() => expect(fetchMock).toHaveBeenCalledWith(
      expect.stringContaining("/root-messages/019c0000-0000-7000-8000-000000000102/task"),
      expect.objectContaining({ method: "POST" }),
    ));
    expect(await screen.findByRole("link", { name: /Task: !7 @lin Please review/ })).toBeVisible();

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
    const preview = await screen.findByRole("region", { name: "1 Thread reply" });
    expect(within(preview).getByText("Created agent Reviewer · active")).toBeVisible();
    fireEvent.click(within(preview).getByRole("button", { name: "1 reply" }));
    const threadPane = await screen.findByRole("complementary", { name: /Thread #general:1/ });
    expect(threadPane.querySelector(".action-message")).toHaveTextContent("Ada Lovelace Created agent Reviewer active");
    expect(within(threadPane).getByRole("link", { name: "Ship message metadata RELATED" })).toHaveAttribute("href", "/s/sumi-lab/tasks/019c0000-0000-7000-8000-000000000090");
    expect(within(threadPane).getByRole("img", { name: "Reviewer avatar" })).toHaveAttribute("data-agent-identicon");
    expect(within(threadPane).queryByText(/member_id|lifecycle.*active/)).not.toBeInTheDocument();
    expect(within(threadPane).getByLabelText("Task: !7 Ship message metadata · in progress · Lin")).toBeVisible();
    const resizeHandle = within(threadPane).getByRole("separator", { name: "Resize Thread pane" });
    expect(resizeHandle).toHaveAttribute("aria-orientation", "vertical");
    expect(resizeHandle).toHaveAttribute("aria-valuemin", "360");
    expect(resizeHandle).toHaveAttribute("aria-valuemax", "480");
    expect(resizeHandle).toHaveAttribute("aria-valuenow", "360");
    fireEvent.keyDown(resizeHandle, { key: "ArrowLeft" });
    expect(resizeHandle).toHaveAttribute("aria-valuenow", "368");
    fireEvent.keyDown(resizeHandle, { key: "ArrowLeft", shiftKey: true });
    expect(resizeHandle).toHaveAttribute("aria-valuenow", "392");
    fireEvent.keyDown(resizeHandle, { key: "End" });
    expect(resizeHandle).toHaveAttribute("aria-valuenow", "480");
    fireEvent.keyDown(resizeHandle, { key: "Home" });
    expect(resizeHandle).toHaveAttribute("aria-valuenow", "360");
    fireEvent.pointerDown(resizeHandle, { button: 0, clientX: 500, pointerId: 7 });
    fireEvent.pointerMove(window, { clientX: 460, pointerId: 7 });
    expect(resizeHandle).toHaveAttribute("aria-valuenow", "400");
    fireEvent.pointerUp(window, { pointerId: 7 });
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
    expect(threadInput).toHaveAttribute("placeholder", "Reply to #general");
    expect(threadInput).toHaveAttribute("rows", "1");
    expect(threadInput.closest("form")).toHaveClass("composer", "thread-composer");
    expect(threadInput).toHaveStyle({ height: "42px" });
    expect(screen.getByLabelText("Message")).toHaveStyle({ height: "42px" });
    fireEvent.change(threadInput, { target: { value: "New Thread reply" } });
    fireEvent.submit(threadInput.closest("form")!);
    expect(await screen.findByText("New Thread reply")).toBeVisible();
    expect(threadInput).toHaveValue("");
    fireEvent.keyDown(window, { key: "Escape" });
    await waitFor(() => expect(screen.queryByRole("complementary", { name: /Thread #general:1/ })).not.toBeInTheDocument());
    await waitFor(() => expect(within(preview).getByRole("button", { name: "1 reply" })).toHaveFocus());
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
    },
    thread_id: "1",
    placement: "root",
    content: { type: "text", body_markdown: body },
    mentions: [],
    mention_all: false,
    attachments: [],
    attention_failures: [],
    created_at: "2026-07-25T00:00:00Z",
    reply_count: 0,
  };
}

function taskSummary() {
  return {
    id: "019c0000-0000-7000-8000-000000000090",
    seq: 7,
    title: "Ship message metadata",
    status: "in_progress",
    assignee_agent_member_id: "019c0000-0000-7000-8000-000000000020",
    assignee_name: "Lin",
    working_elsewhere: false,
  };
}

function agentCreatedMessage(channelId: string, seq: number, memberId: string) {
  return {
    ...message(channelId, seq, ""),
    placement: "reply",
    content: {
      type: "agent_created",
      agent: { member_id: memberId, name: "Reviewer", lifecycle: "active", available: true },
    },
  };
}

function json(body: unknown, status = 200): Response {
  return new Response(JSON.stringify(body), {
    status,
    headers: { "Content-Type": "application/json" },
  });
}
