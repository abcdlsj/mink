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

async function composerGeometry(locator) {
  return locator.evaluate((form) => {
    const input = form.querySelector("textarea");
    const attach = form.querySelector(":scope > .icon-button");
    const send = form.querySelector(".send-button");
    if (!input || !attach || !send) throw new Error("composer controls are incomplete");
    const formBox = form.getBoundingClientRect();
    const inputBox = input.getBoundingClientRect();
    const attachBox = attach.getBoundingClientRect();
    const sendBox = send.getBoundingClientRect();
    const parent = form.parentElement;
    const parentBox = parent.getBoundingClientRect();
    const parentRightBorder = parent.offsetWidth - parent.clientWidth - parent.clientLeft;
    const threadPane = parent.matches(".channel-workspace")
      ? parent.querySelector(":scope > .thread-pane")
      : null;
    const contentRight = threadPane
      ? threadPane.getBoundingClientRect().left
      : parentBox.right - parentRightBorder;
    return {
      top: Math.round(formBox.top),
      bottom: Math.round(formBox.bottom),
      height: Math.round(formBox.height),
      leftGap: Math.round(formBox.left - parentBox.left - parent.clientLeft),
      rightGap: Math.round(contentRight - formBox.right),
      inputHeight: Math.round(inputBox.height),
      attach: [Math.round(attachBox.width), Math.round(attachBox.height), Math.round(attachBox.y - inputBox.y)],
      send: [Math.round(sendBox.width), Math.round(sendBox.height), Math.round(sendBox.y - inputBox.y)],
      fontSize: getComputedStyle(input).fontSize,
    };
  });
}

await page.goto(`${base}/login`);
await page.getByLabel("Email").fill("dev@example.test");
await page.getByLabel("Password").fill("correct horse battery staple");
await page.getByRole("button", { name: "Sign in" }).click();
await page.waitForURL(/\/spaces\/new/);
await page.goto(`${base}/s/sumi-dev/channels/general`);
await page.locator(".channel-workspace").waitFor();

if (await page.locator(".inline-thread-preview").count() === 0) {
  await page.getByRole("button", { name: "Reply in Thread" }).first().click();
  const composer = page.locator('textarea[aria-label="Thread reply"]');
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
  let box = await pane.boundingBox();
  const resizeHandle = page.getByRole("separator", { name: "Resize Thread pane" });
  const handleVisible = await resizeHandle.isVisible();
  let resizedPaneWidth = null;
  let keyboardResetWidth = null;
  if (label === "mobile") {
    if (handleVisible) throw new Error("mobile Thread resize handle must not be visible");
  } else {
    if (!handleVisible) throw new Error(`${label} Thread resize handle is not visible`);
    const channelBox = await page.locator(".channel-header").boundingBox();
    if (!channelBox || Math.round(channelBox.width) < 480) {
      throw new Error(`${label} Channel became narrower than 480px: ${JSON.stringify(channelBox)}`);
    }
    await resizeHandle.press("End");
    box = await pane.boundingBox();
    resizedPaneWidth = box && Math.round(box.width);
    const maximum = Number(await resizeHandle.getAttribute("aria-valuemax"));
    if (resizedPaneWidth !== maximum || maximum > 480) {
      throw new Error(`${label} keyboard maximum is invalid: ${JSON.stringify({ resizedPaneWidth, maximum })}`);
    }
    await page.screenshot({ path: `${output}/pane-${label}-max.png` });
    await resizeHandle.press("Home");
    box = await pane.boundingBox();
    keyboardResetWidth = box && Math.round(box.width);
    if (keyboardResetWidth !== 360) throw new Error(`${label} Home did not restore 360px: ${keyboardResetWidth}`);
    if (label === "desktop") {
      const handleBox = await resizeHandle.boundingBox();
      if (!handleBox) throw new Error("desktop Thread resize handle has no geometry");
      const pointerX = handleBox.x + handleBox.width / 2;
      await page.mouse.move(pointerX, handleBox.y + handleBox.height / 2);
      await page.mouse.down();
      await page.mouse.move(pointerX - 80, handleBox.y + handleBox.height / 2);
      await page.mouse.up();
      box = await pane.boundingBox();
      if (!box || Math.round(box.width) !== 440) throw new Error(`desktop pointer resize failed: ${JSON.stringify(box)}`);
    }
  }
  const channelComposer = await composerGeometry(page.locator(".channel-workspace > .composer"));
  const threadComposer = await composerGeometry(page.locator(".thread-composer"));
  if (JSON.stringify(channelComposer) !== JSON.stringify(threadComposer)) {
    throw new Error(`${label} composers diverge: ${JSON.stringify({ channelComposer, threadComposer })}`);
  }
  if (threadComposer.fontSize !== "14px") throw new Error(`${label} Thread placeholder is not 14px`);
  await page.screenshot({ path: `${output}/pane-${label}.png` });
  const overflow = await page.evaluate(() => document.documentElement.scrollWidth > window.innerWidth);
  if (label === "mobile") {
    if (!box || Math.round(box.x) !== 0 || Math.round(box.width) !== width) throw new Error(`mobile pane is not full screen: ${JSON.stringify(box)}`);
  } else if (!box || box.x <= 0 || box.width >= width) {
    throw new Error(`${label} pane does not preserve the Channel: ${JSON.stringify(box)}`);
  }
  await page.getByRole("button", { name: "Close Thread" }).click();
  const after = await timeline.evaluate((element) => element.scrollTop);
  results.push({ label, overflow, scrollRestored: before === after, composer: threadComposer, pane: box && { x: Math.round(box.x), width: Math.round(box.width) }, handleVisible, resizedPaneWidth, keyboardResetWidth });
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

const channelComposer = page.locator('textarea[aria-label="Message"]');
await channelComposer.fill("Thread context root.");
await Promise.all([
  page.waitForResponse((response) => response.request().method() === "POST" && /\/channels\/[^/]+\/messages$/.test(response.url())),
  page.getByRole("button", { name: "Send message", exact: true }).click(),
]);
await page.getByRole("button", { name: "Reply in Thread" }).click();
const threadComposer = page.locator('textarea[aria-label="Thread reply"]');
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
