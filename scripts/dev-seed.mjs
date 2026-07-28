#!/usr/bin/env node
// Dev-only seed: drives the same HTTP flow the integration tests use to leave a
// running server in a ready-to-chat state — a registered owner, a Space, a
// paired Computer (codex driver, reusing the local ~/.codex login), and three
// stable Codex Agents already joined to #general. Enable with SUMI_DEV_SEED=1
// (see mise task).
//
// This never touches production: it only talks to a local Sumi server and
// spawns a local Computer daemon that reads your existing codex credentials.

import { spawn } from "node:child_process";
import { randomBytes } from "node:crypto";
import { chmodSync, existsSync, mkdirSync, readFileSync, renameSync, writeFileSync } from "node:fs";
import { createServer } from "node:http";
import { homedir } from "node:os";
import { join } from "node:path";
import { pathToFileURL } from "node:url";
import readline from "node:readline";

const SERVER = process.env.SUMI_SEED_SERVER ?? "http://127.0.0.1:3000";
const CODEX_HOME = process.env.SUMI_SEED_CODEX_HOME ?? join(homedir(), ".codex");
const OWNER_EMAIL = process.env.SUMI_SEED_EMAIL ?? "dev@example.test";
const OWNER_PASSWORD = process.env.SUMI_SEED_PASSWORD ?? "correct horse battery staple";
const SEED_MARKER = "[dev-seed]";
export const DEV_SPACE = Object.freeze({ name: "Sumi Dev Lab", slug: "sumi-dev", accent: "#FE7DA8" });
export const DEV_CHANNEL_SLUG = "general";
export const DEV_COMPUTER_STATE = process.env.SUMI_SEED_STATE_DIR ?? join(homedir(), ".sumi", "computer", "dev-seed");
const MACOS_UNIX_SOCKET_PATH_MAX_BYTES = 103;

export const AGENT_PROFILES = Object.freeze([
  Object.freeze({
    name: "PM",
    handle: "pm",
    role_text: "You are the product manager. Clarify outcomes, constrain scope, maintain priorities and acceptance criteria, and surface decisions that need a Human. Do not invent technical facts or claim implementation is complete without evidence.",
    driver_kind: "codex",
  }),
  Object.freeze({
    name: "Coder",
    handle: "coder",
    role_text: "You are the implementation owner. Diagnose root causes, write focused code, run relevant tests, and report verifiable results. Keep changes simple and never hide failures behind compatibility layers.",
    driver_kind: "codex",
  }),
  Object.freeze({
    name: "Reviewer",
    handle: "reviewer",
    role_text: "You are the independent reviewer. Inspect specifications and changes for correctness, security, regressions, and missing tests. Challenge weak evidence and do not approve work until risks are explicit.",
    driver_kind: "codex",
  }),
]);

export function prepareComputerStateDirectory(stateDir) {
  const socketPath = join(stateDir, "daemon.sock");
  const socketPathBytes = Buffer.byteLength(socketPath);
  if (process.platform === "darwin" && socketPathBytes > MACOS_UNIX_SOCKET_PATH_MAX_BYTES) {
    throw new Error(
      `Computer state path is too long for a macOS Unix socket (${socketPathBytes} bytes): ${socketPath}. Set SUMI_SEED_STATE_DIR to a shorter persistent path.`,
    );
  }
  mkdirSync(stateDir, { recursive: true, mode: 0o700 });
  chmodSync(stateDir, 0o700);
}

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

async function ensureSpace(cookie) {
  const existing = await api("GET", `/api/v1/spaces/by-slug/${DEV_SPACE.slug}`, { cookie });
  if (existing.ok) {
    const space = await existing.json();
    log(`reusing space "${space.name}" (${space.slug})`);
    return space;
  }
  if (existing.status !== 404) {
    throw new Error(`lookup space failed: ${existing.status} ${await existing.text()}`);
  }
  const response = await api("POST", "/api/v1/spaces", {
    cookie,
    body: DEV_SPACE,
  });
  if (response.status !== 201) {
    throw new Error(`create space failed: ${response.status} ${await response.text()}`);
  }
  const space = await response.json();
  log(`created space "${space.name}" (${space.slug})`);
  return space;
}

