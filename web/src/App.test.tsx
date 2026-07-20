import {
  act,
  cleanup,
  fireEvent,
  render,
  screen,
  waitFor,
  within,
} from "@testing-library/react";
import { create } from "@bufbuild/protobuf";
import { timestampFromDate } from "@bufbuild/protobuf/wkt";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import App from "./App";
import { CreateAgentForm } from "./components/CreateAgentForm";
import { AgentSchema, Driver, type Agent } from "./gen/sumi/agent/v1/agent_pb";
import {
  Architecture,
  ComputerSchema,
  OperatingSystem,
  type Computer,
} from "./gen/sumi/computer/v1/computer_pb";
import {
  AgentPlacementSchema,
  PlacementState,
  type AgentPlacement,
} from "./gen/sumi/placement/v1/placement_pb";
import {
  GetBootstrapResponseSchema,
  type GetBootstrapResponse,
} from "./gen/sumi/system/v1/system_pb";
import { getBootstrap } from "./lib/bootstrap";
import {
  createAgent,
  getAgentDetail,
  getComputer,
  loadFacts,
  setAgentPlacement,
  type FactsSnapshot,
} from "./lib/facts";
import { getSession, logoutSession } from "./lib/session";

vi.mock("./lib/bootstrap", () => ({ getBootstrap: vi.fn() }));
vi.mock("./lib/facts", () => ({
  loadFacts: vi.fn(),
  getAgentDetail: vi.fn(),
  getComputer: vi.fn(),
  createAgent: vi.fn(),
  setAgentPlacement: vi.fn(),
  factErrorMessage: (error: unknown, action: string) =>
    error instanceof Error ? error.message : `Could not ${action}.`,
}));
vi.mock("./lib/session", () => ({
  getSession: vi.fn(),
  logoutSession: vi.fn(),
}));

const mockedBootstrap = vi.mocked(getBootstrap);
const mockedLoadFacts = vi.mocked(loadFacts);
const mockedGetAgentDetail = vi.mocked(getAgentDetail);
const mockedGetComputer = vi.mocked(getComputer);
const mockedCreateAgent = vi.mocked(createAgent);
const mockedSetPlacement = vi.mocked(setAgentPlacement);
const mockedGetSession = vi.mocked(getSession);
const mockedLogoutSession = vi.mocked(logoutSession);

const bootstrap = create(GetBootstrapResponseSchema, {
  serverId: "7ba1a702-8df6-4a35-993f-122261797262",
  version: "0.1.0",
  platforms: ["macos", "linux"],
  capabilities: ["conversation-shell"],
});

beforeEach(() => {
  mockedBootstrap.mockResolvedValue(bootstrap);
  mockedLoadFacts.mockResolvedValue(emptyFacts());
  mockedGetAgentDetail.mockRejectedValue(new Error("Agent detail unavailable"));
  mockedGetComputer.mockRejectedValue(new Error("Computer detail unavailable"));
  mockedGetSession.mockResolvedValue({
    id: "33333333-3333-4333-8333-333333333333",
    name: "Owner",
  });
  mockedLogoutSession.mockResolvedValue();
});

afterEach(() => {
  cleanup();
  vi.clearAllMocks();
});

