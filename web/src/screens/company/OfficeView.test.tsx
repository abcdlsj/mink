import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { act, cleanup, render, screen, waitFor } from "@testing-library/react";
import { createElement, type ReactNode } from "react";
import { afterEach, describe, expect, it, vi } from "vitest";

import type { Agent, Member } from "../../api/client";
import { CompanyOfficeView } from "./OfficeView";

vi.mock("@tanstack/react-router", () => ({
  Link: ({ children, ...props }: { children?: ReactNode } & Record<string, unknown>) =>
    createElement("a", { ...props, href: "#" }, children),
}));

const spaceId = "space-office";
const currentMember: Member = {
  access_level: "owner",
  display_name: "Ada",
  id: "human-ada",
  kind: "human",
  permissions: [],
};

afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
});

describe("CompanyOfficeView", () => {
  it("keeps an independent station for every Agent beyond the 12-Agent tier", async () => {
    const agents = Array.from({ length: 13 }, (_, index) => officeAgent(index));
    stubAgents(agents);

    renderOffice();

    await screen.findByRole("heading", { name: "Office" });
    const workers = await screen.findAllByRole("img", { name: "Office worker sprite" });
    expect(workers).toHaveLength(13);

    const room = document.querySelector(".office-room") as HTMLElement;
    expect(room).toHaveStyle({ width: "960px", height: "640px" });
    const transforms = [...document.querySelectorAll<HTMLElement>(".office-agent")].map(
      (agent) => agent.style.transform,
    );
    expect(new Set(transforms).size).toBe(13);
  });

  it("keeps each Agent at the same station when a refresh returns a different order", async () => {
    const first = officeAgent(0);
    const second = officeAgent(1);
    const fetchAgents = stubAgentResponses([
      [first, second],
      [second, first],
    ]);
    const client = renderOffice();

    const firstLink = await screen.findByTitle("Agent 0 · Idle");
    const secondLink = await screen.findByTitle("Agent 1 · Idle");
    const firstStation = firstLink.style.transform;
    const secondStation = secondLink.style.transform;

    await act(() => client.invalidateQueries({ queryKey: ["agents", spaceId] }));
    await waitFor(() =>
      expect(
        fetchAgents.mock.calls.filter(([input]) =>
          String(input).endsWith(`/spaces/${spaceId}/agents`),
        ),
      ).toHaveLength(2),
    );

    expect(firstLink).toHaveStyle({ transform: firstStation });
    expect(secondLink).toHaveStyle({ transform: secondStation });
  });

  it("renders direct targets without movement or sprite animation under reduced motion", async () => {
    stubAgents([officeAgent(0)]);
    const interval = vi.spyOn(window, "setInterval");
    vi.stubGlobal(
      "matchMedia",
      vi.fn(() => ({
        matches: true,
        addEventListener: vi.fn(),
        removeEventListener: vi.fn(),
      })),
    );

    renderOffice();

    await screen.findByRole("heading", { name: "Office" });
    const worker = await screen.findByRole("img", { name: "Office worker sprite" });
    expect(worker).toHaveClass("pixel-agent--sit");
    expect(worker.closest(".office-agent")).toHaveStyle({ transitionDuration: "0ms" });
    expect(interval.mock.calls.some(([, delay]) => delay === 1000)).toBe(false);
  });
});

function renderOffice() {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  render(
    <QueryClientProvider client={client}>
      <CompanyOfficeView
        spaceSlug="sumi-lab"
        spaceId={spaceId}
        channels={[]}
        directMessages={[]}
        members={[currentMember]}
        currentMember={currentMember}
      />
    </QueryClientProvider>,
  );
  return client;
}

function stubAgents(agents: Agent[]) {
  return stubAgentResponses([agents]);
}

function stubAgentResponses(responses: Agent[][]) {
  let index = 0;
  const fetchAgents = vi.fn(async (input: RequestInfo | URL) => {
    const path = String(input);
    if (path.endsWith(`/spaces/${spaceId}/agents`)) {
      const response = responses[Math.min(index, responses.length - 1)];
      index += 1;
      return json(response);
    }
    throw new Error(`Unexpected request: ${path}`);
  });
  vi.stubGlobal(
    "fetch",
    fetchAgents,
  );
  return fetchAgents;
}

function officeAgent(index: number): Agent {
  return {
    access_level: "member",
    activity_status: "idle",
    attention_config: {
      ambient_debounce_seconds: 20,
      ambient_enabled: true,
      ambient_max_wait_seconds: 120,
      dm_immediate: true,
      max_retry_count: 3,
      mention_immediate: true,
    },
    computer_reachable: true,
    computer_id: `computer-${index}`,
    created_at: "2026-08-01T00:00:00Z",
    desired_lifecycle: "active",
    driver_kind: "builtin",
    last_error_code: null,
    member_id: `agent-${index}`,
    memory_files: [],
    name: `Agent ${index}`,
    provision_status: "ready",
    retired_at: null,
    role_revision: 1,
    role_text: "Worker",
    space_id: spaceId,
    updated_at: "2026-08-01T00:00:00Z",
  };
}

function json(value: unknown, status = 200): Response {
  return new Response(JSON.stringify(value), {
    status,
    headers: { "Content-Type": "application/json" },
  });
}
