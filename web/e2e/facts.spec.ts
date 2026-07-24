import { expect, test, type Page } from "@playwright/test";
import { createClient, type Interceptor } from "@connectrpc/connect";
import { createConnectTransport } from "@connectrpc/connect-web";
import { timestampFromDate } from "@bufbuild/protobuf/wkt";
import { randomBytes, randomUUID } from "node:crypto";
import { execFile, spawn } from "node:child_process";
import { constants } from "node:fs";
import { chmod, mkdtemp, mkdir, open, rm, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { fileURLToPath } from "node:url";
import { promisify } from "node:util";
import {
  AgentService,
  EngineKind,
  ProviderProtocol,
  RuntimeSandboxProvider,
  type Agent,
} from "../src/gen/sumi/agent/v1/agent_pb";
import {
  CapabilityHealth,
  ComputerService,
  CredentialDeliveryAlgorithm,
  CredentialDeliveryState,
  CredentialKind,
  type Computer,
} from "../src/gen/sumi/computer/v1/computer_pb";
import { PlacementService } from "../src/gen/sumi/placement/v1/placement_pb";
import { sealCredential } from "../src/lib/credentialDelivery";

const baseURL = process.env.PLAYWRIGHT_BASE_URL ?? "http://127.0.0.1:8080";
const repoRoot = fileURLToPath(new URL("../..", import.meta.url));
const run = promisify(execFile);
let ownerCredential = "";
let ownerKeyFile = "";
const createdBindingHandles: string[] = [];
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
  await cleanupCredentialBindings(createdBindingHandles);
  if (seed) await rm(seed.hostRoot, { recursive: true, force: true });
});

test("real facts move from pending to ready after sumi-computer ack", async ({
  page,
}) => {
  await page.setViewportSize({ width: 1440, height: 900 });
  await page.goto("/");
  await openAgent(page, seed.pendingAgentName);
  await expect(agentRegion(page).locator(".state-chip.pending")).toBeVisible();

  await runHost(seed);
  await page.getByRole("button", { name: "Refresh Agents" }).click();

  await expect(agentRegion(page).locator(".state-chip.ready")).toBeVisible();
  await expect(agentRegion(page)).not.toContainText(seed.pairingToken);
  await expect(page.locator("body")).not.toContainText(seed.hostRoot);
});

