import { chromium } from "@playwright/test";
import { mkdirSync } from "node:fs";

const base = "http://127.0.0.1:5173";
const output = "/tmp/sumi-dm-verify";
const viewports = [[1440, 900, "desktop"], [1024, 768, "tablet"], [390, 844, "mobile"]];
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

const results = [];
for (const [width, height, label] of viewports) {
  await page.setViewportSize({ width, height });
  await page.goto(`${base}/s/sumi-dev/channels/general`);
  await page.locator(".channel-workspace").waitFor();

  const startDm = page.getByRole("button", { name: "Start DM" });
  if (!(await startDm.isVisible())) {
    const navigationTriggers = page.getByRole("button", { name: "Open navigation" });
    for (let index = 0; index < await navigationTriggers.count(); index += 1) {
      if (await navigationTriggers.nth(index).isVisible()) {
        await navigationTriggers.nth(index).click();
        break;
      }
    }
  }

  await startDm.click();
  const dialog = page.getByRole("dialog", { name: "Start DM" });
  await dialog.waitFor();
  if (await dialog.getByText("@sumi-dev", { exact: true }).count()) {
    throw new Error(`${label} DM picker includes the current Member`);
  }
  await dialog.getByLabel("Find a Member").fill("iris");
  const iris = dialog.getByRole("button", { name: /Iris avatar.*@iris/i });
  await iris.waitFor();

  const dialogBox = await dialog.boundingBox();
  if (
    !dialogBox ||
    dialogBox.x < 0 ||
    dialogBox.y < 0 ||
    dialogBox.x + dialogBox.width > width ||
    dialogBox.y + dialogBox.height > height
  ) {
    throw new Error(`${label} DM picker escaped the viewport: ${JSON.stringify(dialogBox)}`);
  }
  await page.screenshot({ path: `${output}/${label}-picker.png`, fullPage: true });

  await iris.click();
  await page.waitForURL(/\/s\/sumi-dev\/dm\//);
  await page.getByRole("heading", { name: "Iris", exact: true }).waitFor();
  const placeholder = await page.locator('textarea[aria-label="Message"]').getAttribute("placeholder");
  const overflow = await page.evaluate(() => document.documentElement.scrollWidth > window.innerWidth);
  if (overflow) throw new Error(`${label} DM page has horizontal overflow`);
  await page.screenshot({ path: `${output}/${label}-dm.png`, fullPage: true });
  results.push({ viewport: `${width}x${height}`, placeholder, overflow });
}

if (errors.length) throw new Error(JSON.stringify(errors));
console.log(JSON.stringify({ results, errors: errors.length, output }));
await browser.close();
