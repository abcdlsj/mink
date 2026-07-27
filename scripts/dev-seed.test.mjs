import assert from "node:assert/strict";
import { chmodSync, existsSync, mkdtempSync, readFileSync, rmSync, statSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import test from "node:test";
import { fileURLToPath } from "node:url";

import {
  AGENT_PROFILES,
  DEV_CHANNEL_SLUG,
  DEV_SPACE,
  createBrowserSessionHandoff,
  prepareComputerStateDirectory,
  prepareComputerStateForSpace,
} from "./dev-seed.mjs";

test("dev-seed provisions its PostgreSQL database before starting the server", () => {
  const repositoryRoot = fileURLToPath(new URL("..", import.meta.url));
  const miseConfig = readFileSync(join(repositoryRoot, "mise.toml"), "utf8");
  const task = miseConfig.match(/\[tasks\.dev-seed\]\n(?<body>(?:[^\[]|\[(?!tasks\.))*?)(?=\n\[|$)/)?.groups?.body;

  assert.ok(task, "mise.toml must define tasks.dev-seed");
  assert.match(task, /^depends = \["db-start"\]$/m);
});

test("development seed defines one stable Space and PM/Coder/Reviewer group", () => {
  assert.deepEqual(DEV_SPACE, { name: "Sumi Dev Lab", slug: "sumi-dev", accent: "#5065D8" });
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

test("development seed preserves stale Computer state when the database was recreated", (context) => {
  const parent = mkdtempSync(join(tmpdir(), "sumi-dev-seed-test-"));
  context.after(() => rmSync(parent, { recursive: true, force: true }));
  const stateDir = join(parent, "computer");
  prepareComputerStateDirectory(stateDir);
  writeFileSync(join(stateDir, "secrets.json"), JSON.stringify({ computer_id: "old-computer", space_id: "old-space" }));

  const result = prepareComputerStateForSpace(stateDir, "new-space", 1234);

  assert.equal(result.pairedIdentity, undefined);
  assert.equal(result.archivedStateDir, `${stateDir}.stale-1234`);
  assert.ok(existsSync(join(result.archivedStateDir, "secrets.json")));
  assert.ok(existsSync(stateDir));
  assert.equal(statSync(stateDir).mode & 0o777, 0o700);
  assert.equal(existsSync(join(stateDir, "secrets.json")), false);
});

test("development seed rejects a Computer state path too long for a macOS Unix socket", { skip: process.platform !== "darwin" }, () => {
  const stateDir = join(tmpdir(), "x".repeat(110));
  assert.throws(
    () => prepareComputerStateDirectory(stateDir),
    /too long for a macOS Unix socket.*SUMI_SEED_STATE_DIR/,
  );
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
