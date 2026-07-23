import { expect, test, type Page } from "@playwright/test";

async function settleMotion(page: Page) {
  await page.mouse.move(0, 0);
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
  test(`unauthenticated shell is stable at ${viewport.width}x${viewport.height}`, async ({
    page,
  }) => {
    await page.setViewportSize(viewport);
    await page.goto("/");
    await expect(
      page.getByRole("heading", {
        name: /Set up local access|Sign in to Sumi/,
      }),
    ).toBeVisible();
    await expect(page.getByText(/Server [a-f0-9]{8}/)).toBeVisible();
    await expect(
      page.getByRole("complementary", { name: "Context", exact: true }),
    ).toBeHidden();
    await expect(
      page.getByRole("button", { name: "Open context" }),
    ).toBeDisabled();
    await expect(page.getByTestId("main-composer")).toBeVisible();
    await expect(page.getByTestId("thread-composer")).toHaveCount(0);
    expect(await hasPageOverflow(page)).toBe(false);

    await settleMotion(page);
    await page.screenshot({
      path: `../test-results/sumi-unauthenticated-${viewport.width}x${viewport.height}.png`,
      fullPage: true,
    });

    await page.keyboard.press("Tab");
    await expect(page.getByRole("button", { name: "Chat" })).toBeFocused();
    const focusOutline = await page
      .getByRole("button", { name: "Chat" })
      .evaluate((element) => getComputedStyle(element).outlineStyle);
    expect(focusOutline).not.toBe("none");
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
  await expect(page.getByText("No conversation selected")).toHaveCount(0);
  await page.screenshot({
    path: "../test-results/sumi-offline-1440x900.png",
    fullPage: true,
  });
  releaseRetry();
});

test("icon tooltips work by pointer and keyboard while dark theme persists", async ({
  page,
}) => {
  await page.emulateMedia({ colorScheme: "light", reducedMotion: "reduce" });
  await page.setViewportSize({ width: 1024, height: 768 });
  await page.goto("/");

  const chat = page.getByRole("button", { name: "Chat" });
  await chat.hover();
  await expect
    .poll(() =>
      chat.evaluate((element) => getComputedStyle(element, "::after").opacity),
    )
    .toBe("1");
  expect(
    await chat.evaluate(
      (element) => getComputedStyle(element, "::after").content,
    ),
  ).toContain("Chat");

  await page.mouse.move(500, 500);
  await page.keyboard.press("Tab");
  await expect(chat).toBeFocused();
  await expect
    .poll(() =>
      chat.evaluate((element) => getComputedStyle(element, "::after").opacity),
    )
    .toBe("1");

  await page.getByRole("button", { name: "Use dark theme" }).click();
  await expect(page.locator("html")).toHaveAttribute("data-theme", "dark");
  expect(await page.evaluate(() => localStorage.getItem("sumi.theme"))).toBe(
    "dark",
  );
  await page.reload();
  await expect(page.locator("html")).toHaveAttribute("data-theme", "dark");
  await expect(
    page.getByRole("button", { name: "Use light theme" }),
  ).toBeVisible();
  expect(await hasPageOverflow(page)).toBe(false);

  await settleMotion(page);
  await page.screenshot({
    path: "../test-results/sumi-dark-1024x768.png",
    fullPage: true,
  });
});

for (const width of [900, 1023]) {
  test(`compact unauthenticated shell keeps navigation single-pane and indicators distinct at ${width}px`, async ({
    page,
  }) => {
    await page.setViewportSize({ width, height: 700 });
    await page.goto("/");
    const conversation = page.getByRole("region", {
      name: "Conversation",
      exact: true,
    });
    const navigation = page.getByRole("complementary", {
      name: "Conversation navigation",
    });
    await expect(conversation).toBeVisible();
    await expect(navigation).toBeHidden();
    await expect(
      page.getByRole("button", { name: "Open context" }),
    ).toBeDisabled();

    const sessionIcon = await page
      .locator(".session-indicator svg")
      .getAttribute("class");
    const serverIcon = await page
      .locator(".server-indicator svg")
      .getAttribute("class");
    expect(sessionIcon).toContain("lucide-user-round");
    expect(serverIcon).toContain("lucide-server");
    expect(sessionIcon).not.toBe(serverIcon);
    await expect(page.locator(".session-indicator")).toHaveAttribute(
      "aria-label",
      /Human signed out/,
    );
    await expect(page.locator(".server-indicator")).toHaveAttribute(
      "aria-label",
      /Server/,
    );
    expect(await hasPageOverflow(page)).toBe(false);

    await page.getByRole("button", { name: "Open navigation" }).click();
    await expect(conversation).toBeHidden();
    await expect(navigation).toBeVisible();
    await page.getByRole("button", { name: "Collapse navigation" }).click();
    await expect(conversation).toBeVisible();
    await expect(navigation).toBeHidden();
    expect(await hasPageOverflow(page)).toBe(false);

    await settleMotion(page);
    await page.screenshot({
      path: `../test-results/sumi-compact-unauthenticated-${width}x700.png`,
      fullPage: true,
    });
  });
}

async function hasPageOverflow(page: Page) {
  return page.evaluate(
    () =>
      document.documentElement.scrollWidth >
      document.documentElement.clientWidth,
  );
}
