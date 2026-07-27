import { chromium } from "@playwright/test";
import { mkdirSync } from "node:fs";

const base = "http://127.0.0.1:5173";
const output = "/tmp/sumi-shots-onboarding";
const viewports = [[1440, 900, "desktop"], [1024, 768, "tablet"], [390, 844, "mobile"]];
mkdirSync(output, { recursive: true });

const browser = await chromium.launch();
const page = await browser.newPage({ viewport: { width: 1440, height: 900 } });
const errors = [];
let expectingFailure = false;
page.on("pageerror", (error) => errors.push(`page:${error.message}`));
page.on("console", (message) => {
  if (expectingFailure && message.type() === "error" && message.text().includes("Failed to load resource")) return;
  if (message.type() === "error") errors.push(`console:${message.text()}`);
});

async function assertViewport(label, selector) {
  const geometry = await page.locator(selector).evaluate((element) => {
    const rect = element.getBoundingClientRect();
    return {
      left: Math.round(rect.left),
      top: Math.round(rect.top),
      right: Math.round(rect.right),
      bottom: Math.round(rect.bottom),
      viewportWidth: window.innerWidth,
      viewportHeight: window.innerHeight,
      documentOverflow: document.documentElement.scrollWidth > window.innerWidth,
    };
  });
  if (geometry.left < 0 || geometry.right > geometry.viewportWidth || geometry.documentOverflow) {
    throw new Error(`${selector} overflows ${label}: ${JSON.stringify(geometry)}`);
  }
  return geometry;
}

const results = [];
for (const [width, height, label] of viewports) {
  await page.setViewportSize({ width, height });
  await page.goto(base);
  await page.getByRole("list", { name: "Onboarding step 1 of 3" }).waitFor();
  if (!(await page.getByLabel("Display name").evaluate((element) => document.activeElement === element))) {
    throw new Error(`Register has the wrong initial focus at ${label}`);
  }
  results.push({ surface: "register", label, geometry: await assertViewport(label, ".register-stage") });
  await page.waitForTimeout(300);
  await page.screenshot({ path: `${output}/register-${label}.png`, fullPage: false });

  await page.goto(`${base}/spaces/new`);
  await page.getByRole("list", { name: "Onboarding step 2 of 3" }).waitFor();
  if (!(await page.getByLabel("Space name").evaluate((element) => document.activeElement === element))) {
    throw new Error(`Create Space has the wrong initial focus at ${label}`);
  }
  await page.getByLabel("Space name").fill("Sumi Visual Lab");
  if ((await page.getByText("/s/sumi-visual-lab").count()) !== 1) throw new Error(`Space URL preview failed at ${label}`);
  await page.getByRole("radio", { name: "Cyan accent" }).check();
  results.push({ surface: "space", label, geometry: await assertViewport(label, ".register-stage") });
  await page.waitForTimeout(300);
  await page.screenshot({ path: `${output}/space-${label}.png`, fullPage: false });
}

await page.setViewportSize({ width: 1440, height: 900 });
await page.goto(base);
await page.route("**/api/v1/auth/register", async (route) => {
  await route.fulfill({
    status: 409,
    contentType: "application/json",
    body: JSON.stringify({ error: { code: "email_taken", message: "That email already belongs to a Human." } }),
  });
});
await page.getByLabel("Display name").fill("Error Probe");
await page.getByLabel("Email").fill("error@example.test");
await page.getByLabel("Password").fill("correct-horse-battery");
expectingFailure = true;
await page.getByRole("button", { name: "Continue" }).click();
await page.getByRole("alert").waitFor();
expectingFailure = false;
await page.screenshot({ path: `${output}/register-error-desktop.png`, fullPage: false });
await page.unroute("**/api/v1/auth/register");

const nonce = Date.now().toString(36);
const slug = `onboarding-${nonce}`;
await page.goto(base);
await page.getByLabel("Display name").fill("Onboarding Human");
await page.getByLabel("Email").fill(`onboarding-${nonce}@example.test`);
await page.getByLabel("Password").fill("correct-horse-battery");
await page.getByRole("button", { name: "Continue" }).click();
await page.waitForURL(/\/spaces\/new$/);
await page.getByLabel("Space name").fill("Onboarding Lab");
await page.getByLabel("URL slug").fill(slug);
await page.getByRole("radio", { name: "Pink accent" }).check();
await page.getByRole("button", { name: "Enter general" }).click();
await page.waitForURL(new RegExp(`/s/${slug}/channels/general$`));
await page.getByRole("region", { name: "Finish your Space setup" }).waitFor();
const pair = page.getByRole("link", { name: "Pair" });
if (!(await pair.getAttribute("href"))?.endsWith("/computers#pair-computer")) throw new Error("Setup strip Pair action is incorrect");

for (const [width, height, label] of viewports) {
  await page.setViewportSize({ width, height });
  await page.getByRole("region", { name: "Finish your Space setup" }).waitFor();
  results.push({ surface: "setup", label, geometry: await assertViewport(label, ".setup-strip") });
  await page.screenshot({ path: `${output}/setup-${label}.png`, fullPage: false });
}

await pair.click();
await page.waitForURL(new RegExp(`/s/${slug}/computers#pair-computer$`));
const pairDialog = page.getByRole("dialog", { name: "Pair Computer" });
await pairDialog.waitFor();
await assertViewport("mobile", ".pair-computer-dialog");
await page.screenshot({ path: `${output}/setup-pair-mobile.png`, fullPage: false });
await page.keyboard.press("Escape");
await pairDialog.waitFor({ state: "detached" });

if (errors.length) throw new Error(errors.join("\n"));
console.log(JSON.stringify({ slug, results, errorState: true, errors: errors.length, output }));
await browser.close();