// Spawn a Computer daemon wired to the local codex login and capture the
// pairing URL it prints to stderr. Returns { child, pairingUrl }.
//
// The daemon is driven by a TOML config file via `--config` (the same shape the
// integration tests use), NOT env vars — figment's SUMI_ env layer does not map
// cleanly onto `computer.state_dir`, so an env-only attempt silently falls back
// to the default state dir and reads whatever stale secrets.json lives there.
function spawnCodexDaemon(stateDir, expectPairing) {
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
  const rl = readline.createInterface({ input: child.stderr });
  let resolvePairing;
  let rejectPairing;
  const pairingUrl = expectPairing ? new Promise((resolve, reject) => {
    resolvePairing = resolve;
    rejectPairing = reject;
  }) : undefined;
  const timer = expectPairing ? setTimeout(() => rejectPairing(new Error("timed out waiting for pairing URL")), 90_000) : undefined;
  rl.on("line", (line) => {
      process.stderr.write(`  [daemon] ${line}\n`);
      if (!expectPairing || !line.includes("Open this URL to pair the Computer")) return;
      // tracing emits ANSI colour codes even over a pipe; strip them, then take
      // the first whitespace-delimited token after `url=` (mirrors the tests).
      // eslint-disable-next-line no-control-regex
      const clean = line.replace(/\[[0-9;]*m/g, "");
      const raw = clean.split("url=")[1]?.split(/\s+/)[0]?.replace(/"/g, "");
      if (raw) {
        clearTimeout(timer);
        resolvePairing(raw);
      }
  });
  child.on("exit", (code) => {
    if (expectPairing) rejectPairing(new Error(`daemon exited early (code ${code})`));
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

async function waitForComputerOnline(cookie, spaceId, computerId) {
  const deadline = Date.now() + 60_000;
  while (Date.now() < deadline) {
    const response = await api("GET", `/api/v1/spaces/${spaceId}/computers`, { cookie });
    if (response.ok) {
      const online = (await response.json()).find((candidate) => candidate.id === computerId && candidate.status === "online");
      if (online) return online;
    }
    await sleep(500);
  }
  throw new Error("Computer did not come online within 60s");
}

function pairedComputerIdentity(stateDir) {
  const path = join(stateDir, "secrets.json");
  if (!existsSync(path)) return undefined;
  const secrets = JSON.parse(readFileSync(path, "utf8"));
  if (!secrets.computer_id || !secrets.space_id) return undefined;
  return { computerId: secrets.computer_id, spaceId: secrets.space_id };
}

export function prepareComputerStateForSpace(stateDir, spaceId, timestamp = Date.now()) {
  prepareComputerStateDirectory(stateDir);
  const pairedIdentity = pairedComputerIdentity(stateDir);
  if (!pairedIdentity || pairedIdentity.spaceId === spaceId) return { pairedIdentity };

  const archivePrefix = `${stateDir}.stale-${timestamp}`;
  let archivedStateDir = archivePrefix;
  for (let suffix = 1; existsSync(archivedStateDir); suffix += 1) {
    archivedStateDir = `${archivePrefix}-${suffix}`;
  }
  renameSync(stateDir, archivedStateDir);
  prepareComputerStateDirectory(stateDir);
  return { pairedIdentity: undefined, archivedStateDir };
}

async function createAgent(cookie, spaceId, computerId, profile) {
  const response = await api("POST", `/api/v1/spaces/${spaceId}/agents`, {
    cookie,
    body: {
      computer_id: computerId,
      name: profile.name,
      handle: profile.handle,
      role_text: profile.role_text,
      access_level: "member",
      driver_kind: profile.driver_kind,
    },
  });
  if (response.status !== 201) {
    throw new Error(`create agent failed: ${response.status} ${await response.text()}`);
  }
  return response.json();
}

async function updateAgentRole(cookie, agent, profile) {
  const response = await api("PATCH", `/api/v1/agents/${agent.member_id}`, {
    cookie,
    body: { role_text: profile.role_text },
  });
  if (!response.ok) {
    throw new Error(`update @${profile.handle} failed: ${response.status} ${await response.text()}`);
  }
  return response.json();
}

async function ensureAgents(cookie, spaceId, computerId) {
  const response = await api("GET", `/api/v1/spaces/${spaceId}/agents`, { cookie });
  if (!response.ok) {
    throw new Error(`list agents failed: ${response.status} ${await response.text()}`);
  }
  const existing = await response.json();
  const agents = [];
  for (const profile of AGENT_PROFILES) {
    const matches = existing.filter((agent) => agent.handle === profile.handle);
    if (matches.length > 1) throw new Error(`multiple active Agents use @${profile.handle}`);
    const agent = matches[0];
    if (!agent) {
      agents.push(await createAgent(cookie, spaceId, computerId, profile));
      log(`created @${profile.handle}`);
      continue;
    }
    if (agent.status === "retired") throw new Error(`seed Agent @${profile.handle} is retired`);
    if (agent.computer_id !== computerId) {
      throw new Error(`seed Agent @${profile.handle} belongs to another Computer`);
    }
    if (agent.driver_kind !== profile.driver_kind) {
      throw new Error(`seed Agent @${profile.handle} uses ${agent.driver_kind}, expected ${profile.driver_kind}`);
    }
    agents.push(agent.role_text === profile.role_text ? agent : await updateAgentRole(cookie, agent, profile));
    log(`reusing @${profile.handle}`);
  }
  return agents;
}

async function addAgentsToChannel(cookie, channelId, agentMemberIds) {
  const response = await api("POST", `/api/v1/channels/${channelId}/members`, {
    cookie,
    body: { agent_member_ids: agentMemberIds },
  });
  if (!response.ok) {
    throw new Error(`add agent to channel failed: ${response.status} ${await response.text()}`);
  }
}

async function main() {
  log("waiting for server health...");
  await waitForHealth();

  const cookie = await ensureOwner();
  const space = await ensureSpace(cookie);

  const stateDir = DEV_COMPUTER_STATE;
  const { pairedIdentity, archivedStateDir } = prepareComputerStateForSpace(stateDir, space.id);
  if (archivedStateDir) log(`archived stale Dev Computer state at ${archivedStateDir}`);
  log(`spawning codex Computer daemon (state: ${stateDir})`);
  const { child, pairingUrl } = spawnCodexDaemon(stateDir, !pairedIdentity);
  process.on("exit", () => child.kill("SIGINT"));

  let computer;
  if (pairedIdentity) {
    log("reusing paired Dev Computer identity");
    computer = { id: pairedIdentity.computerId };
  } else {
    const resolvedPairingUrl = await pairingUrl;
    log("pairing URL captured — confirming");
    computer = await confirmPairing(cookie, space.id, resolvedPairingUrl);
  }
  const online = await waitForComputerOnline(cookie, space.id, computer.id);
  log(`Computer "${online.name ?? "Dev Computer"}" is online`);

  const agents = await ensureAgents(cookie, space.id, computer.id);
  await addAgentsToChannel(cookie, space.general_channel_id, agents.map((agent) => agent.member_id));
  log(`fixed group ${AGENT_PROFILES.map((profile) => `@${profile.handle}`).join(", ")} joined #${DEV_CHANNEL_SLUG}`);
  const channelUrl = `${SERVER.replace(/:\d+$/, ":5173")}/s/${space.slug}/channels/general`;
  const browserHandoff = await createBrowserSessionHandoff(cookie, channelUrl);

  log("");
  log("READY — open this local URL to sign in and enter #general:");
  log(`  ${browserHandoff.url}`);
  log(`  destination: ${channelUrl}`);
  log(`  fallback login: ${OWNER_EMAIL} / ${OWNER_PASSWORD}`);
  log("");
  log(`Keeping the Computer daemon alive so ${AGENT_PROFILES.map((profile) => `@${profile.handle}`).join(", ")} can reply. Ctrl-C to stop.`);

  // Keep the daemon (and this process) running for the acceptance session.
  await new Promise(() => {});
}

if (process.argv[1] && import.meta.url === pathToFileURL(process.argv[1]).href) {
  main().catch((error) => {
    console.error(SEED_MARKER, "failed:", error.message);
    process.exit(1);
  });
}
