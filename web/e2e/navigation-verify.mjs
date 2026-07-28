import { chromium } from "@playwright/test";
import { mkdirSync } from "node:fs";

const base = "http://127.0.0.1:5173";
const output = "/tmp/sumi-shots-navigation";
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

const results = [];
const navigation = page.getByRole("complementary", { name: "Space navigation" });
const close = navigation.getByRole("button", { name: "Close navigation" });
const railOpen = page.getByRole("complementary", { name: "Space tools" }).getByRole("button", { name: "Open navigation" });

const expandedNavigation = await navigation.boundingBox();
const closeButton = await close.boundingBox();
if (!expandedNavigation || Math.round(expandedNavigation.width) !== 294) {
  throw new Error(`desktop navigation width is not 294px: ${JSON.stringify(expandedNavigation)}`);
}
if (!closeButton || Math.round(closeButton.width) !== 26 || Math.round(closeButton.height) !== 26) {
  throw new Error(`navigation close is not compact: ${JSON.stringify(closeButton)}`);
}
await close.click();
await navigation.waitFor({ state: "hidden" });
const collapsedChannel = await page.locator(".channel-workspace").boundingBox();
const desktopReturnFocus = await railOpen.evaluate((element) => document.activeElement === element);
if (!collapsedChannel || Math.round(collapsedChannel.x) !== 64 || !desktopReturnFocus) {
  throw new Error(`desktop collapse did not release the navigation column: ${JSON.stringify({ collapsedChannel, desktopReturnFocus })}`);
}
await page.screenshot({ path: `${output}/desktop-collapsed.png` });
await railOpen.click();
await navigation.waitFor({ state: "visible" });
await page.screenshot({ path: `${output}/desktop-expanded.png` });
results.push({ viewport: "1440x900", close: 26, collapsedChannelX: Math.round(collapsedChannel.x), returnFocus: desktopReturnFocus });

await close.click();
await page.setViewportSize({ width: 1024, height: 768 });
await railOpen.click();
const tabletNavigation = await navigation.boundingBox();
if (!tabletNavigation || Math.round(tabletNavigation.x) !== 52) {
  throw new Error(`tablet drawer did not open from the Space rail: ${JSON.stringify(tabletNavigation)}`);
}
await page.screenshot({ path: `${output}/tablet-open.png` });
await close.click();
await navigation.waitFor({ state: "hidden" });
results.push({ viewport: "1024x768", drawerX: Math.round(tabletNavigation.x), closed: true });

await page.setViewportSize({ width: 390, height: 844 });
const headerOpen = page.getByRole("button", { name: "Open navigation" });
await headerOpen.click();
const mobileNavigation = await navigation.boundingBox();
if (!mobileNavigation || Math.round(mobileNavigation.x) !== 0) {
  throw new Error(`mobile drawer is not full-left: ${JSON.stringify(mobileNavigation)}`);
}
await page.screenshot({ path: `${output}/mobile-open.png` });
await close.click();
await navigation.waitFor({ state: "hidden" });
const mobileReturnFocus = await headerOpen.evaluate((element) => document.activeElement === element);
results.push({ viewport: "390x844", drawerX: Math.round(mobileNavigation.x), returnFocus: mobileReturnFocus });

const overflow = await page.evaluate(() => document.documentElement.scrollWidth > window.innerWidth);
if (overflow || errors.length) throw new Error(JSON.stringify({ overflow, errors }));
console.log(JSON.stringify({ results, overflow, errors: errors.length, output }));
await browser.close();
