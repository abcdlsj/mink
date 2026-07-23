import { expect, test, type Locator, type Page } from "@playwright/test";
import { createClient, type Interceptor } from "@connectrpc/connect";
import { createConnectTransport } from "@connectrpc/connect-web";
import { randomUUID } from "node:crypto";
import { execFile } from "node:child_process";
import { constants } from "node:fs";
import { open } from "node:fs/promises";
import { fileURLToPath } from "node:url";
import { promisify } from "node:util";
import { AgentService, Driver } from "../src/gen/sumi/agent/v1/agent_pb";

const baseURL = process.env.PLAYWRIGHT_BASE_URL ?? "http://127.0.0.1:8080";
const repoRoot = fileURLToPath(new URL("../..", import.meta.url));
const run = promisify(execFile);
let ownerCredential = "";
let ownerKeyFile = "";
let agentName = "";
let groupName = "";
let mainMessage = "";

const ownerAuthorization: Interceptor = (next) => async (request) => {
  if (!ownerCredential) throw new Error("Owner credential is not available");
  request.header.set("Authorization", `Bearer ${ownerCredential}`);
  return next(request);
};
const ownerAgentClient = createClient(
  AgentService,
  createConnectTransport({
    baseUrl: baseURL,
    interceptors: [ownerAuthorization],
  }),
);

test.describe.configure({ mode: "serial" });
test.use({
  trace: "off",
  video: "off",
  screenshot: "only-on-failure",
  colorScheme: "light",
});

test.beforeAll(async () => {
  const owner = await readOwnerCredential();
  ownerCredential = owner.credential;
  ownerKeyFile = owner.path;
  const suffix = randomUUID().slice(0, 8);
  agentName = `collaborator-${suffix}`;
  groupName = `Release room ${suffix}`;
  mainMessage = `Main message ${suffix}`;
  const created = await ownerAgentClient.createAgent({
    requestId: randomUUID(),
    name: agentName,
    description: "Production browser collaboration peer",
    driver: Driver.CODEX,
  });
  if (!created.agent) throw new Error("Agent seed failed");
});

test.beforeEach(async ({ page }) => {
  await authenticatePage(page);
});

test.afterAll(() => {
  ownerCredential = "";
  ownerKeyFile = "";
});

