import { expect, test } from "@playwright/test";

test("completes the responsive Channel, Thread, Members, Computers and Inbox path", async ({ page }, testInfo) => {
  const pageErrors: string[] = [];
  const openResponsiveNavigation = async () => {
    const triggers = page.getByRole("button", { name: "Open navigation" });
    for (let index = 0; index < await triggers.count(); index += 1) {
      const trigger = triggers.nth(index);
      if (await trigger.isVisible()) {
        await trigger.click();
        return true;
      }
    }
    return false;
  };
  const closeResponsiveNavigation = async () => {
    const close = page.getByRole("complementary", { name: "Space navigation" }).getByRole("button", { name: "Close navigation" });
    if (await close.isVisible()) await close.click();
  };
  const navigateToSpaceTool = async (name: "Members" | "Computers") => {
    const link = page.getByRole("link", { name, exact: true });
    if (await link.isVisible()) {
      await link.click();
      return;
    }
    if (await openResponsiveNavigation() && (await link.isVisible())) {
      await link.click();
      return;
    }
    await page.goto(`/s/${slug}/${name.toLowerCase()}`);
  };
  page.on("pageerror", (error) => pageErrors.push(error.stack ?? error.message));
  page.on("console", (message) => {
    if (message.type() === "error") pageErrors.push(message.text());
  });
  page.on("response", (response) => {
    if (!response.ok()) pageErrors.push(`${response.status()} ${response.request().method()} ${response.url()}`);
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
  if (await openResponsiveNavigation()) {
    await page.getByRole("button", { name: "Create Channel" }).click();
    await expect(page.getByRole("dialog", { name: "Create Channel" })).toBeVisible();
    await expect(page.getByRole("group", { name: "Initial Agents" })).toBeVisible();
    await page.getByRole("button", { name: "Close Create Channel" }).click();
    await closeResponsiveNavigation();
  }
  await expect(page.getByLabel("Attach file")).toBeEnabled();
  const longMessage = `A long Message remains readable at ${testInfo.project.name}. https://example.test/${"boundary/".repeat(24)}`;
  const composer = page.locator('textarea[aria-label="Message"]');
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
  const rootMessage = page.locator("article.message-row").filter({ hasText: longMessage });
  await expect(rootMessage).toBeVisible();
  // Above the narrow breakpoint the action panel is revealed on hover or focus, so it has no pointer
  // events until the Message row is hovered.
  await rootMessage.hover();
  await rootMessage.getByRole("button", { name: "Reply in Thread" }).click();
  await expect(page.getByRole("complementary", { name: /Thread #general:1/ })).toBeVisible();
  await page.locator('textarea[aria-label="Thread reply"]').fill("Thread reply keeps the root visible and uses the same Message layout.");
  await page.getByRole("button", { name: "Send Thread reply", exact: true }).click();
  await expect(page.getByRole("complementary", { name: /Thread #general:1/ }).getByText("Thread reply keeps the root visible and uses the same Message layout.")).toBeVisible();
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

  await navigateToSpaceTool("Members");
  await expect(page.locator(".members-header").getByRole("heading", { name: "Members", exact: true })).toBeVisible();
  await expect(page.getByRole("group", { name: "Filter Members by kind" })).toBeVisible();

  await navigateToSpaceTool("Computers");
  await expect(page.getByRole("heading", { name: "Pair a Computer", exact: true })).toBeVisible();

  const railInbox = page.getByRole("complementary", { name: "Space tools" }).getByRole("link", { name: "Inbox" });
  const navigationInbox = page.getByRole("complementary", { name: "Space navigation" }).getByRole("link", { name: "Inbox" });
  if (await railInbox.isVisible()) {
    await railInbox.click();
  } else {
    if (await openResponsiveNavigation() && (await navigationInbox.isVisible())) {
      await navigationInbox.click();
    } else {
      await page.goto(`/s/${slug}/inbox`);
    }
  }
  await expect(page.getByRole("heading", { name: "Inbox", exact: true, level: 1 })).toBeVisible();
  await expect(page.getByRole("heading", { name: "Nothing needs your attention" })).toBeVisible();
  await expect(page.getByText(/not your Message history/i)).toBeVisible();
  await expect(page.locator("body")).not.toContainText(/As Task|Joint Channels|Chat \/ Tasks \/ Files/);
});
