import { expect, test } from "@playwright/test";
import { constants } from "node:fs";
import { open } from "node:fs/promises";

const localUsername = "owner";
const localPassword = "Sumi local acceptance 2026!";

test.describe.configure({ mode: "serial" });
test.use({
  trace: "off",
  video: "off",
  screenshot: "only-on-failure",
  colorScheme: "light",
});

test("one-time local setup, logout, failed login, and successful login form one Human loop", async ({
  page,
}) => {
  await page.setViewportSize({ width: 1440, height: 900 });
  await page.goto("/");

  await expect(
    page.getByRole("heading", {
      name: /Create your local account|Welcome back/,
    }),
  ).toBeVisible();
  const setupHeading = page.getByRole("heading", {
    name: "Create your local account",
  });
  if (await setupHeading.isVisible()) {
    await settleMotion(page);
    await page.screenshot({
      path: "../test-results/sumi-local-setup-1440x900.png",
      fullPage: true,
    });
    await page.getByLabel("Username").fill(localUsername);
    await page.getByLabel("Password").fill(localPassword);
    await page.getByLabel("Owner setup code").fill(await readOwnerCredential());
    await page.getByRole("button", { name: "Join Sumi" }).click();
  } else {
    await expect(
      page.getByRole("heading", { name: "Welcome back" }),
    ).toBeVisible();
    await signIn(page, localPassword);
  }

  await expect(page.getByText("No conversation selected")).toBeVisible();
  await expect(page.getByRole("button", { name: "Log out" })).toBeVisible();
  await page.screenshot({
    path: "../test-results/sumi-local-authenticated-1440x900.png",
    fullPage: true,
  });

  await page.getByRole("button", { name: "Log out" }).click();
  await expect(
    page.getByRole("heading", { name: "Welcome back" }),
  ).toBeVisible();
  await signIn(page, "incorrect password value");
  await expect(page.getByRole("alert")).toHaveText(
    "Username or password is incorrect.",
  );
  await page.getByLabel("Password").fill(localPassword);
  await expect(page.getByRole("alert")).toHaveCount(0);
  await page.getByRole("button", { name: "Sign in" }).click();

  await expect(page.getByText("No conversation selected")).toBeVisible();
  await expect(page.getByRole("button", { name: "Log out" })).toBeVisible();
  expect(await hasPageOverflow(page)).toBe(false);
});

async function signIn(page: import("@playwright/test").Page, password: string) {
  await page.getByLabel("Username").fill(localUsername);
  await page.getByLabel("Password").fill(password);
  await page.getByRole("button", { name: "Sign in" }).click();
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
      throw new Error(
        "PLAYWRIGHT_OWNER_KEY_FILE must contain one high-entropy credential",
      );
    }
    return credential;
  } finally {
    await handle.close();
  }
}

async function hasPageOverflow(page: import("@playwright/test").Page) {
  return page.evaluate(
    () =>
      document.documentElement.scrollWidth >
      document.documentElement.clientWidth,
  );
}

async function settleMotion(page: import("@playwright/test").Page) {
  await page.mouse.move(0, 0);
  await page.evaluate(async () => {
    await Promise.all(
      document
        .getAnimations()
        .map((animation) => animation.finished.catch(() => {})),
    );
  });
}