test("real page creates canonical DM and Group, sends main/Thread messages, manages members and archive state", async ({
  page,
}) => {
  await page.setViewportSize({ width: 1440, height: 900 });
  await page.goto("/");
  await expect(page.getByText("No conversation selected")).toBeVisible();

  await createDMFromPage(page);
  await expect(
    page.getByRole("heading", { name: "Direct message" }),
  ).toBeVisible();
  const mainComposer = page.getByTestId("main-composer");
  await mainComposer
    .getByRole("textbox", { name: "Message", exact: true })
    .fill(mainMessage);
  await mainComposer.getByRole("button", { name: "Send message" }).click();
  const mainRow = page.locator(".message-row").filter({ hasText: mainMessage });
  await expect(mainRow).toBeVisible();

  await mainRow.getByRole("button", { name: "Open thread" }).click();
  const context = contextPane(page);
  await expect(context).toBeVisible();
  await expect(
    context.getByText("No replies yet. Start the Thread below."),
  ).toBeVisible();
  const threadComposer = context.getByTestId("thread-composer");
  await threadComposer
    .getByRole("textbox", { name: "Thread reply", exact: true })
    .fill("First reply");
  await threadComposer.getByRole("button", { name: "Send message" }).click();
  await expect(
    context
      .getByRole("list", { name: "Thread replies" })
      .getByText("First reply"),
  ).toBeVisible();
  await threadComposer
    .getByRole("textbox", { name: "Thread reply", exact: true })
    .fill("Second reply");
  await threadComposer.getByRole("button", { name: "Send message" }).click();
  await expect(
    context
      .getByRole("list", { name: "Thread replies" })
      .getByText("Second reply"),
  ).toBeVisible();
  await context.getByRole("button", { name: "Close thread" }).click();

  const dmRows = conversationNavigation(page).getByRole("button", {
    name: "Direct message",
    exact: true,
  });
  const dmCount = await dmRows.count();
  await createDMFromPage(page);
  await expect(mainRow).toBeVisible();
  await expect(dmRows).toHaveCount(dmCount);

  await createGroupFromPage(page);
  await expect(page.getByRole("heading", { name: groupName })).toBeVisible();
  await page.getByRole("button", { name: "Open context" }).click();
  await addMemberFromPage(contextPane(page));
  await expect(contextPane(page).getByText(agentName)).toBeVisible();
  await contextPane(page)
    .getByRole("button", { name: `Remove ${agentName}` })
    .click();
  await expect(
    contextPane(page).getByRole("button", { name: `Remove ${agentName}` }),
  ).toHaveCount(0);
  await addMemberFromPage(contextPane(page));
  await expect(
    contextPane(page).getByRole("button", { name: `Remove ${agentName}` }),
  ).toBeVisible();

  await contextPane(page)
    .getByRole("button", { name: "Archive Space" })
    .click();
  await expect(
    page.getByRole("textbox", { name: "Message", exact: true }),
  ).toHaveAttribute("placeholder", "Archived Spaces are read-only");
  await expect(
    contextPane(page).getByRole("button", { name: "Unarchive Space" }),
  ).toBeVisible();
  await expect(
    navigationSection(page, "Archived").getByRole("button", {
      name: new RegExp(groupName),
    }),
  ).toBeVisible();
  await expect(
    navigationSection(page, "Spaces").getByRole("button", {
      name: groupName,
      exact: true,
    }),
  ).toHaveCount(0);
  await contextPane(page)
    .getByRole("button", { name: "Unarchive Space" })
    .click();
  await expect(
    page.getByRole("textbox", { name: "Message", exact: true }),
  ).toHaveAttribute("placeholder", "Message this Space");
  await expect(
    contextPane(page).getByRole("button", { name: "Archive Space" }),
  ).toBeVisible();
  await expect(
    navigationSection(page, "Spaces").getByRole("button", {
      name: groupName,
      exact: true,
    }),
  ).toBeVisible();
  await expect(
    navigationSection(page, "Archived").getByRole("button", {
      name: new RegExp(groupName),
    }),
  ).toHaveCount(0);

  expect(await hasPageOverflow(page)).toBe(false);
  await page.screenshot({
    path: "../test-results/sumi-collaboration-1440x900.png",
    fullPage: true,
  });

  await page.getByRole("button", { name: "Use dark theme" }).click();
  await expect(page.locator("html")).toHaveAttribute("data-theme", "dark");
  expect(await hasPageOverflow(page)).toBe(false);
  await settleMotion(page);
  await page.screenshot({
    path: "../test-results/sumi-collaboration-dark-1440x900.png",
    fullPage: true,
  });

  await page.getByRole("button", { name: "Log out" }).click();
  await expect(page.getByText("Authentication required")).toBeVisible();
  expect(await collaborationStatus(page)).toBe(401);
  await page.getByRole("button", { name: "Agents" }).click();
  await expect(
    page
      .getByRole("complementary", { name: "Agents navigation" })
      .getByText(agentName),
  ).toBeVisible();
});

test("external session revocation clears protected facts on the next Collaboration request", async ({
  page,
}) => {
  await page.setViewportSize({ width: 1440, height: 900 });
  await page.goto("/");
  await conversationNavigation(page)
    .getByRole("button", { name: "Direct message", exact: true })
    .last()
    .click();
  const mainRow = page.locator(".message-row").filter({ hasText: mainMessage });
  await expect(mainRow).toBeVisible();
  await mainRow.getByRole("button", { name: "Open thread" }).click();
  await expect(contextPane(page)).toBeVisible();
  await expect(
    contextPane(page)
      .getByRole("list", { name: "Thread replies" })
      .getByText("First reply"),
  ).toBeVisible();

  expect(
    await page.evaluate(async () =>
      fetch("/auth/logout", { method: "POST" }).then(
        (response) => response.status,
      ),
    ),
  ).toBe(204);
  await page
    .getByRole("button", { name: "Refresh conversation", exact: true })
    .click();

  await expect(page.getByText("Authentication required")).toBeVisible();
  await expect(mainRow).toHaveCount(0);
  await expect(contextPane(page)).toBeHidden();
  expect(await collaborationStatus(page)).toBe(401);
});

