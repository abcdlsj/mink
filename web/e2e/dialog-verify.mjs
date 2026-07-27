import { chromium } from "@playwright/test";
import { mkdirSync } from "node:fs";
import { v7 as uuidv7 } from "uuid";

const base = "http://127.0.0.1:5173";
const output = "/tmp/sumi-shots-dialog";
mkdirSync(output, { recursive: true });

const browser = await chromium.launch();
const page = await browser.newPage({ viewport: { width: 1440, height: 900 } });
const errors = [];
let expectingAgentError = false;
page.on("pageerror", (error) => errors.push(`page:${error.message}`));
page.on("console", (message) => {
  if (expectingAgentError && message.type() === "error" && message.text().includes("Failed to load resource")) return;
  if (message.type() === "error") errors.push(`console:${message.text()}`);
});

await page.goto(`${base}/login`);
await page.getByLabel("Email").fill("dev@example.test");
await page.getByLabel("Password").fill("correct horse battery staple");
await page.getByRole("button", { name: "Sign in" }).click();
await page.waitForURL(/\/spaces\/new/);

const viewports = [[1440, 900, "desktop"], [1024, 768, "tablet"], [390, 844, "mobile"]];
const results = [];

async function openNavigationWhenCompact() {
  if (await page.locator(".space-navigation--open").count()) return;
  const trigger = page.getByRole("button", { name: "Open navigation" }).first();
  if (await trigger.isVisible().catch(() => false)) await trigger.click();
}

async function cleanupDialogChannels() {
  await page.setViewportSize({ width: 1440, height: 900 });
  await page.goto(`${base}/s/sumi-dev/channels/general`);
  await page.locator(".channel-workspace").waitFor();
  const spaceResponse = await page.request.get(`${base}/api/v1/spaces/by-slug/sumi-dev`);
  const space = await spaceResponse.json();
  const channelsResponse = await page.request.get(`${base}/api/v1/spaces/${space.id}/channels`);
  const channels = await channelsResponse.json();
  for (const channel of channels.channels.filter((candidate) => candidate.slug.startsWith("dialog-check-"))) {
    const response = await page.request.post(`${base}/api/v1/channels/${channel.id}/archive`, {
      headers: { "Idempotency-Key": uuidv7() },
    });
    if (!response.ok()) throw new Error(`Could not archive stale Dialog check Channel ${channel.slug}: ${response.status()}`);
  }
}

async function assertDialog(dialog, name, label, expectedFocus) {
  await dialog.waitFor();
  const geometry = await dialog.evaluate((element) => {
    const rect = element.getBoundingClientRect();
    return {
      x: Math.round(rect.x),
      y: Math.round(rect.y),
      right: Math.round(rect.right),
      bottom: Math.round(rect.bottom),
      width: Math.round(rect.width),
      height: Math.round(rect.height),
      viewportWidth: window.innerWidth,
      viewportHeight: window.innerHeight,
      documentOverflow: document.documentElement.scrollWidth > window.innerWidth,
    };
  });
  if (geometry.x < 0 || geometry.y < 0 || geometry.right > geometry.viewportWidth || geometry.bottom > geometry.viewportHeight) {
    throw new Error(`${name} escapes ${label} viewport: ${JSON.stringify(geometry)}`);
  }
  if (geometry.documentOverflow) throw new Error(`${name} causes document overflow at ${label}`);
  if (expectedFocus && !(await expectedFocus.evaluate((element) => document.activeElement === element))) {
    throw new Error(`${name} has the wrong initial focus at ${label}`);
  }
  await page.screenshot({ path: `${output}/${name.toLowerCase().replaceAll(" ", "-").replaceAll("?", "")}-${label}.png` });
  results.push({ name, label, geometry });
  return dialog;
}

await cleanupDialogChannels();
for (const [width, height, label] of viewports) {
  await page.setViewportSize({ width, height });
  await openNavigationWhenCompact();
  const trigger = page.getByRole("button", { name: "Create Channel" });
  const triggerElement = await trigger.elementHandle();
  await trigger.click();
  const dialog = page.getByRole("dialog", { name: "Create Channel" });
  await assertDialog(dialog, "Create Channel", label, dialog.getByLabel("Name"));
  await page.keyboard.press("Escape");
  if (triggerElement && !(await triggerElement.evaluate((element) => !element.isConnected || document.activeElement === element))) throw new Error(`Create Channel did not restore focus at ${label}`);
}

await page.setViewportSize({ width: 1440, height: 900 });
const channelSlug = `dialog-check-${Date.now().toString(36)}`;
await page.getByRole("button", { name: "Create Channel" }).click();
const createDialog = page.getByRole("dialog", { name: "Create Channel" });
await createDialog.getByLabel("Name").fill("Dialog Check");
await createDialog.getByLabel("Slug").fill(channelSlug);
await createDialog.getByLabel("Visibility").selectOption("private");
await createDialog.getByRole("button", { name: "Create Channel", exact: true }).click();
await page.waitForURL(new RegExp(`/channels/${channelSlug}$`));

