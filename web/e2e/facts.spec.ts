import { expect, test, type Page } from "@playwright/test";
import { createClient, type Interceptor } from "@connectrpc/connect";
import { createConnectTransport } from "@connectrpc/connect-web";
import { randomUUID } from "node:crypto";
import { execFile } from "node:child_process";
import { constants } from "node:fs";
import { chmod, mkdtemp, mkdir, open, rm, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { fileURLToPath } from "node:url";
import { promisify } from "node:util";
import { AgentService, Driver } from "../src/gen/sumi/agent/v1/agent_pb";
import {
  Architecture,
  ComputerService,
  OperatingSystem,
} from "../src/gen/sumi/computer/v1/computer_pb";
import {
  AcknowledgementResult,
  PlacementService,
} from "../src/gen/sumi/placement/v1/placement_pb";

const baseURL = process.env.PLAYWRIGHT_BASE_URL ?? "http://127.0.0.1:8080";
const repoRoot = fileURLToPath(new URL("../..", import.meta.url));
const run = promisify(execFile);
const transport = createConnectTransport({ baseUrl: baseURL });
let ownerCredential = "";
let ownerKeyFile = "";
const ownerAuthorization: Interceptor = (next) => async (request) => {
  if (!ownerCredential) throw new Error("Owner credential is not available");
  request.header.set("Authorization", `Bearer ${ownerCredential}`);
  return next(request);
};
const ownerTransport = createConnectTransport({
  baseUrl: baseURL,
  interceptors: [ownerAuthorization],
});
const ownerAgentClient = createClient(AgentService, ownerTransport);
const ownerPlacementClient = createClient(PlacementService, ownerTransport);
const computerClient = createClient(ComputerService, transport);
const placementClient = createClient(PlacementService, transport);

type Seed = Awaited<ReturnType<typeof seedFacts>>;
let seed: Seed;

test.describe.configure({ mode: "serial" });
test.use({ trace: "off", video: "off", screenshot: "only-on-failure" });

test.beforeAll(async () => {
  const owner = await readOwnerCredential();
  ownerCredential = owner.credential;
  ownerKeyFile = owner.path;
  seed = await seedFacts();
});

test.beforeEach(async ({ page }) => {
  await authenticatePage(page);
});

test.afterAll(async () => {
  ownerCredential = "";
  ownerKeyFile = "";
  if (seed) await rm(seed.hostRoot, { recursive: true, force: true });
});

test("real facts move from pending to active after sumi-computer ack", async ({
  page,
}) => {
  await page.setViewportSize({ width: 1440, height: 900 });
  await page.goto("/");
  await openAgent(page, seed.pendingAgentName);
  await expect(agentRegion(page).locator(".state-chip.pending")).toBeVisible();

  await runHost(seed);
  await page.getByRole("button", { name: "Refresh Agents" }).click();

  await expect(agentRegion(page).locator(".state-chip.active")).toBeVisible();
  await expect(agentRegion(page)).not.toContainText(seed.registrationKey);
  await expect(page.locator("body")).not.toContainText(seed.hostRoot);
});

test("Agent management stays read-only with browser Human auth", async ({
  page,
}) => {
  await page.setViewportSize({ width: 1440, height: 900 });
  await page.goto("/");
  await page.getByRole("button", { name: "Agents" }).click();
  const navigation = agentsNavigation(page);
  await expect(navigation.getByText(seed.pendingAgentName)).toBeVisible();
  await expect(
    navigation.getByRole("button", { name: "Create Agent" }),
  ).toHaveCount(0);
  await openAgent(page, seed.pendingAgentName);
  await expect(agentRegion(page).getByText(seed.computerName)).toBeVisible();
  await expect(
    agentRegion(page).getByText(
      "Placement changes are not available in this read-only view.",
    ),
  ).toBeVisible();
  await expect(
    agentRegion(page).getByRole("button", { name: /placement/i }),
  ).toHaveCount(0);
});

for (const viewport of [
  { width: 1440, height: 900 },
  { width: 1024, height: 768 },
  { width: 900, height: 700 },
]) {
  test(`management views remain usable at ${viewport.width}px`, async ({
    page,
  }) => {
    await page.setViewportSize(viewport);
    await page.goto("/");
    await expect(page.getByText("No conversations yet")).toBeVisible();
    await page.getByRole("button", { name: "Agents" }).focus();
    await page.keyboard.press("Enter");
    const agentNav = agentsNavigation(page);
    await expect(agentNav.getByText(seed.failedAgentName)).toBeVisible();
    await agentNav
      .getByRole("button", { name: new RegExp(seed.failedAgentName) })
      .focus();
    await page.keyboard.press("Enter");
    await expect(
      agentRegion(page).getByText("workspace_io_error"),
    ).toBeVisible();
    await expect(agentRegion(page)).not.toContainText(seed.registrationKey);
    await page.getByRole("button", { name: "Computers" }).click();
    const computerNav = computersNavigation(page);
    await expect(computerNav.getByText(seed.computerName)).toBeVisible();
    await computerNav
      .getByRole("button", { name: new RegExp(seed.computerName) })
      .focus();
    await page.keyboard.press("Enter");
    await expect(
      computerRegion(page).getByText("Registered Computer"),
    ).toBeVisible();
    await expect(
      computerRegion(page).getByText(seed.pendingAgentName),
    ).toBeVisible();
    const overflow = await page.evaluate(
      () =>
        document.documentElement.scrollWidth >
        document.documentElement.clientWidth,
    );
    expect(overflow).toBe(false);
  });
}

test("browser handoff reaches Collaboration and logout revokes it immediately", async ({
  page,
}) => {
  await page.goto("/");
  await expect(page.getByText("No conversations yet")).toBeVisible();
  expect(await collaborationStatus(page)).toBe(200);

  await page.getByRole("button", { name: "Log out" }).click();
  await expect(page.getByText("Authentication required")).toBeVisible();
  expect(await collaborationStatus(page)).toBe(401);

  await page.getByRole("button", { name: "Agents" }).click();
  await expect(
    agentsNavigation(page).getByText(seed.pendingAgentName),
  ).toBeVisible();
});

async function seedFacts() {
  const suffix = randomUUID().slice(0, 8);
  const registrationKey = `${randomUUID()}${randomUUID()}`;
  const computerName = `e2e-host-${suffix}`;
  const registered = await computerClient.registerComputer({
    registrationKey,
    name: computerName,
    os: OperatingSystem.MACOS,
    arch: Architecture.ARM64,
  });
  if (!registered.computer) throw new Error("Computer seed failed");
  const pendingAgentName = `pending-${suffix}`;
  const pendingAgent = await createSeedAgent(pendingAgentName);
  await ownerPlacementClient.setAgentPlacement({
    requestId: randomUUID(),
    agentId: pendingAgent.id,
    computerId: registered.computer.id,
  });

  const failedAgentName = `failed-${suffix}`;
  const failedAgent = await createSeedAgent(failedAgentName);
  const failedPlacement = await ownerPlacementClient.setAgentPlacement({
    requestId: randomUUID(),
    agentId: failedAgent.id,
    computerId: registered.computer.id,
  });
  if (!failedPlacement.placement)
    throw new Error("Failed placement seed failed");
  await placementClient.acknowledgeAgentPlacement({
    computerId: registered.computer.id,
    registrationKey,
    agentId: failedAgent.id,
    generation: failedPlacement.placement.generation,
    result: AcknowledgementResult.FAILED,
    errorCode: "workspace_io_error",
  });

  const hostRoot = await mkdtemp(join(tmpdir(), "sumi-web-e2e-"));
  const keyFile = join(hostRoot, "computer.key");
  await writeFile(keyFile, registrationKey, { mode: 0o600 });
  await chmod(keyFile, 0o600);
  await mkdir(join(hostRoot, "data-root"), { recursive: true, mode: 0o700 });

  return {
    registrationKey,
    computerId: registered.computer.id,
    computerName,
    pendingAgentName,
    failedAgentName,
    hostRoot,
    keyFile,
  };
}

async function createSeedAgent(name: string) {
  const response = await ownerAgentClient.createAgent({
    requestId: randomUUID(),
    name,
    description: `Production Web seed for ${name}`,
    driver: Driver.CODEX,
  });
  if (!response.agent) throw new Error("Agent seed failed");
  return response.agent;
}

async function readOwnerCredential() {
  if (!process.env.PLAYWRIGHT_OWNER_KEY_FILE) {
    throw new Error("PLAYWRIGHT_OWNER_KEY_FILE is required");
  }
  const handle = await open(
    process.env.PLAYWRIGHT_OWNER_KEY_FILE,
    constants.O_RDONLY | constants.O_NOFOLLOW,
  );
  try {
    const info = await handle.stat();
    if (!info.isFile() || (info.mode & 0o777) !== 0o600) {
      throw new Error("PLAYWRIGHT_OWNER_KEY_FILE must be a regular 0600 file");
    }
    const credential = await handle.readFile({ encoding: "utf8" });
    if (!/^[A-Za-z0-9_-]{43,128}$/.test(credential)) {
      throw new Error(
        "PLAYWRIGHT_OWNER_KEY_FILE must contain one high-entropy credential",
      );
    }
    return {
      credential,
      path: process.env.PLAYWRIGHT_OWNER_KEY_FILE,
    };
  } finally {
    await handle.close();
  }
}

async function authenticatePage(page: Page) {
  let stdout = "";
  try {
    const result = await run(
      "mise",
      [
        "exec",
        "--",
        "go",
        "run",
        "./cmd/sumi-server",
        "auth",
        "--server",
        baseURL,
        "--human-key-file",
        ownerKeyFile,
      ],
      { cwd: repoRoot },
    );
    stdout = result.stdout.trim();
  } catch {
    throw new Error("Browser handoff CLI failed");
  }
  const handoffPattern = new RegExp(
    `^${escapePattern(baseURL)}/auth/browser-handoffs/[A-Za-z0-9_-]{43}$`,
  );
  if (!handoffPattern.test(stdout)) {
    throw new Error("Browser handoff CLI returned an unsafe URL");
  }
  try {
    await page.goto(stdout);
  } catch {
    throw new Error("Browser handoff navigation failed");
  }
  if (page.url() !== `${baseURL}/`) {
    throw new Error("Browser handoff did not clear its one-time URL");
  }
}

async function collaborationStatus(page: Page) {
  return page.evaluate(async () => {
    const response = await fetch(
      "/sumi.space.v1.CollaborationService/ListSpaces",
      {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: "{}",
      },
    );
    return response.status;
  });
}

function escapePattern(value: string) {
  return value.replace(/[.*+?^${}()|[\]\\]/g, "\\$&");
}

async function runHost(current: Seed) {
  await run(
    "mise",
    [
      "exec",
      "--",
      "go",
      "run",
      "./cmd/sumi-computer",
      "--server",
      baseURL,
      "--data-root",
      join(current.hostRoot, "data-root"),
      "--registration-key-file",
      current.keyFile,
      "--name",
      current.computerName,
      "--once",
    ],
    { cwd: repoRoot },
  );
}

async function openAgent(page: Page, name: string) {
  await page.getByRole("button", { name: "Agents", exact: true }).click();
  const navigation = agentsNavigation(page);
  await navigation.getByRole("button", { name: new RegExp(name) }).click();
  await expect(
    agentRegion(page).getByRole("heading", { name, level: 2 }),
  ).toBeVisible();
}

function agentsNavigation(page: Page) {
  return page.getByRole("complementary", { name: "Agents navigation" });
}

function computersNavigation(page: Page) {
  return page.getByRole("complementary", { name: "Computers navigation" });
}

function agentRegion(page: Page) {
  return page.getByRole("region", { name: "Agents" });
}

function computerRegion(page: Page) {
  return page.getByRole("region", { name: "Computers" });
}
