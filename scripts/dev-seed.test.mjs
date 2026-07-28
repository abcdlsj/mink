import assert from "node:assert/strict";
import { chmodSync, existsSync, mkdirSync, mkdtempSync, readFileSync, rmSync, statSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import test from "node:test";
import { fileURLToPath } from "node:url";

import {
  AGENT_PROFILES,
  DEV_CHANNEL_SLUG,
  DEV_SPACE,
  computerStateDirectory,
  createBrowserSessionHandoff,
  findComputerStateForSpace,
  prepareComputerStateDirectory,
} from "./dev-seed.mjs";

test("dev-seed provisions its PostgreSQL database before starting the server", () => {
  const repositoryRoot = fileURLToPath(new URL("..", import.meta.url));
  const miseConfig = readFileSync(join(repositoryRoot, "mise.toml"), "utf8");
  const task = miseConfig.match(/\[tasks\.dev-seed\]\n(?<body>(?:[^\[]|\[(?!tasks\.))*?)(?=\n\[|$)/)?.groups?.body;

  assert.ok(task, "mise.toml must define tasks.dev-seed");
  assert.match(task, /^depends = \["db-start"\]$/m);
  assert.match(task, /RUST_LOG = "sumi=warn,tower_http=warn"/);
});

test("development seed defines one stable Space and PM/Coder/Reviewer group", () => {
  assert.deepEqual(DEV_SPACE, { name: "Sumi Dev Lab", slug: "sumi-dev", accent: "#FE7DA8" });
  assert.equal(DEV_CHANNEL_SLUG, "general");
  assert.deepEqual(AGENT_PROFILES.map(({ name, handle, driver_kind }) => ({ name, handle, driver_kind })), [
    { name: "PM", handle: "pm", driver_kind: "codex" },
    { name: "Coder", handle: "coder", driver_kind: "codex" },
    { name: "Reviewer", handle: "reviewer", driver_kind: "codex" },
  ]);
  assert.equal(new Set(AGENT_PROFILES.map((profile) => profile.role_text)).size, 3);
  for (const profile of AGENT_PROFILES) assert.ok(profile.role_text.length > 80);
});

test("development seed enforces private Computer state permissions", (context) => {
  const parent = mkdtempSync(join(tmpdir(), "sumi-dev-seed-test-"));
  context.after(() => rmSync(parent, { recursive: true, force: true }));
  const stateDir = join(parent, "computer");

  prepareComputerStateDirectory(stateDir);
  assert.equal(statSync(stateDir).mode & 0o777, 0o700);

  chmodSync(stateDir, 0o755);
  prepareComputerStateDirectory(stateDir);
  assert.equal(statSync(stateDir).mode & 0o777, 0o700);
});

test("development seed stores Computer state under Space and Computer IDs", (context) => {
  const parent = mkdtempSync(join(tmpdir(), "sumi-dev-seed-test-"));
  context.after(() => rmSync(parent, { recursive: true, force: true }));
  const spaceId = "019fa900-0000-7000-8000-000000000001";
  const computerId = "019fa900-0000-7000-8000-000000000002";
  const stateDir = computerStateDirectory(parent, spaceId, computerId);
  mkdirSync(stateDir, { recursive: true });
  writeFileSync(join(stateDir, "secrets.json"), JSON.stringify({ computer_id: computerId, space_id: spaceId }));

  const result = findComputerStateForSpace(parent, spaceId);

  assert.equal(result.stateDir, join(parent, spaceId, computerId));
  assert.deepEqual(result.pairedIdentity, { computerId, spaceId });
});

test("development seed allows a deep Computer state path when its runtime path is short", { skip: process.platform !== "darwin" }, (context) => {
  const stateDir = join(tmpdir(), "x".repeat(110));
  context.after(() => rmSync(stateDir, { recursive: true, force: true }));
  assert.doesNotThrow(() => prepareComputerStateDirectory(stateDir));
});

test("browser Session handoff sets the Cookie and remains retryable", async (context) => {
  const destination = "http://127.0.0.1:5173/s/dev-space/channels/general";
  const handoff = await createBrowserSessionHandoff("sumi_session=test-token", destination);
  context.after(() => handoff.close());

  const response = await fetch(handoff.url, { redirect: "manual" });
  assert.equal(response.status, 302);
  assert.equal(response.headers.get("location"), destination);
  assert.match(
    response.headers.get("set-cookie") ?? "",
    /^sumi_session=test-token; Path=\/; HttpOnly; SameSite=Lax$/,
  );
  assert.equal(response.headers.get("cache-control"), "no-store");

  const replay = await fetch(handoff.url, { redirect: "manual" });
  assert.equal(replay.status, 302);
  assert.equal(replay.headers.get("location"), destination);
  assert.match(replay.headers.get("set-cookie") ?? "", /^sumi_session=test-token;/);
});
