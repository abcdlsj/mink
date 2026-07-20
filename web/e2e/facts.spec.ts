import { expect, test, type Page } from "@playwright/test";
import { createClient } from "@connectrpc/connect";
import { createConnectTransport } from "@connectrpc/connect-web";
import { randomUUID } from "node:crypto";
import { execFile } from "node:child_process";
import { chmod, mkdtemp, mkdir, rm, writeFile } from "node:fs/promises";
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
const agentClient = createClient(AgentService, transport);
const computerClient = createClient(ComputerService, transport);
const placementClient = createClient(PlacementService, transport);

type Seed = Awaited<ReturnType<typeof seedFacts>>;
let seed: Seed;

test.describe.configure({ mode: "serial" });

test.beforeAll(async () => {
  seed = await seedFacts();
});

test.afterAll(async () => {
  await rm(seed.hostRoot, { recursive: true, force: true });
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

test("Create Agent keeps identity separate from optional placement", async ({
  page,
}) => {
  const name = `ui-${randomUUID().slice(0, 8)}`;
  await page.setViewportSize({ width: 1440, height: 900 });
  await page.goto("/");
  await page.getByRole("button", { name: "Agents" }).click();
  const navigation = agentsNavigation(page);
  await expect(navigation.getByText(seed.pendingAgentName)).toBeVisible();
  await navigation.getByRole("button", { name: "Create Agent" }).click();

  await page.getByLabel(/^Name/).fill(name);
  await page
    .getByLabel(/^Description/)
    .fill("Created through the production Web flow");
  await page.getByLabel(/^Driver/).selectOption(String(Driver.CODEX));
  await page.getByLabel(/^Optional Computer/).selectOption(seed.computerId);
  await agentRegion(page).getByRole("button", { name: "Create Agent" }).focus();
  await page.keyboard.press("Enter");

  await expect(agentRegion(page).getByText("Durable identity")).toBeVisible();
  await expect(
    agentRegion(page).getByRole("heading", { name, level: 2 }),
  ).toBeVisible();
  await expect(agentRegion(page).locator(".state-chip.pending")).toBeVisible();
  const persisted = await agentClient.listAgents({});
  expect(persisted.agents.some((agent) => agent.name === name)).toBe(true);
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
    await screenshot(page, `conversation-${viewport.width}`);

    await page.getByRole("button", { name: "Agents" }).focus();
    await page.keyboard.press("Enter");
    const agentNav = agentsNavigation(page);
    await expect(agentNav.getByText(seed.failedAgentName)).toBeVisible();
    await screenshot(page, `agents-list-${viewport.width}`);

    await agentNav
      .getByRole("button", { name: new RegExp(seed.failedAgentName) })
      .focus();
    await page.keyboard.press("Enter");
    await expect(
      agentRegion(page).getByText("workspace_io_error"),
    ).toBeVisible();
    await expect(agentRegion(page)).not.toContainText(seed.registrationKey);
    await screenshot(page, `agents-detail-${viewport.width}`);

    if (viewport.width < 1024) {
      await page.getByRole("button", { name: "Back to Agents list" }).click();
    }
    await agentsNavigation(page)
      .getByRole("button", { name: "Create Agent" })
      .click();
    await expect(
      page.getByRole("heading", { name: "New Agent identity" }),
    ).toBeVisible();
    await screenshot(page, `agents-create-${viewport.width}`);

    await agentRegion(page).getByRole("button", { name: "Cancel" }).focus();
    await page.keyboard.press("Enter");

    await page.getByRole("button", { name: "Computers" }).click();
    const computerNav = computersNavigation(page);
    await expect(computerNav.getByText(seed.computerName)).toBeVisible();
    await screenshot(page, `computers-list-${viewport.width}`);

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
    await screenshot(page, `computers-detail-${viewport.width}`);

    const overflow = await page.evaluate(
      () =>
        document.documentElement.scrollWidth >
        document.documentElement.clientWidth,
    );
    expect(overflow).toBe(false);
  });
}

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
  await placementClient.setAgentPlacement({
    agentId: pendingAgent.id,
    computerId: registered.computer.id,
  });

  const failedAgentName = `failed-${suffix}`;
  const failedAgent = await createSeedAgent(failedAgentName);
  const failedPlacement = await placementClient.setAgentPlacement({
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
  const response = await agentClient.createAgent({
    requestId: randomUUID(),
    name,
    description: `Production Web seed for ${name}`,
    driver: Driver.CODEX,
  });
  if (!response.agent) throw new Error("Agent seed failed");
  return response.agent;
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
  await page.getByRole("button", { name: "Agents" }).click();
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

async function screenshot(page: Page, name: string) {
  await settleMotion(page);
  await page.screenshot({
    path: `../test-results/task12-${name}.png`,
    fullPage: true,
  });
}

async function settleMotion(page: Page) {
  await page.evaluate(async () => {
    await Promise.all(
      document
        .getAnimations()
        .map((animation) => animation.finished.catch(() => {})),
    );
  });
}
