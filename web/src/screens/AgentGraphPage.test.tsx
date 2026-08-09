import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { cleanup, fireEvent, render, screen, within } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";

import type { AgentGraph, AgentGraphEdge, AgentGraphNode } from "../api/client";
import { AgentGraphWorkspace, INITIAL_GRAPH_VIEW } from "./AgentGraphPage";
import { layoutGraph } from "./agentGraphLayout";

const spaceId = "019c0000-0000-7000-8000-000000000001";
const agentA: AgentGraphNode = {
  member_id: "019c0000-0000-7000-8000-000000000010",
  display_name: "Coder_One",
  role_text: "Coder",
};
const agentB: AgentGraphNode = {
  member_id: "019c0000-0000-7000-8000-000000000011",
  display_name: "Reviewer_Two",
  role_text: "Reviewer",
};
const edge: AgentGraphEdge = {
  member_a_id: agentA.member_id,
  member_b_id: agentB.member_id,
  dm_message_count: 2,
  mention_a_to_b: 1,
  mention_b_to_a: 0,
  reply_a_to_b: 0,
  reply_b_to_a: 1,
  total_interactions: 4,
  last_message_at: "2026-08-01T10:00:00Z",
  recent_messages: [
    {
      id: "019c0000-0000-7000-8000-000000000020",
      channel_id: "019c0000-0000-7000-8000-000000000021",
      kind: "mention",
      author_member_id: agentA.member_id,
      target_member_id: agentB.member_id,
      created_at: "2026-08-01T09:00:00Z",
      body_markdown: "please review this",
    },
    {
      id: "019c0000-0000-7000-8000-000000000022",
      channel_id: "019c0000-0000-7000-8000-000000000021",
      kind: "reply",
      author_member_id: agentB.member_id,
      target_member_id: agentA.member_id,
      created_at: "2026-08-01T10:00:00Z",
      body_markdown: "looks good",
    },
  ],
};
const edgeBToC: AgentGraphEdge = {
  ...edge,
  member_a_id: agentB.member_id,
  member_b_id: "019c0000-0000-7000-8000-000000000012",
  total_interactions: 2,
  recent_messages: [],
};
const agentC: AgentGraphNode = {
  member_id: edgeBToC.member_b_id,
  display_name: "Planner_Three",
  role_text: "Planner",
};

function json(value: unknown, status = 200): Response {
  return new Response(JSON.stringify(value), {
    status,
    headers: { "Content-Type": "application/json" },
  });
}

function renderWorkspace(graph: AgentGraph) {
  vi.stubGlobal(
    "fetch",
    vi.fn(async (input: RequestInfo | URL) => {
      const path = String(input);
      if (path.endsWith(`/spaces/${spaceId}/agent-graph`)) return json(graph);
      throw new Error(`Unexpected request: ${path}`);
    }),
  );
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return render(
    <QueryClientProvider client={client}>
      <AgentGraphWorkspace spaceId={spaceId} spaceSlug="sumi-lab" />
    </QueryClientProvider>,
  );
}

afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
});

