import { expect, test, type Page } from "@playwright/test";

async function settleMotion(page: Page) {
  await page.evaluate(async () => {
    await Promise.all(
      document
        .getAnimations()
        .map((animation) => animation.finished.catch(() => {})),
    );
  });
}

for (const viewport of [
  { width: 1440, height: 900 },
  { width: 1024, height: 768 },
]) {
  test(`conversation shell is stable at ${viewport.width}x${viewport.height}`, async ({
    page,
  }) => {
    await page.setViewportSize(viewport);
    await page.goto("/");
    await expect(page.getByText("No conversations yet")).toBeVisible();
    await expect(page.getByText(/Server [a-f0-9]{8}/)).toBeVisible();
    await expect(page.getByLabel("Context", { exact: true })).toBeHidden();
    await expect(page.getByText("No context selected")).toBeHidden();

    const hasOverflow = await page.evaluate(
      () =>
        document.documentElement.scrollWidth >
        document.documentElement.clientWidth,
    );
    expect(hasOverflow).toBe(false);

    await expect(page.getByTestId("main-composer")).toBeVisible();
    await expect(page.getByTestId("context-composer")).toHaveCount(0);
    await settleMotion(page);

    await page.screenshot({
      path: `../test-results/sumi-${viewport.width}x${viewport.height}.png`,
      fullPage: true,
    });

    await page.keyboard.press("Tab");
    await expect(page.getByRole("button", { name: "Chat" })).toBeFocused();
    const focusOutline = await page
      .getByRole("button", { name: "Chat" })
      .evaluate((element) => getComputedStyle(element).outlineStyle);
    expect(focusOutline).not.toBe("none");

    await page.getByRole("button", { name: "Open context" }).click();
    const conversation = page.getByRole("region", {
      name: "Conversation",
      exact: true,
    });
    const context = page.getByRole("complementary", {
      name: "Context",
      exact: true,
    });
    await expect(conversation).toBeVisible();
    await expect(context).toBeVisible();
    await expect(page.getByText("No context selected")).toBeVisible();

    const mainComposer = await page.getByTestId("main-composer").boundingBox();
    const contextPane = await context.boundingBox();
    expect(mainComposer).not.toBeNull();
    expect(contextPane).not.toBeNull();
    expect(mainComposer!.x + mainComposer!.width).toBeLessThanOrEqual(
      contextPane!.x,
    );
    await settleMotion(page);

    await page.screenshot({
      path: `../test-results/sumi-context-open-${viewport.width}x${viewport.height}.png`,
      fullPage: true,
    });

    await page.getByRole("button", { name: "Close context" }).click();
    await expect(context).toBeHidden();
  });
}

test("offline bootstrap offers retry", async ({ page }) => {
  let attempts = 0;
  let releaseRetry!: () => void;
  await page.route(
    "**/sumi.system.v1.SystemService/GetBootstrap",
    async (route) => {
      attempts += 1;
      if (attempts > 1) {
        await new Promise<void>((resolve) => {
          releaseRetry = resolve;
        });
      }
      await route.abort();
    },
  );
  await page.goto("/");
  await expect(page.getByRole("alert")).toContainText("Server unavailable");
  await page.getByRole("button", { name: "Retry" }).click();
  await expect.poll(() => attempts).toBeGreaterThan(1);
  await expect(page.getByRole("button", { name: "Retrying" })).toBeDisabled();
  await expect(page.getByRole("alert")).toContainText("Server unavailable");
  await expect(page.getByText("No conversations yet")).toHaveCount(0);
  await page.screenshot({
    path: "../test-results/sumi-offline-1440x900.png",
    fullPage: true,
  });
  releaseRetry();
});

for (const width of [900, 1023]) {
  test(`compact context is a reachable single pane at ${width}px`, async ({
    page,
  }) => {
    await page.setViewportSize({ width, height: 700 });
    await page.goto("/");

    const conversation = page.getByRole("region", {
      name: "Conversation",
      exact: true,
    });
    const context = page.getByRole("complementary", {
      name: "Context",
      exact: true,
    });
    await expect(conversation).toBeVisible();
    await expect(context).toBeHidden();
    await expect(page.getByTestId("main-composer")).toBeVisible();
    await expect(page.getByTestId("context-composer")).toHaveCount(0);

    const hasOverflow = await page.evaluate(
      () =>
        document.documentElement.scrollWidth >
        document.documentElement.clientWidth,
    );
    expect(hasOverflow).toBe(false);

    await settleMotion(page);
    await page.screenshot({
      path: `../test-results/sumi-compact-${width}x700.png`,
      fullPage: true,
    });

    await page.getByRole("button", { name: "Open context" }).click();

    await expect(conversation).toBeHidden();
    await expect(context).toBeVisible();
    await expect(page.getByTestId("main-composer")).toBeHidden();
    await expect(page.getByText("No context selected")).toBeVisible();
    await expect(page.getByTestId("context-composer")).toHaveCount(0);

    await settleMotion(page);
    await page.screenshot({
      path: `../test-results/sumi-compact-context-${width}x700.png`,
      fullPage: true,
    });

    await page.getByRole("button", { name: "Back to conversation" }).click();

    await expect(conversation).toBeVisible();
    await expect(context).toBeHidden();
    await expect(page.getByTestId("main-composer")).toBeVisible();
    await expect(page.getByTestId("context-composer")).toHaveCount(0);
  });
}
