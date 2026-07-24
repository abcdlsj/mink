import { expect, test, type Page } from "@playwright/test";
import { createClient, type Interceptor } from "@connectrpc/connect";
import { createConnectTransport } from "@connectrpc/connect-web";
import { randomBytes, randomUUID } from "node:crypto";
import { execFile } from "node:child_process";
import { constants } from "node:fs";
import { chmod, mkdtemp, mkdir, open, rm, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { fileURLToPath } from "node:url";
import { promisify } from "node:util";
import { AgentService, Driver } from "../src/gen/sumi/agent/v1/agent_pb";
import { ComputerService } from "../src/gen/sumi/computer/v1/computer_pb";
import { PlacementService } from "../src/gen/sumi/placement/v1/placement_pb";

const baseURL = process.env.PLAYWRIGHT_BASE_URL ?? "http://127.0.0.1:8080";
const repoRoot = fileURLToPath(new URL("../..", import.meta.url));
const run = promisify(execFile);
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
const ownerComputerClient = createClient(ComputerService, ownerTransport);
const ownerPlacementClient = createClient(PlacementService, ownerTransport);

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
  await expect(agentRegion(page)).not.toContainText(seed.pairingToken);
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
    await expect(page.getByText("No conversation selected")).toBeVisible();
    await page.getByRole("button", { name: "Agents" }).focus();
    await page.keyboard.press("Enter");
    const agentNav = agentsNavigation(page);
    await expect(agentNav.getByText(seed.failedAgentName)).toBeVisible();
    await agentNav
      .getByRole("button", { name: new RegExp(seed.failedAgentName) })
      .focus();
    await page.keyboard.press("Enter");
    await expect(
      agentRegion(page).getByText("workspace_invalid"),
    ).toBeVisible();
    await expect(agentRegion(page)).not.toContainText(seed.pairingToken);
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
  await expect(page.getByText("No conversation selected")).toBeVisible();
  expect(await collaborationStatus(page)).toBe(200);

  await page.getByRole("button", { name: "Log out" }).click();
  await expect(
    page.getByRole("heading", {
      name: /Create your local account|Welcome back/,
    }),
  ).toBeVisible();
  expect(await collaborationStatus(page)).toBe(401);

  await page.getByRole("button", { name: "Agents" }).click();
  await expect(
    agentsNavigation(page).getByText(seed.pendingAgentName),
  ).toBeVisible();
});

async function seedFacts() {
  const suffix = randomUUID().slice(0, 8);
  const pairingToken = randomBytes(32).toString("base64url");
  const computerName = `e2e-host-${suffix}`;
  await ownerComputerClient.createComputerPairing({
    requestId: randomUUID(),
    pairingToken,
    expiresAt: {
      seconds: BigInt(Math.floor(Date.now() / 1000) + 600),
      nanos: 0,
    },
  });
  const hostRoot = await mkdtemp(join(tmpdir(), "sumi-web-e2e-"));
  const tokenFile = join(hostRoot, "pairing.token");
  await writeFile(tokenFile, pairingToken, { mode: 0o600 });
  await chmod(tokenFile, 0o600);
  await mkdir(join(hostRoot, "data-root"), { recursive: true, mode: 0o700 });
  await pairHost(hostRoot, tokenFile, computerName);
  const computers = await ownerComputerClient.listComputers({});
  const computer = computers.computers.find(
    (candidate) => candidate.name === computerName,
  );
  if (!computer) throw new Error("Computer seed failed");

  const failedAgentName = `failed-${suffix}`;
  const failedAgent = await createSeedAgent(failedAgentName);
  const failedPlacement = await ownerPlacementClient.setAgentPlacement({
    requestId: randomUUID(),
    agentId: failedAgent.id,
    computerId: computer.id,
  });
  if (!failedPlacement.placement)
    throw new Error("Failed placement seed failed");
  const failedAgentRoot = join(
    hostRoot,
    "data-root",
    "agents",
    `agent_${failedAgent.id}`,
  );
  const invalidWorkspace = join(failedAgentRoot, "workspace");
  await mkdir(failedAgentRoot, { recursive: true, mode: 0o700 });
  await writeFile(invalidWorkspace, "not a directory", { mode: 0o600 });
  await expectHostFailure(
    hostRoot,
    computerName,
    "path is not a real directory",
  );
  await rm(invalidWorkspace, { force: true });

  const pendingAgentName = `pending-${suffix}`;
  const pendingAgent = await createSeedAgent(pendingAgentName);
  await ownerPlacementClient.setAgentPlacement({
    requestId: randomUUID(),
    agentId: pendingAgent.id,
    computerId: computer.id,
  });

  return {
    pairingToken,
    computerId: computer.id,
    computerName,
    pendingAgentName,
    failedAgentName,
    hostRoot,
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
  await syncHost(current.hostRoot, current.computerName);
}

async function pairHost(
  hostRoot: string,
  tokenFile: string,
  computerName: string,
) {
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
      join(hostRoot, "data-root"),
      "--pairing-token-file",
      tokenFile,
      "--name",
      computerName,
      "--once",
    ],
    { cwd: repoRoot },
  );
}

async function syncHost(hostRoot: string, computerName: string) {
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
      join(hostRoot, "data-root"),
      "--name",
      computerName,
      "--once",
    ],
    { cwd: repoRoot },
  );
}

async function expectHostFailure(
  hostRoot: string,
  computerName: string,
  stderrNeedle: string,
) {
  try {
    await syncHost(hostRoot, computerName);
  } catch (error) {
    if (
      typeof error === "object" &&
      error !== null &&
      "stderr" in error &&
      String(error.stderr).includes(stderrNeedle)
    ) {
      return;
    }
    throw error;
  }
  throw new Error(`Computer seed unexpectedly accepted ${stderrNeedle}`);
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
