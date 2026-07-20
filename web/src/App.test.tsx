import {
  act,
  cleanup,
  fireEvent,
  render,
  screen,
  within,
} from "@testing-library/react";
import { create } from "@bufbuild/protobuf";
import { timestampFromDate } from "@bufbuild/protobuf/wkt";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import App from "./App";
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

const mockedBootstrap = vi.mocked(getBootstrap);
const mockedLoadFacts = vi.mocked(loadFacts);
const mockedGetAgentDetail = vi.mocked(getAgentDetail);
const mockedGetComputer = vi.mocked(getComputer);
const mockedCreateAgent = vi.mocked(createAgent);
const mockedSetPlacement = vi.mocked(setAgentPlacement);

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

  it("creates an Agent and optional placement as two real steps", async () => {
    const facts = readyFacts({ agents: [], placements: [] });
    const agent = makeAgent("artifact-reviewer");
    const placement = makePlacement(agent.id, facts.computers[0].id);
    mockedLoadFacts.mockResolvedValue(facts);
    mockedCreateAgent.mockResolvedValue(agent);
    mockedSetPlacement.mockResolvedValue(placement);
    mockedGetAgentDetail.mockResolvedValue({ agent, placement });

    render(<App />);
    fireEvent.click(await screen.findByRole("button", { name: "Agents" }));
    await screen.findByText("No agents yet");
    fireEvent.click(screen.getByRole("button", { name: "Create Agent" }));

    const form = screen
      .getByRole("heading", { name: "New Agent identity" })
      .closest("form")!;
    fireEvent.change(within(form).getByLabelText(/^Name/), {
      target: { value: "artifact-reviewer" },
    });
    fireEvent.change(within(form).getByLabelText(/^Description/), {
      target: { value: "Reviews published artifacts" },
    });
    fireEvent.change(within(form).getByLabelText(/^Driver/), {
      target: { value: Driver.CODEX },
    });
    const computerSelect = within(form).getByLabelText(/^Optional Computer/);
    expect(
      Array.from((computerSelect as HTMLSelectElement).options).map(
        (option) => option.value,
      ),
    ).toContain(facts.computers[0].id);
    fireEvent.change(computerSelect, {
      target: { value: facts.computers[0].id },
    });
    expect(computerSelect).toHaveValue(facts.computers[0].id);
    fireEvent.submit(form);

    expect(await screen.findByText("Durable identity")).toBeInTheDocument();
    expect(mockedCreateAgent).toHaveBeenCalledWith(
      expect.objectContaining({
        name: "artifact-reviewer",
        description: "Reviews published artifacts",
        driver: Driver.CODEX,
        requestId: expect.any(String),
      }),
    );
    expect(mockedSetPlacement).toHaveBeenCalledWith(
      agent.id,
      facts.computers[0].id,
    );
  });

  it("preserves a created Agent when optional placement fails and retries only placement", async () => {
    const facts = readyFacts({ agents: [], placements: [] });
    const agent = makeAgent("recovery-agent");
    const placement = makePlacement(agent.id, facts.computers[0].id);
    mockedLoadFacts.mockResolvedValue(facts);
    mockedCreateAgent.mockResolvedValue(agent);
    mockedSetPlacement
      .mockRejectedValueOnce(new Error("Computer no longer exists"))
      .mockResolvedValueOnce(placement);
    mockedGetAgentDetail.mockResolvedValue({ agent, placement });

    render(<App />);
    fireEvent.click(await screen.findByRole("button", { name: "Agents" }));
    await screen.findByText("No agents yet");
    fireEvent.click(screen.getByRole("button", { name: "Create Agent" }));
    const form = screen
      .getByRole("heading", { name: "New Agent identity" })
      .closest("form")!;
    fireEvent.change(within(form).getByLabelText(/^Name/), {
      target: { value: "recovery-agent" },
    });
    const computerSelect = within(form).getByLabelText(/^Optional Computer/);
    fireEvent.change(computerSelect, {
      target: { value: facts.computers[0].id },
    });
    expect(computerSelect).toHaveValue(facts.computers[0].id);
    fireEvent.submit(form);

    expect(
      await screen.findByText("Agent created, placement failed"),
    ).toBeInTheDocument();
    expect(screen.getByText(/recovery-agent is preserved/)).toBeInTheDocument();

    fireEvent.click(screen.getByRole("button", { name: "Retry placement" }));

    expect(await screen.findByText("Durable identity")).toBeInTheDocument();
    expect(mockedCreateAgent).toHaveBeenCalledTimes(1);
    expect(mockedSetPlacement).toHaveBeenCalledTimes(2);
  });

  it("prevents duplicate Create Agent submits while the first request is pending", async () => {
    const facts = readyFacts({ agents: [], placements: [] });
    let resolveAgent!: (agent: Agent) => void;
    mockedLoadFacts.mockResolvedValue(facts);
    mockedCreateAgent.mockImplementation(
      () =>
        new Promise((resolve) => {
          resolveAgent = resolve;
        }),
    );

    render(<App />);
    fireEvent.click(await screen.findByRole("button", { name: "Agents" }));
    await screen.findByText("No agents yet");
    fireEvent.click(screen.getByRole("button", { name: "Create Agent" }));
    const form = screen
      .getByRole("heading", { name: "New Agent identity" })
      .closest("form")!;
    fireEvent.change(within(form).getByLabelText(/^Name/), {
      target: { value: "single-submit" },
    });
    fireEvent.submit(form);
    fireEvent.submit(form);

    expect(
      within(form).getByRole("button", { name: "Creating Agent" }),
    ).toBeDisabled();
    expect(mockedCreateAgent).toHaveBeenCalledTimes(1);

    await act(async () => resolveAgent(makeAgent("single-submit")));
  });

  it("reuses one request ID when a lost Create response is retried", async () => {
    const facts = readyFacts({ agents: [], placements: [] });
    const agent = makeAgent("idempotent-create");
    const placement = makePlacement(agent.id, facts.computers[0].id);
    mockedLoadFacts.mockResolvedValue(facts);
    mockedCreateAgent
      .mockRejectedValueOnce(new Error("Create response was lost"))
      .mockResolvedValueOnce(agent);
    mockedSetPlacement.mockResolvedValue(placement);
    mockedGetAgentDetail.mockResolvedValue({ agent, placement });

    render(<App />);
    fireEvent.click(await screen.findByRole("button", { name: "Agents" }));
    await screen.findByText("No agents yet");
    fireEvent.click(screen.getByRole("button", { name: "Create Agent" }));
    const form = screen
      .getByRole("heading", { name: "New Agent identity" })
      .closest("form")!;
    fireEvent.change(within(form).getByLabelText(/^Name/), {
      target: { value: "idempotent-create" },
    });
    fireEvent.change(within(form).getByLabelText(/^Optional Computer/), {
      target: { value: facts.computers[0].id },
    });

    fireEvent.submit(form);
    expect(
      await within(form).findByText("Create response was lost"),
    ).toBeInTheDocument();
    expect(mockedSetPlacement).not.toHaveBeenCalled();

    fireEvent.submit(form);
    expect(await screen.findByText("Durable identity")).toBeInTheDocument();

    expect(mockedCreateAgent).toHaveBeenCalledTimes(2);
    const firstRequest = mockedCreateAgent.mock.calls[0][0];
    const secondRequest = mockedCreateAgent.mock.calls[1][0];
    expect(secondRequest.requestId).toBe(firstRequest.requestId);
    expect(mockedSetPlacement).toHaveBeenCalledTimes(1);
    expect(mockedSetPlacement).toHaveBeenCalledWith(
      agent.id,
      facts.computers[0].id,
    );
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