test("Agent management exposes runtime configuration with browser Human auth", async ({
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
  await expect(
    agentRegion(page)
      .getByRole("definition")
      .filter({ hasText: seed.computerName }),
  ).toBeVisible();
  await expect(
    agentRegion(page).getByRole("button", { name: "Configure runtime" }),
  ).toBeVisible();
  await expect(agentRegion(page).getByLabel("Runtime Computer")).toHaveValue(
    seed.computerId,
  );
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
  const hostBinary = join(hostRoot, "sumi-computer");
  await run(
    "mise",
    ["exec", "--", "go", "build", "-o", hostBinary, "./cmd/sumi-computer"],
    { cwd: repoRoot },
  );
  const computers = await ownerComputerClient.listComputers({});
  const computer = computers.computers.find(
    (candidate) => candidate.name === computerName,
  );
  if (!computer) throw new Error("Computer seed failed");

  const failedAgentName = `failed-${suffix}`;
  const failedAgent = await createSeedAgent(failedAgentName);
  const pendingAgentName = `pending-${suffix}`;
  const pendingAgent = await createSeedAgent(pendingAgentName);
  const bindingHandles = [
    await configureSeedAgent(
      failedAgent,
      computer,
      hostRoot,
      hostBinary,
      computerName,
    ),
    await configureSeedAgent(
      pendingAgent,
      computer,
      hostRoot,
      hostBinary,
      computerName,
    ),
  ];
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

async function configureSeedAgent(
  agent: Agent,
  computer: Computer,
  hostRoot: string,
  hostBinary: string,
  computerName: string,
) {
  const capability = computer.capabilityInventory?.credentialDelivery;
  if (
    capability?.health !== CapabilityHealth.HEALTHY ||
    capability.algorithm !==
      CredentialDeliveryAlgorithm.X25519_XCHACHA20_POLY1305 ||
    capability.keyId === "" ||
    capability.publicKey.length !== 32
  ) {
    throw new Error("Computer secure credential delivery is unavailable");
  }
  const requestId = randomUUID();
  const expiresAt = new Date(Date.now() + 5 * 60_000);
  const sealed = sealCredential(
    capability.publicKey,
    {
      requestId,
      computerId: computer.id,
      agentId: agent.id,
      credentialKind: "openai",
      keyId: capability.keyId,
      expiresAt,
    },
    `sk-e2e-${randomBytes(32).toString("base64url")}`,
  );
  const enqueued = await ownerComputerClient.enqueueCredentialDelivery({
    requestId,
    computerId: computer.id,
    agentId: agent.id,
    credentialKind: CredentialKind.OPENAI,
    sealedCredential: {
      algorithm: CredentialDeliveryAlgorithm.X25519_XCHACHA20_POLY1305,
      keyId: sealed.keyId,
      ephemeralPublicKey: sealed.ephemeralPublicKey,
      nonce: sealed.nonce,
      ciphertext: sealed.ciphertext,
    },
    expiresAt: timestampFromDate(expiresAt),
  });
  if (!enqueued.delivery) throw new Error("Credential delivery seed failed");
  const completed = await processCredentialDelivery(
    hostRoot,
    hostBinary,
    computerName,
    computer.id,
    agent.id,
    enqueued.delivery.id,
  );
  createdBindingHandles.push(completed.bindingHandle);
  const runtime = await ownerAgentClient.updateAgentRuntimeSpec({
    requestId: randomUUID(),
    agentId: agent.id,
    expectedRevision: 0n,
    engine: EngineKind.BUILTIN,
    providerProtocol: ProviderProtocol.OPENAI_RESPONSES,
    providerEndpoint: "https://provider.invalid/v1",
    model: "e2e-model",
    credentialBindingHandle: completed.bindingHandle,
    sandboxProvider: RuntimeSandboxProvider.TRUSTED_LOCAL,
    maxRunDurationSeconds: 120,
    maxOutputBytes: 1n << 20n,
    toolPolicy: {
      message: true,
      work: true,
      artifact: true,
      knowledge: true,
    },
  });
  if (!runtime.runtimeSpec) throw new Error("Runtime spec seed failed");
  return completed.bindingHandle;
}

async function processCredentialDelivery(
  hostRoot: string,
  hostBinary: string,
  computerName: string,
  computerId: string,
  agentId: string,
  deliveryId: string,
) {
  const daemon = spawn(
    hostBinary,
    [
      "--server",
      baseURL,
      "--data-root",
      join(hostRoot, "data-root"),
      "--name",
      computerName,
    ],
    { cwd: repoRoot, stdio: ["ignore", "ignore", "pipe"] },
  );
  let stderr = "";
  daemon.stderr.setEncoding("utf8");
  daemon.stderr.on("data", (chunk: string) => {
    stderr += chunk;
  });
  try {
    for (let attempt = 0; attempt < 150; attempt += 1) {
      const deliveries = await ownerComputerClient.listCredentialDeliveries({
        computerId,
        agentId,
      });
      const delivery = deliveries.deliveries.find(
        (candidate) => candidate.id === deliveryId,
      );
      if (
        delivery?.state === CredentialDeliveryState.SUCCEEDED &&
        delivery.bindingHandle !== ""
      ) {
        return delivery;
      }
      if (delivery?.state === CredentialDeliveryState.FAILED) {
        throw new Error(
          `Credential delivery failed: ${delivery.errorCode || "unknown"}`,
        );
      }
      if (daemon.exitCode !== null) {
        throw new Error(`Computer daemon stopped early: ${stderr.trim()}`);
      }
      await delay(100);
    }
    throw new Error(`Credential delivery timed out: ${stderr.trim()}`);
  } finally {
    await stopProcess(daemon);
  }
}

async function stopProcess(process: ReturnType<typeof spawn>) {
  if (process.exitCode !== null) return;
  process.kill("SIGTERM");
  await Promise.race([
    new Promise<void>((resolve) => process.once("exit", () => resolve())),
    delay(3000).then(() => {
      process.kill("SIGKILL");
    }),
  ]);
}

function delay(milliseconds: number) {
  return new Promise<void>((resolve) => setTimeout(resolve, milliseconds));
}

async function cleanupCredentialBindings(handles: string[]) {
  for (const handle of handles) {
    if (process.platform === "darwin") {
      await run("security", [
        "delete-generic-password",
        "-a",
        handle,
        "-s",
        "co.sumi.credential",
      ]);
    } else if (process.platform === "linux") {
      await run("secret-tool", [
        "clear",
        "service",
        "co.sumi.credential",
        "account",
        handle,
      ]);
    }
  }
}

async function createSeedAgent(name: string) {
  const response = await ownerAgentClient.createAgent({
    requestId: randomUUID(),
    handle: name.slice(0, 32),
    displayName: name,
    role: "collaborator",
    mission: `Production Web seed for ${name}`,
    instructions: "",
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
