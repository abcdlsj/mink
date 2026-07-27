#!/usr/bin/env node
// Dev-only probe: logs in as the seed owner, posts a message mentioning @sol in
// #general, and polls until an agent-authored reply appears. Verifies the codex
// driver actually produces a real reply end-to-end. Usage:
//   node scripts/dev-probe.mjs <space-slug>

import { randomBytes } from "node:crypto";

const SERVER = process.env.SUMI_SEED_SERVER ?? "http://127.0.0.1:3000";
const OWNER_EMAIL = process.env.SUMI_SEED_EMAIL ?? "dev@example.test";
const OWNER_PASSWORD = process.env.SUMI_SEED_PASSWORD ?? "correct horse battery staple";
const SLUG = process.argv[2];

if (!SLUG) {
  console.error("usage: node scripts/dev-probe.mjs <space-slug>");
  process.exit(1);
}

const sleep = (ms) => new Promise((r) => setTimeout(r, ms));

function uuidv7() {
  const bytes = randomBytes(16);
  const ms = Date.now();
  bytes[0] = (ms / 2 ** 40) & 0xff;
  bytes[1] = (ms / 2 ** 32) & 0xff;
  bytes[2] = (ms / 2 ** 24) & 0xff;
  bytes[3] = (ms / 2 ** 16) & 0xff;
  bytes[4] = (ms / 2 ** 8) & 0xff;
  bytes[5] = ms & 0xff;
  bytes[6] = (bytes[6] & 0x0f) | 0x70;
  bytes[8] = (bytes[8] & 0x3f) | 0x80;
  const hex = bytes.toString("hex");
  return `${hex.slice(0, 8)}-${hex.slice(8, 12)}-${hex.slice(12, 16)}-${hex.slice(16, 20)}-${hex.slice(20)}`;
}

async function api(method, path, { cookie, body } = {}) {
  const headers = { "idempotency-key": uuidv7() };
  if (cookie) headers.cookie = cookie;
  if (body !== undefined) headers["content-type"] = "application/json";
  const response = await fetch(new URL(path, SERVER), {
    method,
    headers,
    body: body !== undefined ? JSON.stringify(body) : undefined,
  });
  return response;
}

async function main() {
  const login = await api("POST", "/api/v1/auth/login", {
    body: { email: OWNER_EMAIL, password: OWNER_PASSWORD },
  });
  if (!login.ok) throw new Error(`login failed: ${login.status}`);
  const cookie = login.headers.get("set-cookie").split(";")[0];

  const spaceRes = await api("GET", `/api/v1/spaces/by-slug/${SLUG}`, { cookie });
  if (!spaceRes.ok) throw new Error(`space lookup failed: ${spaceRes.status}`);
  const space = await spaceRes.json();
  const channelId = space.general_channel_id;

  const agentsRes = await api("GET", `/api/v1/spaces/${space.id}/agents`, { cookie });
  const agents = await agentsRes.json();
  const sol = agents.find((a) => a.handle === "sol" || a.name === "Sol");
  if (!sol) throw new Error(`@sol not found; agents: ${JSON.stringify(agents)}`);
  const agentMemberId = sol.member_id;
  console.log(`[probe] posting mention to @sol (member ${agentMemberId}) in channel ${channelId}`);

  const post = await api("POST", `/api/v1/channels/${channelId}/messages`, {
    cookie,
    body: {
      body_markdown: "@sol Reply with a single short sentence to confirm you are alive.",
      mentions: [agentMemberId],
      attachment_ids: [],
    },
  });
  if (post.status !== 201) {
    throw new Error(`post message failed: ${post.status} ${await post.text()}`);
  }
  const posted = await post.json();
  console.log(`[probe] message posted (id ${posted.id}); waiting for @sol reply...`);

  const deadline = Date.now() + 120_000;
  let lastCount = 0;
  while (Date.now() < deadline) {
    const listRes = await api("GET", `/api/v1/channels/${channelId}/messages`, { cookie });
    if (listRes.ok) {
      const payload = await listRes.json();
      const messages = Array.isArray(payload) ? payload : payload.messages ?? payload.items ?? [];
      if (messages.length !== lastCount) {
        lastCount = messages.length;
      }
      const agentReply = messages.find(
        (m) => m.author?.kind === "agent" || m.author_kind === "agent",
      );
      if (agentReply) {
        console.log("[probe] ✅ @sol replied:");
        console.log("  " + (agentReply.body_markdown ?? JSON.stringify(agentReply)).replace(/\n/g, "\n  "));
        process.exit(0);
      }
    }
    await sleep(2000);
  }
  console.error("[probe] ❌ no agent reply within 120s");
  process.exit(2);
}

main().catch((error) => {
  console.error("[probe] failed:", error.message);
  process.exit(1);
});
