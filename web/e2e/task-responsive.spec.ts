import { expect, test } from "@playwright/test";

const spaceId = "019c0000-0000-7000-8000-000000000001";
const ownerId = "019c0000-0000-7000-8000-000000000002";
const agentId = "019c0000-0000-7000-8000-000000000003";
const channelId = "019c0000-0000-7000-8000-000000000004";
const taskId = "019c0000-0000-7000-8000-000000000005";

test("completes the Task list and detail flow without viewport overflow", async ({ page }) => {
  let continuity = { state: "warm", generation: 2 };
  await page.route("**/api/v1/**", async (route) => {
    const request = route.request();
    const path = new URL(request.url()).pathname;
    const method = request.method();
    const body = (() => {
      if (path.includes("/spaces/by-slug/")) return space;
      if (path === "/api/v1/auth/me") return { id: "human", display_name: "Ada", email: "ada@example.test" };
      if (path.endsWith("/channels")) return { can_create: true, channels: [{ id: channelId, space_id: spaceId, kind: "public", slug: "general", created_by_member_id: ownerId, joined: true }] };
      if (path.endsWith("/dms")) return [];
      if (path.endsWith("/computers")) return [];
      if (path.endsWith("/members")) return [owner, agentMember];
      if (path.endsWith("/agents")) return [agent];
      if (path.endsWith(`/tasks/${taskId}/reset-session`) && method === "POST") {
        continuity = { state: "cold", generation: 3 };
        return task(continuity);
      }
      if (path.endsWith(`/tasks/${taskId}`)) return task(continuity);
      if (path.endsWith("/tasks")) return [task(continuity)];
      return undefined;
    })();
    if (body === undefined) {
      await route.fulfill({ status: 404, contentType: "application/json", body: JSON.stringify({ error: { code: "not_found", message: path, retryable: false } }) });
      return;
    }
    await route.fulfill({ status: 200, contentType: "application/json", body: JSON.stringify(body) });
  });

  await page.goto("/s/sumi-lab/tasks");
  await expect(page.getByRole("heading", { name: "Tasks", exact: true, level: 1 })).toBeVisible();
  await expect(page.getByRole("link", { name: /Rebuild WebUI/ })).toBeVisible();
  await expectNoHorizontalOverflow(page);

  await page.getByRole("link", { name: /Rebuild WebUI/ }).click();
  await expect(page).toHaveURL(new RegExp(`/tasks/${taskId}$`));
  await expect(page.getByRole("heading", { name: "Source Thread" })).toBeVisible();
  await expect(page.getByRole("heading", { name: "Related Threads" })).toBeVisible();
  await expect(page.getByRole("heading", { name: "Current Run and Focus" })).toBeVisible();
  await expect(page.getByText("WARM")).toBeVisible();
  await expectNoHorizontalOverflow(page);

  await page.getByRole("button", { name: /Reset continuity/ }).click();
  await expect(page.getByText("COLD")).toBeVisible();
  await expectNoHorizontalOverflow(page);
});

async function expectNoHorizontalOverflow(page: import("@playwright/test").Page) {
  const dimensions = await page.evaluate(() => ({ viewport: window.innerWidth, content: document.documentElement.scrollWidth }));
  expect(dimensions.content).toBeLessThanOrEqual(dimensions.viewport);
}

const space = {
  id: spaceId,
  name: "Sumi Lab",
  slug: "sumi-lab",
  accent: "#FE7DA8",
  owner_member_id: ownerId,
  current_member_id: ownerId,
  general_channel_id: channelId,
};

const owner = { id: ownerId, kind: "human", display_name: "Ada", access_level: "owner", permissions: [] };
const agentMember = { id: agentId, kind: "agent", display_name: "Lin", access_level: "member", permissions: [] };
const agent = { member_id: agentId, name: "Lin", desired_lifecycle: "active", activity_status: "running" };

function task(session_continuity: { state: string; generation: number }) {
  const source = { id: "019c0000-0000-7000-8000-000000000006", channel_id: channelId, channel_slug: "general", root_message_id: "019c0000-0000-7000-8000-000000000007", root_message_seq: 42, relation: "source" };
  const related = { id: "019c0000-0000-7000-8000-000000000008", channel_id: channelId, channel_slug: "design", root_message_id: "019c0000-0000-7000-8000-000000000009", root_message_seq: 7, relation: "related" };
  const run = { id: "019c0000-0000-7000-8000-000000000010", task_id: taskId, agent_member_id: agentId, agent_name: "Lin", focus: source, status: "running", started_at: "2026-07-30T01:00:00Z" };
  return {
    id: taskId,
    seq: 3,
    space_id: spaceId,
    title: "Rebuild WebUI",
    status: "in_progress",
    creator_member_id: ownerId,
    creator_name: "Ada",
    assignee_agent_member_id: agentId,
    assignee_name: "Lin",
    source_thread: source,
    related_threads: [related],
    current_run: run,
    recent_runs: [],
    session_continuity,
    created_at: "2026-07-30T00:00:00Z",
    updated_at: "2026-07-30T01:00:00Z",
  };
}