for (const [width, height, label] of viewports) {
  await page.setViewportSize({ width, height });
  const trigger = page.getByRole("button", { name: "Add Agents to Channel" });
  const triggerElement = await trigger.elementHandle();
  await trigger.click();
  const dialog = page.getByRole("dialog", { name: "Add Agents" });
  await assertDialog(dialog, "Add Agents", label, dialog.getByRole("checkbox").first());
  if (label === "desktop") {
    await dialog.locator("..").click({ position: { x: 3, y: 3 } });
  } else {
    await page.keyboard.press("Escape");
  }
  await dialog.waitFor({ state: "detached" });
  if (triggerElement && !(await triggerElement.evaluate((element) => !element.isConnected || document.activeElement === element))) throw new Error(`Add Agents did not restore focus at ${label}`);
}

await page.goto(`${base}/s/sumi-dev/computers`);
await page.locator(".computers-workspace").waitFor();
for (const [width, height, label] of viewports) {
  await page.setViewportSize({ width, height });
  await openNavigationWhenCompact();
  const trigger = page.getByRole("link", { name: "Pair Computer" });
  await trigger.click();
  const dialog = page.getByRole("dialog", { name: "Pair Computer" });
  await assertDialog(dialog, "Pair Computer", label, dialog.getByRole("button", { name: "Close Pair Computer" }));
  await page.keyboard.press("Escape");
  await dialog.waitFor({ state: "detached" });
}

for (const [width, height, label] of viewports) {
  await page.setViewportSize({ width, height });
  await page.goto(`${base}/s/sumi-dev/computers#create-agent`);
  const dialog = page.getByRole("dialog", { name: "Create Agent" });
  await assertDialog(dialog, "Create Agent", label, dialog.getByLabel("Agent name"));
  await page.keyboard.press("Escape");
  await dialog.waitFor({ state: "detached" });
}

await page.setViewportSize({ width: 1440, height: 900 });
await page.route("**/api/v1/spaces/*/agents", async (route) => {
  if (route.request().method() !== "POST") return route.continue();
  await route.fulfill({
    status: 409,
    contentType: "application/json",
    body: JSON.stringify({ error: { code: "dialog_check", message: "The test error stays inside this Dialog." } }),
  });
});
await page.goto(`${base}/s/sumi-dev/computers#create-agent`);
const errorDialog = page.getByRole("dialog", { name: "Create Agent" });
await errorDialog.getByLabel("Agent name").fill("Dialog Probe");
await errorDialog.getByLabel("Role").fill("Verify the Dialog error boundary.");
expectingAgentError = true;
await errorDialog.getByRole("button", { name: "Create Agent", exact: true }).click();
await errorDialog.getByRole("alert").waitFor();
expectingAgentError = false;
await assertDialog(errorDialog, "Create Agent Error", "desktop", null);
await page.screenshot({ path: `${output}/create-agent-error-desktop.png` });
await page.keyboard.press("Escape");
await page.unroute("**/api/v1/spaces/*/agents");

await page.goto(`${base}/s/sumi-dev/computers`);
for (const [width, height, label] of viewports) {
  await page.setViewportSize({ width, height });
  const trigger = page.getByRole("button", { name: "Delete" });
  const triggerElement = await trigger.elementHandle();
  await trigger.click();
  const dialog = page.getByRole("dialog", { name: /Delete .+\?/ });
  await assertDialog(dialog, "Delete Computer", label, dialog.getByRole("button", { name: "Close delete confirmation" }));
  const confirm = dialog.getByRole("button", { name: /^Delete / });
  if (!(await confirm.isDisabled())) throw new Error(`Delete Computer is enabled before confirmation at ${label}`);
  const acknowledgement = dialog.getByRole("checkbox", { name: /will be retired/i });
  if (await acknowledgement.count()) {
    await acknowledgement.check();
    if (await confirm.isDisabled()) throw new Error(`Delete Computer stays disabled after confirmation at ${label}`);
  }
  await page.keyboard.press("Escape");
  await dialog.waitFor({ state: "detached" });
  if (triggerElement && !(await triggerElement.evaluate((element) => !element.isConnected || document.activeElement === element))) throw new Error(`Delete Computer did not restore focus at ${label}`);
}

await cleanupDialogChannels();

if (errors.length) throw new Error(errors.join("\n"));
console.log(JSON.stringify({ results, errorState: true, dangerConfirmation: true, errors: errors.length, output }));
await browser.close();
