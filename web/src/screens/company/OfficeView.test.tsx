import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { cleanup, render, screen } from "@testing-library/react";
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
  return render(
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
}

function stubAgents(agents: Agent[]) {
  vi.stubGlobal(
    "fetch",
    vi.fn(async (input: RequestInfo | URL) => {
      const path = String(input);
      if (path.endsWith(`/spaces/${spaceId}/agents`)) return json(agents);
      throw new Error(`Unexpected request: ${path}`);
    }),
  );
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
