import { expect, test, type Page } from "@playwright/test";
import { create } from "@bufbuild/protobuf";
import { createClient, type Interceptor } from "@connectrpc/connect";
import { createConnectTransport } from "@connectrpc/connect-web";
import { randomUUID } from "node:crypto";
import { execFile } from "node:child_process";
import { constants } from "node:fs";
import { open } from "node:fs/promises";
import { fileURLToPath } from "node:url";
import { promisify } from "node:util";
import { CollaborationService, MessageTargetSchema } from "../src/gen/sumi/space/v1/space_pb";
import { WorkService } from "../src/gen/sumi/work/v1/work_pb";

const baseURL = process.env.PLAYWRIGHT_BASE_URL ?? "http://127.0.0.1:8080";
const repoRoot = fileURLToPath(new URL("../..", import.meta.url));
const run = promisify(execFile);
let ownerCredential = "";
let ownerKeyFile = "";
let groupName = "";
let groupID = "";
let sourceMessageID = "";
let sourceTargetSequence = 0n;

const ownerAuthorization: Interceptor = (next) => async (request) => {
  request.header.set("Authorization", `Bearer ${ownerCredential}`);
  return next(request);
};
const ownerTransport = createConnectTransport({
  baseUrl: baseURL,
  interceptors: [ownerAuthorization],
});
const ownerCollaboration = createClient(CollaborationService, ownerTransport);
const ownerWork = createClient(WorkService, ownerTransport);

test.describe.configure({ mode: "serial" });
test.use({ trace: "off", video: "off", screenshot: "only-on-failure" });

test.beforeAll(async () => {
  const owner = await readOwnerCredential();
  ownerCredential = owner.credential;
  ownerKeyFile = owner.path;
  const suffix = randomUUID().slice(0, 8);
  groupName = `Work inbox ${suffix}`;
  const group = await ownerCollaboration.createGroup({
    requestId: randomUUID(),
    name: groupName,
  });
  if (!group.space) throw new Error("Work Inbox Group seed failed");
  groupID = group.space.id;
  const target = create(MessageTargetSchema, {
    target: { case: "spaceId", value: groupID },
  });
  const message = await ownerCollaboration.sendMessage({
    requestId: randomUUID(),
    target,
    body: "Work source message",
  });
  if (!message.message) throw new Error("Work Inbox source seed failed");
  sourceMessageID = message.message.id;
  sourceTargetSequence = message.message.targetSequence;
});

async function seedApproval() {
  const goal = `Approve Work ${randomUUID().slice(0, 8)}`;
  const work = await ownerWork.createWork({
    requestId: randomUUID(),
    sourceMessageId: sourceMessageID,
    sourceSpaceId: groupID,
    sourceTarget: create(MessageTargetSchema, {
      target: { case: "spaceId", value: groupID },
    }),
    sourceTargetSequence,
    goal,
    acceptanceCriteria: ["approval visible"],
  });
  if (!work.work) throw new Error("Work Inbox Work seed failed");
  await ownerWork.requestApproval({
    requestId: randomUUID(),
    workId: work.work.id,
    question: "Approve the Work Inbox path?",
  });
  return goal;
}

test.afterAll(() => {
  ownerCredential = "";
  ownerKeyFile = "";
});

for (const viewport of [
  { width: 1440, height: 900 },
  { width: 1024, height: 768 },
]) {
  test(`Human Work Inbox opens and resolves actual approval at ${viewport.width}px`, async ({ page }) => {
    const workGoal = await seedApproval();
    await page.setViewportSize(viewport);
    await authenticatePage(page);
    await page.goto("/");
    const navigation = page.getByRole("complementary", { name: "Conversation navigation" });
    if (!(await navigation.isVisible())) {
      await page.getByRole("button", { name: "Open navigation" }).click();
    }
    await navigation.getByRole("button", { name: groupName, exact: true }).click();
    await page.getByRole("tab", { name: "Work" }).click();
    const inbox = page.getByRole("region", { name: "Work Inbox" });
    await expect(inbox.getByText("Approval requested")).toBeVisible();
    await inbox.getByRole("button", { name: /Approval requested/ }).click();
    await expect(inbox.getByRole("heading", { name: workGoal })).toBeVisible();
    await inbox.getByRole("button", { name: "Approve" }).click();
    await expect(inbox.getByText("No Work needs your attention.")).toBeVisible();
    expect(await page.evaluate(() => document.documentElement.scrollWidth > document.documentElement.clientWidth)).toBe(false);
    await page.screenshot({
      path: `../test-results/sumi-work-${viewport.width}x${viewport.height}.png`,
      fullPage: true,
    });
  });
}

async function authenticatePage(page: Page) {
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
  const url = result.stdout.trim();
  if (!new RegExp(`^${escapePattern(baseURL)}/auth/browser-handoffs/[A-Za-z0-9_-]{43}$`).test(url)) {
    throw new Error("Browser handoff returned an unsafe URL");
  }
  await page.goto(url);
  if (page.url() !== `${baseURL}/`) {
    throw new Error("Browser handoff did not clear its one-time URL");
  }
}

async function readOwnerCredential() {
  const path = process.env.PLAYWRIGHT_OWNER_KEY_FILE;
  if (!path) throw new Error("PLAYWRIGHT_OWNER_KEY_FILE is required");
  const handle = await open(path, constants.O_RDONLY | constants.O_NOFOLLOW);
  try {
    const info = await handle.stat();
    if (!info.isFile() || (info.mode & 0o777) !== 0o600) {
      throw new Error("PLAYWRIGHT_OWNER_KEY_FILE must be a regular 0600 file");
    }
    const credential = await handle.readFile({ encoding: "utf8" });
    if (!/^[A-Za-z0-9_-]{43,128}$/.test(credential)) {
      throw new Error("PLAYWRIGHT_OWNER_KEY_FILE must contain one high-entropy credential");
    }
    return { credential, path };
  } finally {
    await handle.close();
  }
}

function escapePattern(value: string) {
  return value.replace(/[.*+?^${}()|[\]\\]/g, "\\$&");
}
