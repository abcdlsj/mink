#!/usr/bin/env node
// Design-lab sample set. Drives the same HTTP API the Browser uses to leave a
// seeded Space in a state that exercises every visual/AX state we review:
// Markdown forms, mentions, attachments, Task badges and all five statuses,
// Thread replies, DM inbox items, Action Messages, System Notices, empty
// states. Idempotent: re-running fills only what is missing.
//
// Requires the dev-seed Space (sumi-dev) to exist first. Run through
// `mise run demo`; standalone: SUMI_SEED_SERVER=http://127.0.0.1:3001 \
//   node design-lab/demo/seed-samples.mjs

import { createHash } from "node:crypto";

import { uuidv7 } from "../../web/scripts/uuid.mjs";

const SERVER = process.env.SUMI_SEED_SERVER ?? "http://127.0.0.1:3001";
const OWNER_EMAIL = process.env.SUMI_SEED_EMAIL ?? "dev@example.test";
const OWNER_PASSWORD = process.env.SUMI_SEED_PASSWORD ?? "correct horse battery staple";
const OWNER_NAME = "abcdlsj";
const MARA_EMAIL = "mara@example.test";
const MARA_PASSWORD = "correct horse battery staple";
const MARA_NAME = "Mara";
const SPACE_SLUG = "sumi-dev";

const sleep = (ms) => new Promise((resolve) => setTimeout(resolve, ms));
const log = (...args) => console.log("[samples]", ...args);

async function request(method, path, { cookie, body } = {}) {
  const headers = { "idempotency-key": uuidv7() };
  if (cookie) headers.cookie = cookie;
  if (body !== undefined) headers["content-type"] = "application/json";
  const response = await fetch(new URL(path, SERVER), {
    method,
    headers,
    body: body !== undefined ? JSON.stringify(body) : undefined,
  });
  const json = await response.json().catch(() => undefined);
  return { response, json };
}

async function ok(requestName, result, expected = 200) {
  if (result.response.status !== expected) {
    throw new Error(
      `${requestName} failed: ${result.response.status} ${JSON.stringify(result.json)}`,
    );
  }
  return result.json;
}

async function login(email, password) {
  const result = await request("POST", "/api/v1/auth/login", {
    body: { email, password },
  });
  if (result.response.status !== 200) {
    throw new Error(`login ${email} failed: ${result.response.status} ${JSON.stringify(result.json)}`);
  }
  const cookie = result.response.headers.get("set-cookie")?.split(";")[0];
  if (!cookie) throw new Error(`login ${email} returned no session cookie`);
  return cookie;
}

async function registerOrLogin(email, password, displayName) {
  const registered = await request("POST", "/api/v1/auth/register", {
    body: { display_name: displayName, email, password },
  });
  if (registered.response.status === 201) {
    log(`registered ${displayName}`);
    const cookie = registered.response.headers.get("set-cookie")?.split(";")[0];
    if (!cookie) throw new Error(`register ${email} returned no session cookie`);
    return cookie;
  }
  return login(email, password);
}

async function waitForHealth() {
  const deadline = Date.now() + 30_000;
  while (Date.now() < deadline) {
    try {
      const response = await fetch(new URL("/api/v1/health", SERVER));
      if (response.ok) return;
    } catch {
      // Server not up yet.
    }
    await sleep(300);
  }
  throw new Error("Sumi server did not become healthy within 30s");
}

async function getSpace(cookie) {
  return ok("space lookup", await request("GET", `/api/v1/spaces/by-slug/${SPACE_SLUG}`, { cookie }));
}

async function listChannels(cookie, spaceId) {
  return (await ok("channel list", await request("GET", `/api/v1/spaces/${spaceId}/channels`, { cookie })))
    .channels;
}

async function listAgents(cookie, spaceId) {
  return ok("agent list", await request("GET", `/api/v1/spaces/${spaceId}/agents`, { cookie }));
}

async function listMembers(cookie, spaceId) {
  return ok("member list", await request("GET", `/api/v1/spaces/${spaceId}/members`, { cookie }));
}

async function listTasks(cookie, spaceId) {
  return ok("task list", await request("GET", `/api/v1/spaces/${spaceId}/tasks`, { cookie }));
}

