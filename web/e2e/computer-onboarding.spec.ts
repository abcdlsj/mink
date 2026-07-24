import { expect, test, type Page } from "@playwright/test";
import { constants } from "node:fs";
import { open } from "node:fs/promises";
import { execFile } from "node:child_process";
import { fileURLToPath } from "node:url";
import { promisify } from "node:util";

const execFileAsync = promisify(execFile);
const baseURL = process.env.PLAYWRIGHT_BASE_URL ?? "http://127.0.0.1:8080";
const repoRoot = fileURLToPath(new URL("../..", import.meta.url));

test.use({ trace: "off", video: "off", screenshot: "only-on-failure" });

test("Computer onboarding copies one private start command without rendering its code", async ({
  context,
  page,
}) => {
  await page.setViewportSize({ width: 1440, height: 900 });
  await context.grantPermissions(["clipboard-read", "clipboard-write"], {
    origin: baseURL,
  });
  await authenticatePage(page);
  await page.getByRole("button", { name: "Computers" }).click();
  const firstComputer = page.getByRole("heading", {
    name: "Connect your first Computer",
  });
  if (!(await firstComputer.isVisible())) {
    await page
      .locator("summary", { hasText: "Connect another Computer" })
      .click();
    await expect(
      page.getByRole("heading", { name: "Connect another Computer" }),
    ).toBeVisible();
  }
  await expect(
    page.getByText(/sumi computer start --pairing-code/),
  ).toContainText("••••");

  await page.getByRole("button", { name: "Create connection command" }).click();
  await expect(
    page.getByRole("button", { name: "Create a new command" }),
  ).toBeVisible();
  await page
    .getByRole("button", { name: "Copy Computer pairing command" })
    .click();
  await expect(
    page.getByRole("button", { name: "Copy Computer pairing command" }),
  ).toContainText("Copied");

  const command = await page.evaluate(() => navigator.clipboard.readText());
  expect(command).toMatch(
    /^sumi computer start --pairing-code sumi-pair-v1\.[A-Za-z0-9_-]+$/,
  );
  const code = command.slice("sumi computer start --pairing-code ".length);
  await expect(page.locator("body")).not.toContainText(code);
  await page.screenshot({
    path: "../test-results/sumi-computer-connection-command-1440x900.png",
    fullPage: true,
  });
});

async function authenticatePage(page: Page) {
  const key = await readHumanCredential();
  const result = await execFileAsync(
    "go",
    [
      "run",
      "./cmd/sumi-server",
      "auth",
      "--server",
      baseURL,
      "--human-key-file",
      key.path,
    ],
    { cwd: repoRoot },
  );
  const url = result.stdout.trim();
  if (
    !new RegExp(
      `^${escapePattern(baseURL)}/auth/browser-handoffs/[A-Za-z0-9_-]{43}$`,
    ).test(url)
  ) {
    throw new Error("Browser handoff returned an unsafe URL");
  }
  await page.goto(url);
  await expect(page.getByText("No conversation selected")).toBeVisible();
}

async function readHumanCredential() {
  const path = process.env.PLAYWRIGHT_OWNER_KEY_FILE;
  if (!path) throw new Error("PLAYWRIGHT_OWNER_KEY_FILE is required");
  const handle = await open(path, constants.O_RDONLY | constants.O_NOFOLLOW);
  try {
    const info = await handle.stat();
    const credential = await handle.readFile({ encoding: "utf8" });
    if (
      !info.isFile() ||
      (info.mode & 0o777) !== 0o600 ||
      !/^[A-Za-z0-9_-]{43,128}$/.test(credential)
    ) {
      throw new Error("PLAYWRIGHT_OWNER_KEY_FILE is invalid");
    }
    return { path, credential };
  } finally {
    await handle.close();
  }
}

function escapePattern(value: string) {
  return value.replace(/[.*+?^${}()|[\]\\]/g, "\\$&");
}
