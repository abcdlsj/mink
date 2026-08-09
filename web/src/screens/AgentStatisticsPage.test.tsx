import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { cleanup, fireEvent, render, screen } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";

import { AgentStatisticsWorkspace } from "./AgentStatisticsPage";

const spaceId = "019c0000-0000-7000-8000-000000000001";
const computerId = "019c0000-0000-7000-8000-000000000002";
const irisId = "019c0000-0000-7000-8000-000000000010";
const leoId = "019c0000-0000-7000-8000-000000000011";

function json(value: unknown, status = 200): Response {
  return new Response(JSON.stringify(value), {
    status,
    headers: { "Content-Type": "application/json" },
  });
}

function usageFor(agentId: string) {
  return {
    requests: 3,
    input_tokens: 12_000,
    output_tokens: 2_000,
    cached_input_tokens: 5_000,
    cache_write_tokens: 1_000,
    cache_hit_rate: 0.42,
    first_at: "2026-08-01T00:00:00Z",
    last_at: "2026-08-01T01:00:00Z",
    series: [
      { bucket: "2026-08-01T00:00:00Z", requests: 3, input_tokens: 12_000, output_tokens: 2_000, cached_input_tokens: 5_000 },
    ],
    by_model: [{ key: "deepseek-v4-pro", requests: 3, input_tokens: 12_000, output_tokens: 2_000, cached_input_tokens: 5_000 }],
    by_agent: [{ key: agentId, requests: 3, input_tokens: 12_000, output_tokens: 2_000, cached_input_tokens: 5_000 }],
    by_agent_series: [irisId, leoId].map((id) => ({
      agent_id: id,
      requests: 3,
      input_tokens: 12_000,
      output_tokens: 2_000,
      cached_input_tokens: 5_000,
      series: [
        { bucket: "2026-08-01T00:00:00Z", requests: 3, input_tokens: 12_000, output_tokens: 2_000, cached_input_tokens: 5_000 },
      ],
    })),
  };
}

function renderWorkspace({ withUsage = true }: { withUsage?: boolean } = {}) {
  const fetchMock = vi.fn(async (input: RequestInfo | URL) => {
      const path = String(input);
      if (path.endsWith("/spaces/by-slug/sumi-lab")) {
        return json({ id: spaceId, name: "Sumi Dev", slug: "sumi-lab" });
      }
      if (path.endsWith(`/spaces/${spaceId}/agents`)) {
        return json([
          { member_id: irisId, name: "Iris", activity_status: "idle" },
          { member_id: leoId, name: "Leo", activity_status: "idle" },
        ]);
      }
      if (path.endsWith(`/spaces/${spaceId}/computers`)) {
        return json([{ id: computerId, name: "Dev Computer", status: withUsage ? "online" : "offline" }]);
      }
      if (path.includes(`/computers/${computerId}/llm-usage`)) {
        return withUsage ? json(usageFor(irisId)) : json({ ...usageFor(irisId), by_agent_series: [], series: [], requests: 0 });
      }
      throw new Error(`Unexpected request: ${path}`);
    });
  vi.stubGlobal("fetch", fetchMock);
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  const view = render(
    <QueryClientProvider client={client}>
      <AgentStatisticsWorkspace spaceSlug="sumi-lab" />
    </QueryClientProvider>,
  );
  return { view, fetchMock };
}

afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
});

describe("AgentStatisticsWorkspace", () => {
  it("lists Agents with usage and shows the selected Agent's curve", async () => {
    renderWorkspace();

    expect(await screen.findByRole("heading", { name: "Agent statistics" })).toBeVisible();
    expect(await screen.findByText("Iris")).toBeVisible();
    expect(screen.getByText("Leo")).toBeVisible();
    expect(screen.getByRole("img", { name: /Token usage curve/ })).toBeVisible();
    expect(screen.getByLabelText("Iris usage")).toBeVisible();
  });

  it("switches the selected Agent", async () => {
    renderWorkspace();
    await screen.findByText("Iris");

    fireEvent.click(screen.getByRole("button", { name: /Leo/ }));

    expect(screen.getByLabelText("Leo usage")).toBeVisible();
  });

  it("shows an empty state when no usage is recorded", async () => {
    renderWorkspace({ withUsage: false });

    expect(await screen.findByText(/No LLM usage recorded yet/)).toBeVisible();
  });
});