describe("App", () => {
  it("does not show business empty state before bootstrap resolves", async () => {
    let resolveBootstrap!: (value: GetBootstrapResponse) => void;
    mockedBootstrap.mockImplementation(
      () =>
        new Promise((resolve) => {
          resolveBootstrap = resolve;
        }),
    );

    render(<App />);

    expect(screen.getByRole("status")).toHaveTextContent("Connecting to Sumi");
    expect(screen.queryByText("No conversations yet")).not.toBeInTheDocument();

    await act(async () => resolveBootstrap(bootstrap));

    expect(screen.getByText("No conversations yet")).toBeInTheDocument();
  });

  it("shows the persistent server identity", async () => {
    render(<App />);

    expect(await screen.findByText("Server 7ba1a702")).toBeInTheDocument();
    expect(screen.getByText("No conversations yet")).toBeInTheDocument();
  });

  it("requires authentication only for Conversation", async () => {
    mockedGetSession.mockResolvedValueOnce(undefined);
    const facts = readyFacts();
    mockedLoadFacts.mockResolvedValue(facts);
    mockedGetAgentDetail.mockResolvedValue({
      agent: facts.agents[0],
      placement: facts.placements[0],
    });

    render(<App />);

    expect(
      await screen.findByText("Authentication required"),
    ).toBeInTheDocument();
    expect(screen.queryByText("No conversations yet")).not.toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: "Agents" }));
    expect(await screen.findByText("release-coordinator")).toBeInTheDocument();
    expect(
      screen.queryByText("Authentication required"),
    ).not.toBeInTheDocument();
  });

  it("logs out without hiding public management facts", async () => {
    const facts = readyFacts();
    mockedLoadFacts.mockResolvedValue(facts);
    mockedGetAgentDetail.mockResolvedValue({
      agent: facts.agents[0],
      placement: facts.placements[0],
    });

    render(<App />);

    fireEvent.click(await screen.findByRole("button", { name: "Log out" }));
    expect(
      await screen.findByText("Authentication required"),
    ).toBeInTheDocument();
    expect(mockedLogoutSession).toHaveBeenCalledTimes(1);
    fireEvent.click(screen.getByRole("button", { name: "Agents" }));
    expect(await screen.findByText("release-coordinator")).toBeInTheDocument();
  });

  it("shows an actionable offline state", async () => {
    mockedBootstrap.mockRejectedValueOnce(new Error("offline"));
    mockedBootstrap.mockImplementationOnce(() => new Promise(() => {}));

    render(<App />);

    expect(await screen.findByRole("alert")).toHaveTextContent(
      "Server unavailable",
    );
    fireEvent.click(screen.getByRole("button", { name: "Retry" }));

    expect(
      await screen.findByRole("button", { name: "Retrying" }),
    ).toBeDisabled();
    expect(screen.queryByText("No conversations yet")).not.toBeInTheDocument();
  });

  it("keeps facts loading separate from bootstrap and shows honest module empties", async () => {
    let resolveFacts!: (value: FactsSnapshot) => void;
    mockedLoadFacts.mockImplementation(
      () =>
        new Promise((resolve) => {
          resolveFacts = resolve;
        }),
    );

    render(<App />);
    fireEvent.click(await screen.findByRole("button", { name: "Agents" }));

    expect(screen.getByText("Loading agents")).toBeInTheDocument();
    expect(screen.queryByText("No agents yet")).not.toBeInTheDocument();

    await act(async () => resolveFacts(emptyFacts()));

    expect(screen.getByText("No agents yet")).toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: "Computers" }));
    expect(screen.getByText("No computers registered")).toBeInTheDocument();
  });

  it("retries an initial facts error", async () => {
    mockedLoadFacts
      .mockRejectedValueOnce(new Error("offline"))
      .mockResolvedValueOnce(emptyFacts());

    render(<App />);
    fireEvent.click(await screen.findByRole("button", { name: "Agents" }));

    expect(
      (await screen.findAllByText("Facts unavailable")).length,
    ).toBeGreaterThan(0);
    const navigation = screen.getByRole("complementary", {
      name: "Agents navigation",
    });
    fireEvent.click(within(navigation).getByRole("button", { name: "Retry" }));

    expect(await screen.findByText("No agents yet")).toBeInTheDocument();
    expect(mockedLoadFacts).toHaveBeenCalledTimes(2);
  });

  it("retries Computer facts without inventing an empty state", async () => {
    const facts = readyFacts();
    mockedLoadFacts
      .mockRejectedValueOnce(new Error("computer query offline"))
      .mockResolvedValueOnce(facts);
    mockedGetComputer.mockResolvedValue(facts.computers[0]);

    render(<App />);
    fireEvent.click(await screen.findByRole("button", { name: "Computers" }));

    const navigation = screen.getByRole("complementary", {
      name: "Computers navigation",
    });
    expect(
      (await within(navigation).findAllByText("Facts unavailable")).length,
    ).toBeGreaterThan(0);
    expect(
      within(navigation).queryByText("No computers registered"),
    ).not.toBeInTheDocument();
    fireEvent.click(within(navigation).getByRole("button", { name: "Retry" }));

    expect(
      await within(navigation).findByText("Build host"),
    ).toBeInTheDocument();
  });

  it("keeps loaded facts visible when refresh fails", async () => {
    const facts = readyFacts();
    mockedLoadFacts
      .mockResolvedValueOnce(facts)
      .mockRejectedValueOnce(new Error("refresh offline"));
    mockedGetAgentDetail.mockResolvedValue({
      agent: facts.agents[0],
      placement: facts.placements[0],
    });

    render(<App />);
    fireEvent.click(await screen.findByRole("button", { name: "Agents" }));
    expect(await screen.findByText("release-coordinator")).toBeInTheDocument();

    fireEvent.click(screen.getByRole("button", { name: "Refresh Agents" }));

    expect(await screen.findByText("refresh offline")).toBeInTheDocument();
    expect(screen.getAllByText("release-coordinator").length).toBeGreaterThan(
      0,
    );
  });

  it("reads Agent and Computer details through Get APIs", async () => {
    const facts = readyFacts();
    mockedLoadFacts.mockResolvedValue(facts);
    mockedGetAgentDetail.mockResolvedValue({
      agent: facts.agents[0],
      placement: facts.placements[0],
    });
    mockedGetComputer.mockResolvedValue(facts.computers[0]);

    render(<App />);
    fireEvent.click(await screen.findByRole("button", { name: "Agents" }));

    expect(await screen.findByText("Durable identity")).toBeInTheDocument();
    expect(mockedGetAgentDetail).toHaveBeenCalledWith(facts.agents[0].id);

    fireEvent.click(screen.getByRole("button", { name: "Computers" }));
    expect(await screen.findByText("Registered Computer")).toBeInTheDocument();
    expect(mockedGetComputer).toHaveBeenCalledWith(facts.computers[0].id);
  });

  it("keeps Agent management read-only while preserving placement facts", async () => {
    const facts = readyFacts();
    mockedLoadFacts.mockResolvedValue(facts);
    mockedGetAgentDetail.mockResolvedValue({
      agent: facts.agents[0],
      placement: facts.placements[0],
    });

    render(<App />);
    fireEvent.click(await screen.findByRole("button", { name: "Agents" }));

    const navigation = screen.getByRole("complementary", {
      name: "Agents navigation",
    });
    const workspace = screen.getByRole("region", { name: "Agents" });
    expect(
      within(navigation).queryByRole("button", { name: "Create Agent" }),
    ).not.toBeInTheDocument();
    expect(
      await within(workspace).findByText("Build host"),
    ).toBeInTheDocument();
    expect(
      within(workspace).getByText(
        "Placement changes are not available in this read-only view.",
      ),
    ).toBeInTheDocument();
    expect(
      within(workspace).queryByRole("button", { name: /placement/i }),
    ).not.toBeInTheDocument();
  });
});