for (const viewport of [
  { width: 1024, height: 768 },
  { width: 900, height: 700 },
  { width: 1023, height: 700 },
]) {
  test(`authenticated navigation, conversation and context remain single-pane safe at ${viewport.width}px`, async ({
    page,
  }) => {
    await page.setViewportSize(viewport);
    await page.goto("/");
    await openNavigation(page);
    await conversationNavigation(page)
      .getByRole("button", { name: groupName, exact: true })
      .focus();
    await page.keyboard.press("Enter");
    await expect(page.getByRole("heading", { name: groupName })).toBeVisible();
    await expect(conversationNavigation(page)).toBeHidden();
    expect(await hasPageOverflow(page)).toBe(false);

    await page.getByRole("button", { name: "Open context" }).click();
    if (viewport.width < 1024) {
      await expect(conversationRegion(page)).toBeHidden();
      await expect(contextPane(page)).toBeVisible();
      await contextPane(page)
        .getByRole("button", { name: "Back to conversation" })
        .click();
      await expect(conversationRegion(page)).toBeVisible();
      await expect(contextPane(page)).toBeHidden();

      await openNavigation(page);
      await conversationNavigation(page)
        .getByRole("button", { name: "Direct message", exact: true })
        .last()
        .click();
      const row = page.locator(".message-row").filter({ hasText: mainMessage });
      await row.getByRole("button", { name: "Open thread" }).focus();
      await page.keyboard.press("Enter");
      await expect(conversationRegion(page)).toBeHidden();
      await expect(
        contextPane(page).getByTestId("thread-composer"),
      ).toBeVisible();
      await settleMotion(page);
      await page.screenshot({
        path: `../test-results/sumi-collaboration-thread-${viewport.width}x${viewport.height}.png`,
        fullPage: true,
      });
      await contextPane(page)
        .getByRole("button", { name: "Back to conversation" })
        .click();
      await expect(conversationRegion(page)).toBeVisible();
    } else {
      await expect(conversationRegion(page)).toBeVisible();
      await expect(contextPane(page)).toBeVisible();
      const composer = await page.getByTestId("main-composer").boundingBox();
      const context = await contextPane(page).boundingBox();
      expect(composer).not.toBeNull();
      expect(context).not.toBeNull();
      expect(composer!.x + composer!.width).toBeLessThanOrEqual(context!.x);
    }
    expect(await hasPageOverflow(page)).toBe(false);
    await settleMotion(page);
    await page.screenshot({
      path: `../test-results/sumi-collaboration-${viewport.width}x${viewport.height}.png`,
      fullPage: true,
    });
  });
}

async function createDMFromPage(page: Page) {
  await openNavigation(page);
  await conversationNavigation(page)
    .getByRole("button", { name: "Create conversation" })
    .click();
  const form = conversationNavigation(page).getByRole("region", {
    name: "Create conversation",
  });
  await form.getByLabel("Peer").selectOption({ label: `${agentName} · Agent` });
  await form.getByRole("button", { name: "Create DM" }).click();
  await expect(form).toBeHidden();
}

async function createGroupFromPage(page: Page) {
  await openNavigation(page);
  await conversationNavigation(page)
    .getByRole("button", { name: "Create conversation" })
    .click();
  const form = conversationNavigation(page).getByRole("region", {
    name: "Create conversation",
  });
  await form.getByRole("tab", { name: /Group/ }).click();
  await form.getByLabel("Group name").fill(groupName);
  await form.getByRole("button", { name: "Create Group" }).click();
  await expect(form).toBeHidden();
}

async function addMemberFromPage(context: Locator) {
  await context
    .getByLabel("Member to add")
    .selectOption({ label: `${agentName} · Agent` });
  await context.getByRole("button", { name: "Add", exact: true }).click();
}

async function openNavigation(page: Page) {
  const navigation = conversationNavigation(page);
  if (!(await navigation.isVisible())) {
    await page.getByRole("button", { name: "Open navigation" }).click();
  }
  await expect(navigation).toBeVisible();
}

function conversationNavigation(page: Page) {
  return page.getByRole("complementary", {
    name: "Conversation navigation",
  });
}

function conversationRegion(page: Page) {
  return page.getByRole("region", { name: "Conversation", exact: true });
}

function navigationSection(page: Page, label: "Spaces" | "Archived") {
  return conversationNavigation(page)
    .locator("details.nav-section")
    .filter({ hasText: label });
}

function contextPane(page: Page) {
  return page.getByRole("complementary", { name: "Context", exact: true });
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
    return { credential, path: process.env.PLAYWRIGHT_OWNER_KEY_FILE };
  } finally {
    await handle.close();
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

async function hasPageOverflow(page: Page) {
  return page.evaluate(
    () =>
      document.documentElement.scrollWidth >
      document.documentElement.clientWidth,
  );
}

async function settleMotion(page: Page) {
  await page.mouse.move(0, 0);
  await page.evaluate(async () => {
    await Promise.all(
      document
        .getAnimations()
        .map((animation) => animation.finished.catch(() => {})),
    );
  });
}

function escapePattern(value: string) {
  return value.replace(/[.*+?^${}()|[\]\\]/g, "\\$&");
}
