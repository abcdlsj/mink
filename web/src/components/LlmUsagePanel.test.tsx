import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { cleanup, fireEvent, render, screen, within } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";

import type { LlmUsage } from "../api/client";
import { LlmUsagePanel } from "./LlmUsagePanel";

const computerId = "019c0000-0000-7000-8000-000000000001";

const series = [
  { bucket: "2026-08-01T00:00:00Z", requests: 6, input_tokens: 22_000, output_tokens: 3_000, cached_input_tokens: 15_000 },
  { bucket: "2026-08-01T01:00:00Z", requests: 6, input_tokens: 26_000, output_tokens: 4_500, cached_input_tokens: 16_000 },
];

const usage: LlmUsage = {
  requests: 12,
  input_tokens: 48_000,
  output_tokens: 7_500,
  cached_input_tokens: 31_000,
  cache_write_tokens: 4_000,
  cache_hit_rate: 0.646,
  first_at: "2026-08-01T00:00:00Z",
  last_at: "2026-08-01T02:00:00Z",
  series,
  by_model: [
    { key: "deepseek-v4-pro", requests: 12, input_tokens: 48_000, output_tokens: 7_500, cached_input_tokens: 31_000 },
  ],
  by_agent: [
    { key: "019c0000-0000-7000-8000-000000000010", requests: 12, input_tokens: 48_000, output_tokens: 7_500, cached_input_tokens: 31_000 },
  ],
  by_agent_series: [
    { agent_id: "019c0000-0000-7000-8000-000000000010", requests: 12, input_tokens: 48_000, output_tokens: 7_500, cached_input_tokens: 31_000, series },
  ],
};

function json(value: unknown, status = 200): Response {
  return new Response(JSON.stringify(value), {
    status,
    headers: { "Content-Type": "application/json" },
  });
}

function renderPanel(overrides: { online?: boolean; data?: LlmUsage } = {}) {
  const fetchMock = vi.fn(async (input: RequestInfo | URL) => {
    const path = String(input);
    if (path.includes(`/computers/${computerId}/llm-usage`)) {
      return overrides.data === undefined ? json(usage) : json(overrides.data);
    }
    throw new Error(`Unexpected request: ${path}`);
  });
  vi.stubGlobal("fetch", fetchMock);
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  const view = render(
    <QueryClientProvider client={client}>
      <LlmUsagePanel computerId={computerId} online={overrides.online ?? true} />
    </QueryClientProvider>,
  );
  return { fetchMock, view };
}

afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
});

describe("LlmUsagePanel", () => {
  it("renders summary cards, the token curve, and breakdown tables", async () => {
    renderPanel();

    const summary = await screen.findByLabelText("LLM usage summary");
    expect(within(summary).getByText("Requests")).toBeVisible();
    expect(within(summary).getByText("12")).toBeVisible();
    expect(within(summary).getByText("48.0k")).toBeVisible();
    expect(within(summary).getByText("7.5k")).toBeVisible();
    expect(within(summary).getByText("65%")).toBeVisible();
    expect(screen.getByRole("img", { name: /Token usage curve/ })).toBeVisible();
    expect(await screen.findByRole("heading", { name: "By model" })).toBeVisible();
    expect(screen.getByRole("heading", { name: "By agent" })).toBeVisible();
    expect(screen.getByText("deepseek-v4-pro")).toBeVisible();
  });

  it("switches the queried period", async () => {
    const { fetchMock } = renderPanel();
    await screen.findByLabelText("LLM usage summary");

    fireEvent.click(screen.getByRole("button", { name: "7d" }));

    await vi.waitFor(() => {
      expect(fetchMock).toHaveBeenCalledWith(
        expect.stringContaining("/llm-usage?range=7d"),
        expect.anything(),
      );
    });
  });

  it("shows an empty state when no usage was recorded", async () => {
    renderPanel({
      data: {
        ...usage,
        requests: 0,
        input_tokens: 0,
        output_tokens: 0,
        cached_input_tokens: 0,
        cache_write_tokens: 0,
        cache_hit_rate: 0,
        series: [],
        by_model: [],
        by_agent: [],
      },
    });

    expect(await screen.findByText(/No LLM usage recorded/)).toBeVisible();
  });

  it("shows the offline state without fetching", async () => {
    const { fetchMock } = renderPanel({ online: false });

    expect(await screen.findByText(/Computer is offline/)).toBeVisible();
    expect(fetchMock).not.toHaveBeenCalled();
  });
});
