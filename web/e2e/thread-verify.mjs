import { chromium } from "@playwright/test";
import { mkdirSync } from "node:fs";

const base = "http://127.0.0.1:5173";
const output = "/tmp/sumi-shots-thread";
mkdirSync(output, { recursive: true });

const browser = await chromium.launch();
const page = await browser.newPage({ viewport: { width: 1440, height: 900 } });
const errors = [];
page.on("pageerror", (error) => errors.push(`page:${error.message}`));
page.on("console", (message) => {
  if (message.type() === "error") errors.push(`console:${message.text()}`);
});

await page.goto(`${base}/login`);
await page.getByLabel("Email").fill("dev@example.test");
await page.getByLabel("Password").fill("correct horse battery staple");
await page.getByRole("button", { name: "Sign in" }).click();
await page.waitForURL(/\/spaces\/new/);
await page.goto(`${base}/s/sumi-dev/channels/general`);
await page.locator(".channel-workspace").waitFor();

if (await page.locator(".inline-thread-preview").count() === 0) {
  await page.getByRole("button", { name: "Reply in Thread" }).first().click();
  const composer = page.getByRole("textbox", { name: "Thread reply", exact: true });
  for (const reply of ["Thread layout evidence one.", "Thread layout evidence two.", "Thread layout evidence three."]) {
    await composer.fill(reply);
    await Promise.all([
      page.waitForResponse((response) => response.request().method() === "POST" && /\/threads\/\d+\/messages$/.test(response.url())),
      page.getByRole("button", { name: "Send Thread reply", exact: true }).click(),
    ]);
  }
  await page.getByRole("button", { name: "Close Thread" }).click();
}

const results = [];
for (const [width, height, label] of [[1440, 900, "desktop"], [1024, 768, "tablet"], [390, 844, "mobile"]]) {
  await page.setViewportSize({ width, height });
  const timeline = page.locator(".message-timeline");
  await timeline.evaluate((element) => { element.scrollTop = Math.max(0, element.scrollHeight - 80); });
  const before = await timeline.evaluate((element) => element.scrollTop);
  await page.screenshot({ path: `${output}/preview-${label}.png` });
  await page.locator(".inline-thread-heading").first().click();
  const pane = page.locator(".thread-pane");
  await pane.waitFor();
  const box = await pane.boundingBox();
  await page.screenshot({ path: `${output}/pane-${label}.png` });
  const overflow = await page.evaluate(() => document.documentElement.scrollWidth > window.innerWidth);
  if (label === "mobile") {
    if (!box || Math.round(box.x) !== 0 || Math.round(box.width) !== width) throw new Error(`mobile pane is not full screen: ${JSON.stringify(box)}`);
  } else if (!box || box.x <= 0 || box.width >= width) {
    throw new Error(`${label} pane does not preserve the Channel: ${JSON.stringify(box)}`);
  }
  await page.getByRole("button", { name: "Close Thread" }).click();
  const after = await timeline.evaluate((element) => element.scrollTop);
  results.push({ label, overflow, scrollRestored: before === after, pane: box && { x: Math.round(box.x), width: Math.round(box.width) } });
}

await page.setViewportSize({ width: 1440, height: 900 });
await page.locator(".inline-thread-heading").first().click();
const subscription = page.getByRole("button", { name: /^(Follow|Unfollow) Thread$/ });
const originalLabel = await subscription.getAttribute("aria-label");
await subscription.click();
await page.getByRole("button", { name: originalLabel === "Follow Thread" ? "Unfollow Thread" : "Follow Thread" }).click();
await page.getByRole("button", { name: "Close Thread" }).click();

const channelSlug = `thread-check-${Date.now().toString(36)}`;
await page.getByRole("button", { name: "Create Channel" }).click();
const dialog = page.getByRole("dialog", { name: "Create Channel" });
await dialog.getByLabel("Name").fill("Thread Check");
await dialog.getByLabel("Slug").fill(channelSlug);
await dialog.getByLabel("Visibility").selectOption("private");
await dialog.getByRole("button", { name: "Create Channel", exact: true }).click();
await page.waitForURL(new RegExp(`/channels/${channelSlug}$`));

const channelComposer = page.getByRole("textbox", { name: "Message", exact: true });
await channelComposer.fill("Thread context root.");
await Promise.all([
  page.waitForResponse((response) => response.request().method() === "POST" && /\/channels\/[^/]+\/messages$/.test(response.url())),
  page.getByRole("button", { name: "Send message", exact: true }).click(),
]);
await page.getByRole("button", { name: "Reply in Thread" }).click();
const threadComposer = page.getByRole("textbox", { name: "Thread reply", exact: true });
await threadComposer.fill("Thread context reply.");
await Promise.all([
  page.waitForResponse((response) => response.request().method() === "POST" && /\/threads\/\d+\/messages$/.test(response.url())),
  page.getByRole("button", { name: "Send Thread reply", exact: true }).click(),
]);
await channelComposer.fill("Channel timeline changed.");
await Promise.all([
  page.waitForResponse((response) => response.request().method() === "POST" && /\/channels\/[^/]+\/messages$/.test(response.url())),
  page.getByRole("button", { name: "Send message", exact: true }).click(),
]);
const contextUpdate = page.locator(".thread-context-update");
await contextUpdate.waitFor();
await page.screenshot({ path: `${output}/context-update-desktop.png` });
await contextUpdate.click();
const channelHeadingFocused = await page.getByRole("heading", { name: `#${channelSlug}` }).evaluate((element) => document.activeElement === element);
await page.getByRole("button", { name: "Archive Channel" }).click();
await page.waitForURL(/\/channels\/general$/);

if (errors.length) throw new Error(errors.join("\n"));
console.log(JSON.stringify({ results, subscriptionRoundTrip: true, contextUpdate: true, channelHeadingFocused, errors: errors.length, output }));
await browser.close();