async function getTask(cookie, taskId) {
  return ok("task read", await request("GET", `/api/v1/tasks/${taskId}`, { cookie }));
}

async function listMessages(cookie, channelId) {
  const page = await ok(
    "message list",
    await request("GET", `/api/v1/channels/${channelId}/messages`, { cookie }),
  );
  return page.messages;
}

async function listDms(cookie, spaceId) {
  return ok("dm list", await request("GET", `/api/v1/spaces/${spaceId}/dms`, { cookie }));
}

async function postMessage(cookie, channelId, bodyMarkdown, { mentions = [], attachmentIds = [] } = {}) {
  const message = await ok(
    "post message",
    await request("POST", `/api/v1/channels/${channelId}/messages`, {
      cookie,
      body: {
        body_markdown: bodyMarkdown,
        mention_all: false,
        mentions,
        attachment_ids: attachmentIds,
      },
    }),
    201,
  );
  return message;
}

async function findMessageByMarker(cookie, channelId, marker) {
  const messages = await listMessages(cookie, channelId);
  return messages.find(
    (message) =>
      message.content?.type === "text" && message.content.body_markdown.includes(marker),
  );
}

async function ensureMessage(cookie, channelId, marker, bodyMarkdown, options = {}) {
  const existing = await findMessageByMarker(cookie, channelId, marker);
  if (existing) return existing;
  return postMessage(cookie, channelId, bodyMarkdown, options);
}

async function ensureAttachmentMessage(cookie, spaceId, channelId, marker, bodyMarkdown) {
  const existing = await findMessageByMarker(cookie, channelId, marker);
  if (existing) return existing;
  const attachment = await ensureUploadedAttachment(
    cookie,
    spaceId,
    "sumi-notes.txt",
    "pixel notes\nshared sample file for attachment rendering.\n",
  );
  return postMessage(cookie, channelId, bodyMarkdown, { attachmentIds: [attachment.id] });
}

async function postThreadReply(cookie, threadId, bodyMarkdown, { mentions = [] } = {}) {
  return ok(
    "thread reply",
    await request("POST", `/api/v1/threads/${threadId}/messages`, {
      cookie,
      body: {
        body_markdown: bodyMarkdown,
        mention_all: false,
        mentions,
        attachment_ids: [],
      },
    }),
    201,
  );
}

async function createThreadReplyIfMissing(cookie, threadId, marker) {
  const thread = await ok(
    "thread read",
    await request("GET", `/api/v1/threads/${threadId}`, { cookie }),
  );
  if (thread.replies.some((reply) => reply.content?.body_markdown?.includes(marker))) {
    return;
  }
  await postThreadReply(cookie, threadId, marker);
}

async function ensureChannel(cookie, spaceId, slug, topic, agentMemberIds = []) {
  const channels = await listChannels(cookie, spaceId);
  const existing = channels.find((channel) => channel.slug === slug);
  if (existing) return existing;
  const channel = await ok(
    `create channel #${slug}`,
    await request("POST", `/api/v1/spaces/${spaceId}/channels`, {
      cookie,
      body: { slug, topic, kind: "public", agent_member_ids: agentMemberIds },
    }),
    201,
  );
  log(`created channel #${slug}`);
  return channel;
}

async function joinChannel(cookie, channelId, displayName) {
  const result = await request("POST", `/api/v1/channels/${channelId}/members/me`, { cookie });
  if (![200, 201].includes(result.response.status)) {
    throw new Error(`${displayName} join #${channelId} failed: ${result.response.status}`);
  }
}

async function ensureMara(ownerCookie, space, members) {
  const existing = members.find(
    (member) => member.kind === "human" && member.display_name === MARA_NAME,
  );
  if (existing) return existing;

  const maraCookie = await registerOrLogin(MARA_EMAIL, MARA_PASSWORD, MARA_NAME);
  const invitation = await request("POST", `/api/v1/spaces/${space.id}/invites`, {
    cookie: ownerCookie,
    body: { email: MARA_EMAIL },
  });
  if (invitation.response.status !== 201) {
    throw new Error(
      `create invitation failed: ${invitation.response.status} ${JSON.stringify(invitation.json)}`,
    );
  }
  const token = invitation.json.token;
  if (!token) throw new Error("invitation returned no token");
  const member = await ok(
    "accept invitation",
    await request("POST", `/api/v1/invites/${encodeURIComponent(token)}/accept`, {
      cookie: maraCookie,
    }),
    201,
  );
  log(`Mara joined ${space.slug}`);
  return member;
}