describe("direct Agent mutation contract", () => {
  it("reuses one canonical placement request ID across a retry lifecycle", async () => {
    const computer = makeComputer();
    const agent = makeAgent("recovery-agent");
    const placement = makePlacement(agent.id, computer.id);
    mockedCreateAgent.mockResolvedValue(agent);
    mockedSetPlacement
      .mockRejectedValueOnce(new Error("Computer no longer exists"))
      .mockResolvedValueOnce(placement);

    render(
      <CreateAgentForm
        computers={[computer]}
        onAgentCreated={() => {}}
        onPlacementChanged={() => {}}
        onFinished={() => {}}
        onCancel={() => {}}
      />,
    );
    const form = screen
      .getByRole("heading", { name: "New Agent identity" })
      .closest("form")!;
    fireEvent.change(within(form).getByLabelText(/^Name/), {
      target: { value: agent.name },
    });
    fireEvent.change(within(form).getByLabelText(/^Optional Computer/), {
      target: { value: computer.id },
    });
    fireEvent.submit(form);

    expect(
      await screen.findByText("Agent created, placement failed"),
    ).toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: "Retry placement" }));

    await waitFor(() => expect(mockedSetPlacement).toHaveBeenCalledTimes(2));
    const first = mockedSetPlacement.mock.calls[0][0];
    const second = mockedSetPlacement.mock.calls[1][0];
    expect(first).toEqual({
      requestId: expect.any(String),
      agentId: agent.id,
      computerId: computer.id,
    });
    expect(first.requestId).toMatch(
      /^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/,
    );
    expect(second.requestId).toBe(first.requestId);
    expect(second.agentId).toBe(first.agentId);
    expect(second.computerId).toBe(first.computerId);
  });
});

function emptyFacts(): FactsSnapshot {
  return { agents: [], computers: [], placements: [] };
}

function readyFacts(overrides: Partial<FactsSnapshot> = {}): FactsSnapshot {
  const computer = makeComputer();
  const agent = makeAgent();
  return {
    agents: [agent],
    computers: [computer],
    placements: [makePlacement(agent.id, computer.id)],
    ...overrides,
  };
}

function makeAgent(name = "release-coordinator"): Agent {
  return create(AgentSchema, {
    id:
      name === "release-coordinator"
        ? "11111111-1111-4111-8111-111111111111"
        : crypto.randomUUID(),
    name,
    description: "Coordinates durable releases",
    driver: Driver.CODEX,
    createdAt: timestampFromDate(new Date("2026-07-20T10:00:00Z")),
    updatedAt: timestampFromDate(new Date("2026-07-20T10:00:00Z")),
  });
}

function makeComputer(): Computer {
  return create(ComputerSchema, {
    id: "22222222-2222-4222-8222-222222222222",
    name: "Build host",
    os: OperatingSystem.LINUX,
    arch: Architecture.AMD64,
    createdAt: timestampFromDate(new Date("2026-07-20T10:00:00Z")),
    lastSeenAt: timestampFromDate(new Date("2026-07-20T10:05:00Z")),
  });
}

function makePlacement(agentId: string, computerId: string): AgentPlacement {
  return create(AgentPlacementSchema, {
    agentId,
    computerId,
    generation: 1n,
    state: PlacementState.PENDING,
    createdAt: timestampFromDate(new Date("2026-07-20T10:01:00Z")),
    updatedAt: timestampFromDate(new Date("2026-07-20T10:01:00Z")),
  });
}
