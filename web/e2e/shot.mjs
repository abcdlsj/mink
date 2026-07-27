import { chromium } from "@playwright/test";
import { mkdirSync } from "node:fs";

const BASE = process.env.SHOT_BASE ?? "http://localhost:5173";
const OUT = "/tmp/sumi-shots";
mkdirSync(OUT, { recursive: true });

const nonce = Date.now().toString(36);
const slug = `shot-${nonce}`;

const browser = await chromium.launch();
const page = await browser.newPage({ viewport: { width: 1440, height: 900 } });
page.on("pageerror", (e) => console.error("PAGEERROR", e.message));

await page.goto(BASE);
await page.getByLabel("Display name").fill("Mara Vance");
await page.getByLabel("Email").fill(`mara-${nonce}@example.test`);
await page.getByLabel("Password").fill("correct-horse-battery");
await page.getByRole("button", { name: "Continue" }).click();

await page.waitForURL(/\/spaces\/new/);
await page.getByLabel("Space name").fill("Building Life");
await page.getByLabel("URL slug").fill(slug);
await page.getByRole("button", { name: "Enter general" }).click();
await page.waitForURL(new RegExp(`/s/${slug}/channels/general`));

const composer = page.getByRole("textbox", { name: "Message", exact: true });
// A burst from the same author (should group) then variety.
const lines = [
  "Kicking off the building-life channel. Here's the plan for this week.",
  "First: wire the timeline grouping so bursts read as one turn.",
  "Second: tabular numbers everywhere we show seq, versions, counts.",
  "Then we tighten the day dividers and the member strip contrast.",
  "@mara let me know if the spacing feels right at 1440 and on mobile.",
];
for (const line of lines) {
  await composer.fill(line);
  await Promise.all([
    page.waitForResponse((r) => r.request().method() === "POST" && /\/channels\/[^/]+\/messages$/.test(r.url())),
    page.getByRole("button", { name: "Send message", exact: true }).click(),
  ]);
}
await page.getByRole("button", { name: "Reply in Thread" }).first().click();
const threadComposer = page.getByRole("textbox", { name: "Thread reply", exact: true });
for (const reply of [
  "The denser rhythm works. Keep the root and these replies visually connected.",
  "Agent marks should stay abstract; no faces or robot silhouettes.",
  "Permission controls need one consistent 36px baseline across the directory.",
]) {
  await threadComposer.fill(reply);
  await Promise.all([
    page.waitForResponse((response) => response.request().method() === "POST" && /\/threads\/\d+\/messages$/.test(response.url())),
    page.getByRole("button", { name: "Send Thread reply", exact: true }).click(),
  ]);
  await threadComposer.waitFor({ state: "visible" });
}
await page.getByRole("button", { name: "Close Thread" }).click();
await page.waitForTimeout(300);

for (const [w, h, tag] of [[1440, 900, "desktop"], [1024, 768, "tablet"], [390, 844, "mobile"]]) {
  await page.setViewportSize({ width: w, height: h });
  await page.waitForTimeout(200);
  await page.screenshot({ path: `${OUT}/channel-${tag}.png`, fullPage: false });
}

// Members page for the control sizing and responsive permission treatment.
await page.goto(`${BASE}/s/${slug}/members`);
for (const [w, h, tag] of [[1440, 900, "desktop"], [1024, 768, "tablet"], [390, 844, "mobile"]]) {
  await page.setViewportSize({ width: w, height: h });
  await page.waitForTimeout(200);
  await page.screenshot({ path: `${OUT}/members-${tag}.png`, fullPage: false });
}

console.log("SLUG", slug);
await browser.close();