async function ensureUploadedAttachment(cookie, spaceId, name, content) {
  const opened = await ok(
    "open upload",
    await request("POST", "/api/v1/attachments/uploads", {
      cookie,
      body: { space_id: spaceId, original_name: name, media_type: "text/plain" },
    }),
    201,
  );
  const upload = await fetch(new URL(opened.upload_path, SERVER), {
    method: "PUT",
    headers: { cookie, "idempotency-key": uuidv7() },
    body: content,
  });
  if (!upload.ok) throw new Error(`upload content failed: ${upload.status}`);
  return ok(
    "complete upload",
    await request("POST", `/api/v1/attachments/${opened.id}/complete`, {
      cookie,
      body: {
        size: Buffer.byteLength(content),
        sha256: createHash("sha256").update(content).digest("hex"),
      },
    }),
    200,
  );
}

async function startTask(cookie, task, assigneeAgentMemberId) {
  return ok(
    "start task",
    await request("POST", `/api/v1/tasks/${task.id}/start`, {
      cookie,
      body: { assignee_agent_member_id: assigneeAgentMemberId },
    }),
    200,
  );
}

async function completeTask(cookie, task, resultMarkdown, resultThreadId) {
  return ok(
    "complete task",
    await request("POST", `/api/v1/tasks/${task.id}/done`, {
      cookie,
      body: { result_markdown: resultMarkdown, result_thread_id: resultThreadId },
    }),
    200,
  );
}

async function closeTask(cookie, task, reason, note) {
  return ok(
    "close task",
    await request("POST", `/api/v1/tasks/${task.id}/close`, {
      cookie,
      body: { reason, note },
    }),
    200,
  );
}

async function linkTaskThread(cookie, task, threadId) {
  return ok(
    "link thread",
    await request("POST", `/api/v1/tasks/${task.id}/threads`, {
      cookie,
      body: { thread_id: threadId },
    }),
    200,
  );
}

async function ensureTaskRoot(cookie, channelId, marker) {
  const existingRoot = await findMessageByMarker(cookie, channelId, marker);
  if (existingRoot) return existingRoot;
  return postMessage(cookie, channelId, marker);
}

async function createTaskFromRoot(cookie, root, title) {
  const task = await ok(
    `create task ${title}`,
    await request("POST", `/api/v1/root-messages/${root.id}/task`, {
      cookie,
      body: { title },
    }),
    201,
  );
  log(`created task ${title}`);
  return task;
}

async function askAgentToSubmitReview(cookie, task, agent, attempt = 1) {
  if (attempt > 2) return false;
  const instruction = `Sample: @${agent.name} — submit this task for review with the task CLI (sumi task submit-review).`;
  const thread = await ok(
    "task thread read",
    await request("GET", `/api/v1/threads/${task.source_thread.id}`, { cookie }),
  );
  if (!thread.replies.some((reply) => reply.content?.body_markdown?.includes(instruction))) {
    await postThreadReply(cookie, task.source_thread.id, instruction, {
      mentions: [agent.member_id],
    });
  }
  log(`asked ${agent.name} to submit task ${task.title}; waiting for a real Run...`);
  const deadline = Date.now() + 150_000;
  while (Date.now() < deadline) {
    await sleep(3000);
    const current = await getTask(cookie, task.id);
    if (current.status === "in_review") return true;
  }
  return askAgentToSubmitReview(cookie, task, agent, attempt + 1);
}

async function cancelActiveRuns(cookie, spaceId) {
  const agents = await listAgents(cookie, spaceId);
  for (const agent of agents) {
    const current = await request(
      "GET",
      `/api/v1/agents/${agent.member_id}/runs/current`,
      { cookie },
    );
    if (current.response.status !== 200) continue;
    const run = current.json?.current_run;
    if (!run?.id) continue;
    const canceled = await request(
      "POST",
      `/api/v1/agents/${agent.member_id}/runs/${run.id}/cancel`,
      { cookie },
    );
    if (canceled.response.ok) log(`canceled stale run for ${agent.name}`);
  }
}

