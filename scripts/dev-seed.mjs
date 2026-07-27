#!/usr/bin/env node
// Dev-only seed: drives the same HTTP flow the integration tests use to leave a
// running server in a ready-to-chat state — a registered owner, a Space, a
// paired Computer (codex driver, reusing the local ~/.codex login), and an
// Agent already joined to #general. Enable with SUMI_DEV_SEED=1 (see mise task).
//
// This never touches production: it only talks to a local Sumi server and
// spawns a local Computer daemon that reads your existing codex credentials.

import { spawn } from "node:child_process";
import { randomBytes, randomUUID } from "node:crypto";
import { mkdtempSync, writeFileSync } from "node:fs";
import { createServer } from "node:http";
import { tmpdir, homedir } from "node:os";
import { join } from "node:path";
import { pathToFileURL } from "node:url";
import readline from "node:readline";

const SERVER = process.env.SUMI_SEED_SERVER ?? "http://127.0.0.1:3000";
const CODEX_HOME = process.env.SUMI_SEED_CODEX_HOME ?? join(homedir(), ".codex");
const OWNER_EMAIL = process.env.SUMI_SEED_EMAIL ?? "dev@example.test";
const OWNER_PASSWORD = process.env.SUMI_SEED_PASSWORD ?? "correct horse battery staple";
const SEED_MARKER = "[dev-seed]";

const log = (...args) => console.log(SEED_MARKER, ...args);
const sleep = (ms) => new Promise((resolve) => setTimeout(resolve, ms));

