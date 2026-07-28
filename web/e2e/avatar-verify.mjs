import { chromium } from "@playwright/test";
import { mkdirSync } from "node:fs";

const base = "http://127.0.0.1:5173";
const output = "/tmp/sumi-shots-avatar";
const viewports = [[1440, 900, "desktop"], [1024, 768, "tablet"], [390, 844, "mobile"]];
mkdirSync(output, { recursive: true });

const browser = await chromium.launch();
const page = await browser.newPage({ viewport: { width: 1440, height: 900 } });
const errors = [];
const expectedByName = new Map();
page.on("pageerror", (error) => errors.push(`page:${error.message}`));
page.on("console", (message) => {
  if (message.type() === "error") errors.push(`console:${message.text()}`);
});

await page.goto(`${base}/login`);
await page.getByLabel("Email").fill("dev@example.test");
await page.getByLabel("Password").fill("correct horse battery staple");
await page.getByRole("button", { name: "Sign in" }).click();
await page.waitForURL(/\/spaces\/new/);

async function verifyIdenticons(route, readySelector) {
  await page.goto(`${base}${route}`);
  await page.locator(readySelector).waitFor();
  const avatars = await page.locator("[data-agent-identicon]").evaluateAll((nodes) => nodes.map((node) => ({
    name: node.getAttribute("aria-label")?.replace(/ avatar$/, "") ?? "",
    signature: node.getAttribute("data-agent-identicon") ?? "",
    hasSvg: Boolean(node.querySelector("svg[viewBox='0 0 8 8']")),
  })));
  if (avatars.length < 3) throw new Error(`${route} did not render the seeded Agents`);
  for (const avatar of avatars) {
    if (!avatar.name || !avatar.signature || !avatar.hasSvg) throw new Error(`${route} rendered an incomplete Agent identicon`);
    const expected = expectedByName.get(avatar.name);
    if (expected && expected !== avatar.signature) throw new Error(`${avatar.name} changed identicon between pages`);
    expectedByName.set(avatar.name, avatar.signature);
  }
  const distinctAgents = new Map(avatars.map((avatar) => [avatar.name, avatar.signature]));
  if (new Set(distinctAgents.values()).size !== distinctAgents.size) {
    throw new Error(`${route} rendered colliding identicons: ${JSON.stringify([...distinctAgents])}`);
  }
  const overflow = await page.evaluate(() => document.documentElement.scrollWidth > window.innerWidth);
  if (overflow) throw new Error(`${route} overflows the viewport`);
  return { agents: distinctAgents.size, signatures: new Set(distinctAgents.values()).size, overflow };
}

const results = [];
for (const [width, height, label] of viewports) {
  await page.setViewportSize({ width, height });
  const channel = await verifyIdenticons("/s/sumi-dev/channels/general", ".channel-workspace");
  await page.screenshot({ path: `${output}/channel-${label}.png` });
  const members = await verifyIdenticons("/s/sumi-dev/members", ".members-workspace");
  await page.screenshot({ path: `${output}/members-${label}.png` });
  const computers = await verifyIdenticons("/s/sumi-dev/computers", ".computers-workspace");
  await page.screenshot({ path: `${output}/computers-${label}.png` });
  results.push({ viewport: `${width}x${height}`, channel, members, computers });
}

if (errors.length) throw new Error(errors.join("\n"));
console.log(JSON.stringify({ results, stableAgents: expectedByName.size, errors: errors.length, output }));
await browser.close();