async function main() {
  await waitForHealth();
  const ownerCookie = await login(OWNER_EMAIL, OWNER_PASSWORD);
  const space = await getSpace(ownerCookie);
  const [channels, agents, members] = await Promise.all([
    listChannels(ownerCookie, space.id),
    listAgents(ownerCookie, space.id),
    listMembers(ownerCookie, space.id),
  ]);
  const ownerMemberId = space.owner_member_id;
  const leo = agents.find((agent) => agent.name === "Leo");
  const iris = agents.find((agent) => agent.name === "Iris");
  const nora = agents.find((agent) => agent.name === "Nora");
  if (!leo || !iris || !nora) {
    throw new Error(`seed Agents missing; found: ${agents.map((agent) => agent.name).join(", ")}`);
  }

  const mara = await ensureMara(ownerCookie, space, members);
  const general = channels.find((channel) => channel.slug === "general");
  if (!general) throw new Error("dev-seed did not create #general");
  const maraCookie = await login(MARA_EMAIL, MARA_PASSWORD);

  // #general samples: Markdown forms, agent mention, attachment, owner mention.
  await ensureMessage(
    ownerCookie,
    general.id,
    "Sample: Markdown buffet",
    [
      "## Sample: Markdown buffet",
      "",
      "- [x] wire timeline grouping",
      "- [ ] tabular numbers",
      "",
      "> A quoted line from the plan.",
      "",
      "Inline `code`, **bold**, *italic*, ~~strike~~ and [docs](https://example.test).",
      "",
      "```",
      "fn main() {",
      '  println!("sumi");',
      "}",
      "```",
    ].join("\n"),
  );
  // Kept as plain text: a real mention would start a Run and occupy an Agent
  // before the review samples below are driven.
  await ensureMessage(
    ownerCookie,
    general.id,
    "Sample: agent note — Leo, confirm you are online",
    "Sample: agent note — Leo, confirm you are online.",
  );
  await ensureAttachmentMessage(
    ownerCookie,
    space.id,
    general.id,
    "Sample: attachment — shared notes",
    "Sample: attachment — shared notes.",
  );
  await joinChannel(maraCookie, general.id, MARA_NAME);

  // #design-lab: review thread plus one root per Task status.
  const designLab = await ensureChannel(
    ownerCookie,
    space.id,
    "design-lab",
    "Visual and AX review samples",
    [leo.member_id, iris.member_id, nora.member_id],
  );
  await joinChannel(maraCookie, designLab.id, MARA_NAME);

  const threadRoot = await ensureMessage(
    ownerCookie,
    designLab.id,
    "Sample: thread root — avatar refresh",
    "Sample: thread root — avatar refresh proposal. Keep the pixel seal, move the colors.",
  );
  await createThreadReplyIfMissing(
    maraCookie,
    threadRoot.thread_id,
    "Sample: reply — seal stays, colors can move.",
  );
  await createThreadReplyIfMissing(
    ownerCookie,
    threadRoot.thread_id,
    "Sample: reply — agreed, moving to review.",
  );

  const taskSamples = [
    {
      marker: "Sample task: TODO — review backlog",
      title: "TODO review backlog",
      target: "todo",
    },
    {
      marker: "Sample task: In Progress — pixel avatar refresh",
      title: "Pixel avatar refresh",
      target: "in_progress",
    },
    {
      marker: "Sample task: In Review — timeline density",
      title: "Timeline density",
      target: "in_review",
    },
    {
      marker: "Sample task: Done — tabular numbers",
      title: "Tabular numbers",
      target: "done",
    },
    {
      marker: "Sample task: Closed — duplicate proposal",
      title: "Duplicate proposal",
      target: "closed",
    },
  ];

  const existingTasks = await listTasks(ownerCookie, space.id);
  const sampleTasks = new Map();
  for (const sample of taskSamples) {
    let task = existingTasks.find((candidate) => candidate.title === sample.title);
    if (!task) {
      const root = await ensureTaskRoot(ownerCookie, designLab.id, sample.marker);
      task = await createTaskFromRoot(ownerCookie, root, sample.title);
    }
    if (
      ["in_progress", "in_review", "done"].includes(sample.target)
      && task.status === "todo"
    ) {
      task = await startTask(ownerCookie, task, leo.member_id);
    }
    if (sample.target === "closed" && task.status !== "closed") {
      task = await closeTask(
        ownerCookie,
        task,
        "duplicate",
        "Sample note: duplicate of another proposal.",
      );
    }
    sampleTasks.set(sample.title, task);
  }

  // In Review and Done need a real assignee Run: submit_review is an Agent-only
  // action, so we mention the assignee in the Source Thread and wait for the
  // Computer daemon to execute it. Failing this only skips those two samples.
  await cancelActiveRuns(ownerCookie, space.id);
  for (const sample of taskSamples.filter((candidate) =>
    ["in_review", "done"].includes(candidate.target),
  )) {
    const task = sampleTasks.get(sample.title);
    if (task.status === "in_review" || task.status === "done") continue;
    if (task.status === "todo") {
      await startTask(ownerCookie, task, leo.member_id);
    }
    const submitted = await askAgentToSubmitReview(ownerCookie, task, leo);
    if (!submitted) {
      log(`warning: ${sample.title} stayed in_progress; no Agent Run completed submit-review`);
      continue;
    }
    if (sample.target === "done") {
      const inReview = await getTask(ownerCookie, task.id);
      await completeTask(
        ownerCookie,
        inReview,
        "Sample result: verified and accepted.",
        inReview.source_thread.id,
      );
      log(`completed ${sample.title}`);
    }
  }

  // Task reference sample references the Done task sequence.
  const doneTask = sampleTasks.get("Tabular numbers");
  if (doneTask) {
    const freshDone = await getTask(ownerCookie, doneTask.id);
    if (freshDone.status === "done") {
      await ensureMessage(
        ownerCookie,
        general.id,
        "Sample: task reference",
        `Sample: task reference — see !${freshDone.seq}.`,
      );
      await linkTaskThread(ownerCookie, freshDone, threadRoot.thread_id);
    } else {
      log("warning: Done sample missing; task reference and related-thread samples skipped");
    }
  }

  // #empty-lab: empty state sample.
  await ensureChannel(ownerCookie, space.id, "empty-lab", "Empty state sample", []);

  // Mara mentions the owner in #general so Inbox has a mention item.
  await ensureMessage(
    maraCookie,
    general.id,
    "Sample: mention — abcdlsj, review the sample thread",
    `Sample: mention — @${OWNER_NAME}, review the sample thread.`,
    { mentions: [ownerMemberId] },
  );

  // DM samples: Mara -> owner (inbox) and owner -> Iris (Agent DM directory).
  let maraDm = (await listDms(maraCookie, space.id)).find(
    (dm) => dm.other_member.id === ownerMemberId,
  );
  if (!maraDm) {
    maraDm = await ok(
      "open Mara DM",
      await request("POST", `/api/v1/spaces/${space.id}/dms`, {
        cookie: maraCookie,
        body: { member_id: ownerMemberId },
      }),
      201,
    );
  }
  await ensureMessage(
    maraCookie,
    maraDm.channel_id,
    "Sample: DM — ping for the design review",
    "Sample: DM — ping for the design review.",
  );

  const ownerDms = await listDms(ownerCookie, space.id);
  const irisDm = ownerDms.find((dm) => dm.other_member.id === iris.member_id);
  if (!irisDm) {
    await ok(
      "open Iris DM",
      await request("POST", `/api/v1/spaces/${space.id}/dms`, {
        cookie: ownerCookie,
        body: { member_id: iris.member_id },
      }),
      201,
    );
    log("opened owner ↔ Iris DM");
  }

  const finalTasks = await listTasks(ownerCookie, space.id);
  const finalChannels = await listChannels(ownerCookie, space.id);
  const inbox = await ok(
    "inbox list",
    await request("GET", `/api/v1/members/${ownerMemberId}/inbox`, { cookie: ownerCookie }),
  );
  log(`sample set ready: ${finalChannels.length} channels, ${finalTasks.length} tasks, ${inbox.length} inbox items`);
}

main().catch((error) => {
  console.error("[samples] failed:", error.message);
  console.error(error.stack);
  process.exit(1);
});
