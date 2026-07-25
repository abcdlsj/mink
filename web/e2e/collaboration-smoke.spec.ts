import { expect, test } from "@playwright/test";

test("registers, creates a Space, opens general and Inbox", async ({ page }) => {
  const nonce = Date.now().toString(36);
  const slug = `sumi-${nonce}`;
  await page.goto("/");
  await page.getByLabel("Display name").fill("Phase Two Human");
  await page.getByLabel("Email").fill(`phase-two-${nonce}@example.test`);
  await page.getByLabel("Password").fill("correct-horse-phase-two");
  await page.getByRole("button", { name: "Continue" }).click();

  await expect(page).toHaveURL(/\/spaces\/new/);
  await page.getByLabel("Space name").fill("Sumi Collaboration Lab");
  await page.getByLabel("URL slug").fill(slug);
  await page.getByRole("button", { name: "Enter general" }).click();

  await expect(page).toHaveURL(new RegExp(`/s/${slug}/channels/general`));
  await expect(page.getByRole("heading", { name: "#general", exact: true })).toBeVisible();
  await expect(page.getByLabel("Attach file")).toBeEnabled();
  await page.getByRole("link", { name: "Inbox" }).click();
  await expect(page.getByRole("heading", { name: "Inbox", exact: true })).toBeVisible();
  await expect(page.getByText("Inbox is clear.")).toBeVisible();
});