describe("AgentGraphWorkspace", () => {
  it("renders nodes, the relationship edge, and the overview totals", async () => {
    renderWorkspace({ nodes: [agentA, agentB], edges: [edge] });

    expect(await screen.findByRole("button", { name: "Coder_One, Coder" })).toBeVisible();
    expect(screen.getByRole("button", { name: "Reviewer_Two, Reviewer" })).toBeVisible();
    expect(
      screen.getByRole("button", {
        name: "4 interactions between Coder_One and Reviewer_Two",
      }),
    ).toBeVisible();
    expect(screen.getByText(/2 Agents · 1 relationships · 4 interactions/)).toBeVisible();
    expect(screen.getByText("Busiest pair: Coder_One ↔ Reviewer_Two with 4 interactions.")).toBeVisible();
  });

  it("selecting an Agent lists its neighbors", async () => {
    renderWorkspace({ nodes: [agentA, agentB], edges: [edge] });

    const node = await screen.findByRole("button", { name: "Coder_One, Coder" });
    fireEvent.click(node);

    expect(await screen.findByRole("heading", { name: "Coder_One" })).toBeVisible();
    const details = screen.getByRole("complementary", { name: "Agent graph details" });
    expect(within(details).getByRole("button", { name: /Reviewer_Two/ })).toHaveTextContent("4");
  });

  it("dims only non-neighbors and keeps selected edge endpoints distinct", async () => {
    renderWorkspace({ nodes: [agentA, agentB, agentC], edges: [edge, edgeBToC] });

    const selected = await screen.findByRole("button", { name: "Coder_One, Coder" });
    fireEvent.click(selected);

    expect(selected).toHaveClass("graph-node--active");
    expect(screen.getByRole("button", { name: "Reviewer_Two, Reviewer" })).toHaveClass(
      "graph-node--neighbor",
    );
    expect(screen.getByRole("button", { name: "Planner_Three, Planner" })).toHaveClass(
      "graph-node--dimmed",
    );

    fireEvent.click(
      screen.getByRole("button", {
        name: "4 interactions between Coder_One and Reviewer_Two",
      }),
    );
    expect(screen.getByRole("button", { name: "Coder_One, Coder" })).toHaveClass(
      "graph-node--active",
    );
    expect(screen.getByRole("button", { name: "Reviewer_Two, Reviewer" })).toHaveClass(
      "graph-node--active",
    );
    expect(screen.getByRole("button", { name: "Planner_Three, Planner" })).toHaveClass(
      "graph-node--dimmed",
    );
  });

  it("converts responsive client pixels for pan and wheel anchor", async () => {
    renderWorkspace({ nodes: [agentA, agentB], edges: [edge] });
    const svg = await screen.findByRole("application", { name: /Agent coordination graph/ });
    vi.spyOn(svg, "getBoundingClientRect").mockReturnValue({
      x: 100,
      y: 50,
      left: 100,
      top: 50,
      right: 550,
      bottom: 330,
      width: 450,
      height: 280,
      toJSON: () => ({}),
    });

    fireEvent.pointerDown(svg, { pointerId: 1, clientX: 100, clientY: 50 });
    fireEvent.pointerMove(svg, { pointerId: 1, clientX: 145, clientY: 78 });
    fireEvent.pointerUp(svg, { pointerId: 1, clientX: 145, clientY: 78 });
    expect(svg.querySelector(":scope > g")?.getAttribute("transform")).toBe("translate(90 56) scale(1)");

    fireEvent.click(screen.getByRole("button", { name: "Zoom in" }));
    fireEvent.pointerDown(svg, { pointerId: 2, clientX: 100, clientY: 50 });
    fireEvent.pointerMove(svg, { pointerId: 2, clientX: 145, clientY: 78 });
    fireEvent.pointerUp(svg, { pointerId: 2, clientX: 145, clientY: 78 });
    expect(svg.querySelector(":scope > g")?.getAttribute("transform")).toBe("translate(180 112) scale(1.25)");

    fireEvent.wheel(svg, { clientX: 325, clientY: 190, deltaY: -100 });
    const transform = svg.querySelector(":scope > g")?.getAttribute("transform") ?? "";
    const match = transform.match(/^translate\(([-0-9.]+) ([-0-9.]+)\) scale\(([-0-9.]+)\)$/);
    expect(match).not.toBeNull();
    expect(Number(match?.[1])).toBeCloseTo(147.6);
    expect(Number(match?.[2])).toBeCloseTo(91.84);
    expect(Number(match?.[3])).toBeCloseTo(1.4);
  });

  it("selecting an edge shows the communication chain", async () => {
    renderWorkspace({ nodes: [agentA, agentB], edges: [edge] });

    const link = await screen.findByRole("button", {
      name: "4 interactions between Coder_One and Reviewer_Two",
    });
    fireEvent.click(link);

    expect(await screen.findByRole("heading", { name: "Coder_One ↔ Reviewer_Two" })).toBeVisible();
    expect(screen.getByText("please review this")).toBeVisible();
    expect(screen.getByText("looks good")).toBeVisible();
    expect(screen.getByRole("heading", { name: "Communication chain" })).toBeVisible();
  });

  it("shows an empty state when the Space has no Agents", async () => {
    renderWorkspace({ nodes: [], edges: [] });

    expect(await screen.findByText("No Agents yet.")).toBeVisible();
  });
});

describe("layoutGraph", () => {
  it("keeps the default view at the canvas origin so the centered layout is visible", () => {
    expect(INITIAL_GRAPH_VIEW).toEqual({ x: 0, y: 0, k: 1 });
  });

  it("places every node inside the viewport with finite coordinates", () => {
    const nodes = layoutGraph([agentA, agentB], [edge], 900, 560, 60);

    expect(nodes).toHaveLength(2);
    for (const node of nodes) {
      expect(Number.isFinite(node.x)).toBe(true);
      expect(Number.isFinite(node.y)).toBe(true);
      expect(node.x).toBeGreaterThanOrEqual(30);
      expect(node.x).toBeLessThanOrEqual(870);
      expect(node.y).toBeGreaterThanOrEqual(30);
      expect(node.y).toBeLessThanOrEqual(530);
    }
  });

  it("returns an empty layout for an empty graph", () => {
    expect(layoutGraph([], [], 900, 560, 10)).toEqual([]);
  });
});
