// Screenshot the design-lab sample states for review and before/after diffs.
// Usage: node design-lab/demo/shots.mjs
// Needs the design demo web on SUMI_DESIGN_WEB_PORT (default 5174).
import { chromium } from "@playwright/test";
import { mkdirSync } from "node:fs";
import { join } from "node:path";

const WEB = process.env.SUMI_DESIGN_WEB_URL ?? "http://127.0.0.1:5174";
const EMAIL = process.env.SUMI_SEED_EMAIL ?? "dev@example.test";
const PASSWORD = process.env.SUMI_SEED_PASSWORD ?? "correct horse battery staple";
const OUT = process.env.SUMI_SHOT_DIR ?? "design-lab/demo/.runtime/shots";
mkdirSync(OUT, { recursive: true });

const browser = await chromium.launch();
const page = await browser.newPage({ viewport: { width: 1440, height: 900 } });
const pageErrors = [];
page.on("pageerror", (error) => pageErrors.push(error.message));

await page.goto(`${WEB}/login`);
await page.getByLabel("Email").fill(EMAIL);
await page.getByLabel("Password").fill(PASSWORD);
await page.getByRole("button", { name: "Sign in" }).click();
await page.waitForURL(/\/spaces\/new/);

const viewports = [
  [1440, 900, "desktop"],
  [1024, 768, "tablet"],
  [390, 844, "mobile"],
];

async function shot(path, selector, wait = 400) {
  await page.goto(`${WEB}${path}`);
  if (selector) await page.locator(selector).first().waitFor({ timeout: 15_000 });
  await page.waitForTimeout(wait);
  const [width, height, tag] = viewports[0];
  if (page.viewportSize()?.width !== width) {
    await page.setViewportSize({ width, height });
    await page.waitForTimeout(150);
  }
  const file = join(OUT, `${tag}-${path.replace(/[^a-zA-Z0-9]+/g, "-").replace(/^-|-$/g, "") || "home"}.png`);
  await page.screenshot({ path: file });
  console.log("shot", file);
}

await shot("/s/sumi-dev/channels/general", ".channel-workspace");
await shot("/s/sumi-dev/channels/design-lab", ".channel-workspace");
await shot("/s/sumi-dev/channels/empty-lab", ".channel-workspace");
await shot("/s/sumi-dev/tasks", ".tasks-workspace");
await shot("/s/sumi-dev/inbox", ".inbox-workspace");
await shot("/s/sumi-dev/members", ".members-workspace");
await shot("/s/sumi-dev/agents", ".agents-workspace");
await shot("/s/sumi-dev/computers", ".computers-workspace");

// Thread pane: open the first sample thread from #design-lab.
await page.goto(`${WEB}/s/sumi-dev/channels/design-lab`);
await page.locator(".message-row").first().waitFor();
const replyButtons = page.getByRole("button", { name: "Reply in Thread" });
if (await replyButtons.count()) {
  await replyButtons.first().click();
  await page.locator(".thread-pane").waitFor();
  await page.waitForTimeout(500);
  await page.screenshot({ path: join(OUT, "desktop-thread-pane.png") });
  console.log("shot", join(OUT, "desktop-thread-pane.png"));
}

for (const [width, height, tag] of viewports.slice(1)) {
  await page.setViewportSize({ width, height });
  await page.goto(`${WEB}/s/sumi-dev/channels/general`);
  await page.locator(".channel-workspace").first().waitFor();
  await page.waitForTimeout(300);
  await page.screenshot({ path: join(OUT, `${tag}-channel-general.png`) });
  console.log("shot", join(OUT, `${tag}-channel-general.png`));
}

await browser.close();
if (pageErrors.length) {
  console.error("page errors:", pageErrors);
  process.exitCode = 1;
}
