import { expect, test } from "@playwright/test";

test("completes the responsive Channel, Thread, Members, Computers and Inbox path", async ({ page }, testInfo) => {
  const pageErrors: string[] = [];
  page.on("pageerror", (error) => pageErrors.push(error.stack ?? error.message));
  page.on("console", (message) => {
    if (message.type() === "error") pageErrors.push(message.text());
  });
  const nonce = `${testInfo.project.name.split("-")[0]}-${Date.now().toString(36)}-${Math.random().toString(36).slice(2, 6)}`;
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
  if (testInfo.project.name === "mobile-390") {
    await page.getByRole("button", { name: "Open navigation" }).click();
  }
  await page.getByRole("button", { name: "Create Channel" }).click();
  await expect(page.getByRole("dialog", { name: "Create Channel" })).toBeVisible();
  await expect(page.getByRole("group", { name: "Initial Agents" })).toBeVisible();
  await page.getByRole("button", { name: "Close Create Channel" }).click();
  if (testInfo.project.name === "mobile-390") {
    await page.locator(".navigation-close").click();
  }
  await expect(page.getByLabel("Attach file")).toBeEnabled();
  const longMessage = `A long Message remains readable at ${testInfo.project.name}. https://example.test/${"boundary/".repeat(24)}`;
  const composer = page.getByRole("textbox", { name: "Message", exact: true });
  await composer.fill(longMessage);
  const [messageResponse] = await Promise.all([
    page.waitForResponse((response) => response.request().method() === "POST" && /\/channels\/[^/]+\/messages$/.test(response.url())),
    page.getByRole("button", { name: "Send message", exact: true }).click(),
  ]);
  const messagePayload = await messageResponse.text();
  expect(messageResponse.ok(), messagePayload).toBeTruthy();
  await page.waitForTimeout(100);
  if (pageErrors.length) throw new Error(`${messagePayload}\n${pageErrors.join("\n")}`);
  await expect(composer).toHaveValue("");
  await expect(page.locator("article.message-row").getByText(longMessage)).toBeVisible();
  await page.getByRole("button", { name: "Reply in Thread" }).click();
  await expect(page.getByRole("complementary", { name: /Thread #general:1/ })).toBeVisible();
  await page.getByRole("textbox", { name: "Thread reply", exact: true }).fill("Thread reply keeps the root visible and uses the same Message layout.");
  await page.getByRole("button", { name: "Send Thread reply", exact: true }).click();
  await expect(page.getByText("Thread reply keeps the root visible and uses the same Message layout.")).toBeVisible();
  await page.getByRole("button", { name: "Close Thread" }).click();

  await page.locator('input[type="file"]').first().setInputFiles({
    name: "viewport-proof.txt",
    mimeType: "text/plain",
    buffer: Buffer.from("Attachment bytes stay out of logs."),
  });
  await expect(page.getByLabel("Attachments ready to send").getByText("viewport-proof.txt")).toBeVisible();
  await composer.fill("Attachment remains part of the Message, not a separate Files product.");
  await page.getByRole("button", { name: "Send message", exact: true }).click();
  await expect(page.locator("article.message-row").getByText("viewport-proof.txt")).toBeVisible();

  if (testInfo.project.name === "mobile-390") {
    await page.getByRole("button", { name: "Open navigation" }).click();
  }
  await page.getByRole("link", { name: "Members" }).click();
  await expect(page.locator(".members-header").getByRole("heading", { name: "Members", exact: true })).toBeVisible();
  await expect(page.getByRole("group", { name: "Filter Members by kind" })).toBeVisible();

  if (testInfo.project.name === "mobile-390") {
    await page.getByRole("button", { name: "Open navigation" }).click();
  }
  await page.getByRole("link", { name: "Computers" }).click();
  await expect(page.getByText("No Computer paired")).toBeVisible();

  if (testInfo.project.name === "mobile-390") {
    await page.getByRole("button", { name: "Open navigation" }).click();
    await page.getByRole("complementary", { name: "Space navigation" }).getByRole("link", { name: "Inbox" }).click();
  } else {
    await page.getByRole("complementary", { name: "Space tools" }).getByRole("link", { name: "Inbox" }).click();
  }
  await expect(page.getByRole("heading", { name: "Inbox", exact: true, level: 1 })).toBeVisible();
  await expect(page.getByText("Inbox is clear.")).toBeVisible();
  await expect(page.locator("body")).not.toContainText(/Tasks|As Task|Joint Channels|Chat \/ Tasks \/ Files/);
});
