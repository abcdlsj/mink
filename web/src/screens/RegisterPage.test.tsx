import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { RouterProvider, createMemoryHistory } from "@tanstack/react-router";
import { render, screen } from "@testing-library/react";
import { describe, expect, it } from "vitest";

import { createAppRouter } from "../router";

describe("RegisterPage", () => {
  it("renders the Human registration fields", async () => {
    const router = createAppRouter(createMemoryHistory({ initialEntries: ["/"] }));
    await router.load();
    render(
      <QueryClientProvider client={new QueryClient()}>
        <RouterProvider router={router} />
      </QueryClientProvider>,
    );

    expect(await screen.findByRole("heading", { name: "Join the room." })).toBeVisible();
    expect(screen.getByLabelText("Display name")).toBeRequired();
    expect(screen.getByLabelText("Email")).toHaveAttribute("type", "email");
    expect(screen.getByLabelText("Password")).toHaveAttribute("minLength", "10");
    expect(screen.getByRole("button", { name: /continue/i })).toBeVisible();
  });
});