// The server requires every Idempotency-Key to be a UUIDv7 (time-ordered).
// Node's crypto.randomUUID() only emits v4, so build v7 by hand: 48-bit
// millisecond timestamp + version/variant bits + random tail.
function uuidv7() {
  const bytes = randomBytes(16);
  const ms = Date.now();
  bytes[0] = (ms / 2 ** 40) & 0xff;
  bytes[1] = (ms / 2 ** 32) & 0xff;
  bytes[2] = (ms / 2 ** 24) & 0xff;
  bytes[3] = (ms / 2 ** 16) & 0xff;
  bytes[4] = (ms / 2 ** 8) & 0xff;
  bytes[5] = ms & 0xff;
  bytes[6] = (bytes[6] & 0x0f) | 0x70; // version 7
  bytes[8] = (bytes[8] & 0x3f) | 0x80; // variant 10
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

async function waitForHealth() {
  const deadline = Date.now() + 30_000;
  while (Date.now() < deadline) {
    try {
      const response = await fetch(new URL("/api/v1/health", SERVER));
      if (response.ok) return;
    } catch {
      // Server not up yet; keep polling.
    }
    await sleep(300);
  }
  throw new Error("Sumi server did not become healthy within 30s");
}

function sessionCookie(response) {
  const raw = response.headers.get("set-cookie");
  if (!raw) throw new Error("auth response did not set a session cookie");
  return raw.split(";")[0];
}

// A Session created by Node belongs to Node's HTTP client, not to the user's
// browser. Hand it to the browser through a loopback-only authenticated redirect.
// Cookies are scoped by host rather than port, so the Cookie set here is sent
// to the Vite dev server on 127.0.0.1:5173 after the redirect.
export async function createBrowserSessionHandoff(cookie, destination) {
  const handoffToken = randomBytes(32).toString("base64url");
  const server = createServer((request, response) => {
    const requestUrl = new URL(request.url ?? "/", "http://127.0.0.1");
    if (request.method !== "GET" || requestUrl.searchParams.get("token") !== handoffToken) {
      response.writeHead(404).end("Not found");
      return;
    }
    response.writeHead(302, {
      "Cache-Control": "no-store",
      Location: destination,
      "Set-Cookie": `${cookie}; Path=/; HttpOnly; SameSite=Lax`,
    });
    response.end();
  });
  await new Promise((resolve, reject) => {
    server.once("error", reject);
    server.listen(0, "127.0.0.1", resolve);
  });
  const address = server.address();
  if (!address || typeof address === "string") {
    server.close();
    throw new Error("could not bind browser Session handoff server");
  }
  return {
    url: `http://127.0.0.1:${address.port}/?token=${encodeURIComponent(handoffToken)}`,
    close: () => new Promise((resolve, reject) => {
      server.close((error) => (error ? reject(error) : resolve()));
    }),
  };
}

// Register the owner, or log in if a previous seed already created them. This
// keeps `mise run dev-seed` idempotent across restarts against the same DB.
async function ensureOwner() {
  const registered = await api("POST", "/api/v1/auth/register", {
    body: { display_name: "Sumi Dev", email: OWNER_EMAIL, password: OWNER_PASSWORD },
  });
  if (registered.status === 201) {
    log(`registered owner ${OWNER_EMAIL}`);
    return sessionCookie(registered);
  }
  const loggedIn = await api("POST", "/api/v1/auth/login", {
    body: { email: OWNER_EMAIL, password: OWNER_PASSWORD },
  });
  if (loggedIn.ok) {
    log(`owner ${OWNER_EMAIL} already exists — logged in`);
    return sessionCookie(loggedIn);
  }
  throw new Error(`could not register or log in owner: ${registered.status}/${loggedIn.status}`);
}

async function createSpace(cookie) {
  const slug = `dev-${randomUUID().slice(0, 8)}`;
  const response = await api("POST", "/api/v1/spaces", {
    cookie,
    body: { name: "Sumi Dev Lab", slug, accent: "#5065D8" },
  });
  if (response.status !== 201) {
    throw new Error(`create space failed: ${response.status} ${await response.text()}`);
  }
  const space = await response.json();
  log(`created space "${space.name ?? slug}" (${slug})`);
  return { ...space, slug };
}

// Spawn a Computer daemon wired to the local codex login and capture the
// pairing URL it prints to stderr. Returns { child, pairingUrl }.
//
// The daemon is driven by a TOML config file via `--config` (the same shape the
// integration tests use), NOT env vars — figment's SUMI_ env layer does not map
// cleanly onto `computer.state_dir`, so an env-only attempt silently falls back
// to the default state dir and reads whatever stale secrets.json lives there.
function spawnCodexDaemon(stateDir) {
  const configPath = join(stateDir, "computer.toml");
  writeFileSync(
    configPath,
    [
      "[computer]",
      `server_url = '${SERVER}'`,
      `state_dir = '${stateDir}'`,
      "open_pairing_browser = false",
      `codex_config_source = '${join(CODEX_HOME, "config.toml")}'`,
      `codex_auth_source = '${join(CODEX_HOME, "auth.json")}'`,
      "shutdown_grace_period_seconds = 1",
      "",
    ].join("\n"),
  );
  const child = spawn(
    "cargo",
    ["run", "--quiet", "--", "computer", "--config", configPath],
    {
      env: process.env,
      stdio: ["ignore", "inherit", "pipe"],
    },
  );
  const pairingUrl = new Promise((resolve, reject) => {
    const rl = readline.createInterface({ input: child.stderr });
    const timer = setTimeout(() => reject(new Error("timed out waiting for pairing URL")), 90_000);
    rl.on("line", (line) => {
      process.stderr.write(`  [daemon] ${line}\n`);
      if (!line.includes("Open this URL to pair the Computer")) return;
      // tracing emits ANSI colour codes even over a pipe; strip them, then take
      // the first whitespace-delimited token after `url=` (mirrors the tests).
      // eslint-disable-next-line no-control-regex
      const clean = line.replace(/\[[0-9;]*m/g, "");
      const raw = clean.split("url=")[1]?.split(/\s+/)[0]?.replace(/"/g, "");
      if (raw) {
        clearTimeout(timer);
        resolve(raw);
      }
    });
    child.on("exit", (code) => reject(new Error(`daemon exited early (code ${code})`)));
  });
  return { child, pairingUrl };
}

async function confirmPairing(cookie, spaceId, pairingUrl) {
  const url = new URL(pairingUrl);
  const segments = url.pathname.split("/").filter(Boolean);
  const pairingId = segments[segments.length - 1];
  const code = url.searchParams.get("code");
  if (!pairingId || !code) throw new Error(`malformed pairing URL: ${pairingUrl}`);
  const response = await api("POST", `/api/v1/computer-pairings/${pairingId}/confirm`, {
    cookie,
    body: { space_id: spaceId, name: "Dev Computer", code },
  });
  if (response.status !== 201) {
    throw new Error(`confirm pairing failed: ${response.status} ${await response.text()}`);
  }
  return response.json();
}

async function waitForComputerOnline(cookie, spaceId) {
  const deadline = Date.now() + 60_000;
  while (Date.now() < deadline) {
    const response = await api("GET", `/api/v1/spaces/${spaceId}/computers`, { cookie });
    if (response.ok) {
      const online = (await response.json()).find((c) => c.status === "online");
      if (online) return online;
    }
    await sleep(500);
  }
  throw new Error("Computer did not come online within 60s");
}

async function createAgent(cookie, spaceId, computerId) {
  const response = await api("POST", `/api/v1/spaces/${spaceId}/agents`, {
    cookie,
    body: {
      computer_id: computerId,
      name: "Sol",
      handle: "sol",
      role_text: "You are Sol, a helpful pair-programming agent in this dev space. Answer concisely.",
      access_level: "member",
      driver_kind: "codex",
    },
  });
  if (response.status !== 201) {
    throw new Error(`create agent failed: ${response.status} ${await response.text()}`);
  }
  return response.json();
}

async function addAgentToChannel(cookie, channelId, agentMemberId) {
  const response = await api("POST", `/api/v1/channels/${channelId}/members`, {
    cookie,
    body: { agent_member_ids: [agentMemberId] },
  });
  if (!response.ok) {
    throw new Error(`add agent to channel failed: ${response.status} ${await response.text()}`);
  }
}

async function main() {
  log("waiting for server health...");
  await waitForHealth();

  const cookie = await ensureOwner();
  const space = await createSpace(cookie);

  const stateDir = mkdtempSync(join(tmpdir(), "sumi-dev-computer-"));
  log(`spawning codex Computer daemon (state: ${stateDir})`);
  const { child, pairingUrl } = spawnCodexDaemon(stateDir);
  process.on("exit", () => child.kill("SIGINT"));

  const resolvedPairingUrl = await pairingUrl;
  log("pairing URL captured — confirming");
  const computer = await confirmPairing(cookie, space.id, resolvedPairingUrl);
  const online = await waitForComputerOnline(cookie, space.id);
  log(`Computer "${online.name ?? "Dev Computer"}" is online`);

  const agent = await createAgent(cookie, space.id, computer.id);
  const agentMemberId = agent.member_id;
  await addAgentToChannel(cookie, space.general_channel_id, agentMemberId);
  log(`agent @sol created and joined #general`);
  const channelUrl = `${SERVER.replace(/:\d+$/, ":5173")}/s/${space.slug}/channels/general`;
  const browserHandoff = await createBrowserSessionHandoff(cookie, channelUrl);

  log("");
  log("READY — open this local URL to sign in and enter #general:");
  log(`  ${browserHandoff.url}`);
  log(`  destination: ${channelUrl}`);
  log(`  fallback login: ${OWNER_EMAIL} / ${OWNER_PASSWORD}`);
  log("");
  log("Keeping the Computer daemon alive so @sol can reply. Ctrl-C to stop.");

  // Keep the daemon (and this process) running for the acceptance session.
  await new Promise(() => {});
}

if (process.argv[1] && import.meta.url === pathToFileURL(process.argv[1]).href) {
  main().catch((error) => {
    console.error(SEED_MARKER, "failed:", error.message);
    process.exit(1);
  });
}
