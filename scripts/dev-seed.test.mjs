import assert from "node:assert/strict";
import { chmodSync, existsSync, mkdirSync, mkdtempSync, readFileSync, rmSync, statSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import test from "node:test";
import { fileURLToPath } from "node:url";

import {
  AGENT_PROFILES,
  CODEX_HOME,
  CODEX_COMMAND,
  DEV_CHANNEL_SLUG,
  DEV_SPACE,
  buildSeedComputerConfig,
  cleanDevelopmentSeedState,
  computerStateDirectory,
  createBrowserSessionHandoff,
  extractComputerPairingUrl,
  findComputerStateForSpace,
  prepareComputerStateDirectory,
} from "./dev-seed.mjs";

test("development seed defaults to codex and accepts a local command override", () => {
  assert.equal(typeof CODEX_COMMAND, "string");
  assert.ok(CODEX_COMMAND.length > 0);
  assert.equal(typeof CODEX_HOME, "string");
  assert.ok(CODEX_HOME.length > 0);

  const taskScript = readFileSync(new URL("./dev-seed-task.sh", import.meta.url), "utf8");
  assert.match(
    taskScript,
    /SUMI_SEED_CODEX_HOME="\$\{SUMI_SEED_CODEX_HOME:-}"[\s\S]*?SUMI_SEED_CODEX_COMMAND="\$\{SUMI_SEED_CODEX_COMMAND:-}"[\s\S]*?mise run dev/,
  );
  assert.match(taskScript, /SUMI_SEED_CODEX_COMMAND="\$1" run_dev/);
  assert.match(taskScript, /SUMI_SEED_CODEX_HOME=\/path\/to\/codex-home/);
  assert.match(readFileSync(new URL("./dev-seed.mjs", import.meta.url), "utf8"), /SUMI_CODEX_COMMAND: CODEX_COMMAND/);
  assert.match(
    readFileSync(new URL("./dev-seed.mjs", import.meta.url), "utf8"),
    /SUMI_SEED_CODEX_HOME \?\? join\(homedir\(\), "\.codex"\)/,
  );
  assert.match(
    readFileSync(new URL("./dev-seed.mjs", import.meta.url), "utf8"),
    /codexHomeFromEnv: CODEX_HOME_FROM_ENV/,
  );
});

test("development seed copies local Computer configuration with isolated runtime overrides", () => {
  const config = buildSeedComputerConfig(
    `[server]\ndatabase_url = "postgres://localhost/sumi_prod"\n\n[computer]\nserver_url = "https://sumi.example.test"\nstate_dir = "/var/lib/sumi"\nopen_pairing_browser = true\ncodex_config_source = "/custom/codex/config.toml"\n\n[computer.builtin]\napi_base = "https://api.deepseek.com"\ntoken = "test-token"\nmodel = "deepseek-v4-pro"\n`,
    { server: "http://127.0.0.1:3000", stateDir: "/tmp/sumi-seed/computer", codexHome: "/fallback/codex" },
  );

  assert.match(config, /database_url = "postgres:\/\/localhost\/sumi_prod"/);
  assert.match(config, /server_url = "http:\/\/127\.0\.0\.1:3000"/);
  assert.match(config, /state_dir = "\/tmp\/sumi-seed\/computer"/);
  assert.match(config, /open_pairing_browser = false/);
  assert.match(config, /codex_config_source = "\/custom\/codex\/config\.toml"/);
  assert.match(config, /api_base = "https:\/\/api\.deepseek\.com"/);
  assert.match(config, /token = "test-token"/);
  assert.match(config, /model = "deepseek-v4-pro"/);
  assert.match(config, /codex_auth_source = "\/fallback\/codex\/auth\.json"/);
});

test("development seed env Codex home overrides explicit base config sources", () => {
  const config = buildSeedComputerConfig(
    `[computer]\ncodex_config_source = "/custom/codex/config.toml"\ncodex_auth_source = "/custom/codex/auth.json"\n`,
    {
      server: "http://127.0.0.1:3000",
      stateDir: "/tmp/sumi-seed/computer",
      codexHome: "/env/codex",
      codexHomeFromEnv: true,
    },
  );

  assert.match(config, /codex_config_source = "\/env\/codex\/config\.toml"/);
  assert.match(config, /codex_auth_source = "\/env\/codex\/auth\.json"/);
});

test("development seed extracts the Computer pairing URL without depending on log prose", () => {
  const pairingUrl = "http://127.0.0.1:3000/pair-computer/019fa900-0000-7000-8000-000000000001/?code=test-code";

  assert.equal(
    extractComputerPairingUrl(`INFO Confirm this Computer in Sumi url=${pairingUrl} expires_at=2026-07-30T06:00:00Z`),
    pairingUrl,
  );
  assert.equal(
    extractComputerPairingUrl(`INFO Open this URL to pair the Computer url=\"${pairingUrl}\"`),
    pairingUrl,
  );
  assert.equal(extractComputerPairingUrl("INFO request completed url=http://127.0.0.1:3000/api/v1/health"), undefined);
  assert.equal(extractComputerPairingUrl("INFO Confirm this Computer in Sumi url=not-a-url"), undefined);
});

test("dev-seed provisions its PostgreSQL database and exposes an explicit clean command", () => {
  const repositoryRoot = fileURLToPath(new URL("..", import.meta.url));
  const miseConfig = readFileSync(join(repositoryRoot, "mise.toml"), "utf8");
  const taskScript = readFileSync(join(repositoryRoot, "scripts", "dev-seed-task.sh"), "utf8");
  const task = miseConfig.match(/\[tasks\.dev-seed\]\n(?<body>(?:[^\[]|\[(?!tasks\.))*?)(?=\n\[|$)/)?.groups?.body;

  assert.ok(task, "mise.toml must define tasks.dev-seed");
  assert.match(task, /run = "sh scripts\/dev-seed-task\.sh"/);
  assert.match(task, /RUST_LOG = "sumi=warn,tower_http=warn"/);
  assert.match(taskScript, /mise run db-start/);
  assert.match(taskScript, /dropdb" --if-exists --force sumi_dev/);
  assert.match(taskScript, /node scripts\/dev-seed\.mjs clean/);

  const developmentTask = miseConfig.match(/\[tasks\.dev\]\n(?<body>(?:[^\[]|\[(?!tasks\.))*?)(?=\n\[|$)/)?.groups?.body;
  assert.ok(developmentTask, "mise.toml must define tasks.dev");
  assert.match(developmentTask, /SUMI_SERVER__DATABASE_URL = "postgres:\/\/localhost\/sumi_dev"/);

  const databaseTask = miseConfig.match(/\[tasks\.db-start\]\n(?<body>(?:[^\[]|\[(?!tasks\.))*?)(?=\n\[|$)/)?.groups?.body;
  assert.ok(databaseTask, "mise.toml must define tasks.db-start");
  assert.match(databaseTask, /dropdb" --force sumi_dev/);
  assert.match(databaseTask, /schema_version=/);
  assert.match(databaseTask, /schema\/postgres\.sql/);
  assert.match(databaseTask, /installed_version.*!= "\$schema_version"/);
  assert.doesNotMatch(databaseTask, /installed_version.*!= "[0-9]+"/);
});

test("development seed cleanup removes only its resolved state root", (context) => {
  const parent = mkdtempSync(join(tmpdir(), "sumi-dev-seed-clean-test-"));
  context.after(() => rmSync(parent, { recursive: true, force: true }));
  const stateRoot = join(parent, ".sumi-dev-seed");
  const unrelated = join(parent, "keep.txt");
  mkdirSync(join(stateRoot, "computer", "space", "computer"), { recursive: true });
  writeFileSync(join(stateRoot, "computer", "computer.toml"), "[computer]\n");
  writeFileSync(join(stateRoot, "computer", "space", "computer", "daemon.db"), "stale");
  writeFileSync(unrelated, "keep");

  const removed = cleanDevelopmentSeedState(stateRoot);

  assert.equal(removed, stateRoot);
  assert.equal(existsSync(stateRoot), false);
  assert.equal(readFileSync(unrelated, "utf8"), "keep");
});

test("development seed cleanup rejects broad filesystem targets", () => {
  assert.throws(() => cleanDevelopmentSeedState("/"), /unsafe dev-seed state root/);
});

test("development seed cleanup rejects an unrelated custom directory", (context) => {
  const target = mkdtempSync(join(tmpdir(), "unrelated-clean-test-"));
  context.after(() => rmSync(target, { recursive: true, force: true }));
  writeFileSync(join(target, "keep.txt"), "keep");

  assert.throws(() => cleanDevelopmentSeedState(target), /unrecognized dev-seed state root/);
  assert.equal(readFileSync(join(target, "keep.txt"), "utf8"), "keep");
});

test("development seed defines one stable Space and PM/Coder/Reviewer group", () => {
  assert.deepEqual(DEV_SPACE, { name: "Sumi Dev", slug: "sumi-dev", accent: "#FE7DA8" });
  assert.equal(DEV_CHANNEL_SLUG, "general");
  assert.deepEqual(AGENT_PROFILES.map(({ name, driver_kind }) => ({ name, driver_kind })), [
    { name: "PM", driver_kind: "codex" },
    { name: "Coder", driver_kind: "codex" },
    { name: "Reviewer", driver_kind: "codex" },
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
