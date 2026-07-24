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
import { Code, ConnectError } from "@connectrpc/connect";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import App from "./App";
import { CreateAgentForm } from "./components/CreateAgentForm";
import {
  AgentProfileSchema,
  AgentRuntimeSpecSchema,
  AgentSchema,
  EngineKind,
  ProviderProtocol,
  RuntimeSandboxProvider,
  type Agent,
} from "./gen/sumi/agent/v1/agent_pb";
import {
  Architecture,
  CapabilityHealth,
  CapabilityInventorySchema,
  ComputerSchema,
  CredentialDeliveryAlgorithm,
  CredentialDeliveryCapabilitySchema,
  CredentialDeliverySchema,
  CredentialDeliveryState,
  CredentialKind,
  CredentialStore,
  EngineCapabilitySchema,
  OperatingSystem,
  SandboxCapabilitySchema,
  SandboxDaemonCrashCleanup,
  SandboxFilesystemIsolation,
  SandboxIsolation,
  SandboxNetworkIsolation,
  SandboxProcessControl,
  SandboxProvider,
  SandboxSecretMaterialization,
  SandboxWorkspaceAccess,
  type Computer,
} from "./gen/sumi/computer/v1/computer_pb";
import {
  AgentPlacementSchema,
  PlacementState,
  type AgentPlacement,
} from "./gen/sumi/placement/v1/placement_pb";
import {
  PrincipalKind,
  SpaceKind,
  type Message,
  type Space,
} from "./gen/sumi/space/v1/space_pb";
import {
  GetBootstrapResponseSchema,
  type GetBootstrapResponse,
} from "./gen/sumi/system/v1/system_pb";
import { getBootstrap } from "./lib/bootstrap";
import {
  createAgent,
  createComputerPairing,
  enqueueCredentialDelivery,
  getAgentDetail,
  getAgentRuntimeSpec,
  getComputer,
  listCredentialDeliveries,
  loadFacts,
  setAgentPlacement,
  updateAgentRuntimeSpec,
  waitForCredentialDelivery,
  type FactsSnapshot,
} from "./lib/facts";
import {
  getLocalSetupRequired,
  getSession,
  loginLocalAccount,
  logoutSession,
  setupLocalAccount,
} from "./lib/session";
import {
  loadConversation,
  loadDirectory,
  loadThread,
  sendSpaceMessage,
  sendThreadMessage,
  type ConversationSnapshot,
  type DirectorySnapshot,
} from "./lib/collaboration";

vi.mock("./lib/bootstrap", () => ({ getBootstrap: vi.fn() }));
vi.mock("./lib/facts", () => ({
  loadFacts: vi.fn(),
  getAgentDetail: vi.fn(),
  getComputer: vi.fn(),
  createAgent: vi.fn(),
  createComputerPairing: vi.fn(),
  getAgentRuntimeSpec: vi.fn(),
  updateAgentRuntimeSpec: vi.fn(),
  setAgentPlacement: vi.fn(),
  enqueueCredentialDelivery: vi.fn(),
  listCredentialDeliveries: vi.fn(),
  waitForCredentialDelivery: vi.fn(),
  factErrorMessage: (error: unknown, action: string) =>
    error instanceof Error ? error.message : `Could not ${action}.`,
}));
vi.mock("./lib/session", () => ({
  getSession: vi.fn(),
  getLocalSetupRequired: vi.fn(),
  loginLocalAccount: vi.fn(),
  setupLocalAccount: vi.fn(),
  logoutSession: vi.fn(),
  LocalAuthError: class LocalAuthError extends Error {},
}));
vi.mock("./lib/collaboration", async (importOriginal) => {
  const actual = await importOriginal<typeof import("./lib/collaboration")>();
  return {
    ...actual,
    loadDirectory: vi.fn(),
    loadConversation: vi.fn(),
    loadMoreConversationMessages: vi.fn(),
    loadThread: vi.fn(),
    loadMoreThreadMessages: vi.fn(),
    createDM: vi.fn(),
    createGroup: vi.fn(),
    addSpaceMember: vi.fn(),
    removeSpaceMember: vi.fn(),
    setSpaceArchived: vi.fn(),
    sendSpaceMessage: vi.fn(),
    sendThreadMessage: vi.fn(),
  };
});

const mockedBootstrap = vi.mocked(getBootstrap);
const mockedLoadFacts = vi.mocked(loadFacts);
const mockedGetAgentDetail = vi.mocked(getAgentDetail);
const mockedGetComputer = vi.mocked(getComputer);
const mockedCreateAgent = vi.mocked(createAgent);
const mockedCreateComputerPairing = vi.mocked(createComputerPairing);
const mockedGetAgentRuntimeSpec = vi.mocked(getAgentRuntimeSpec);
const mockedUpdateAgentRuntimeSpec = vi.mocked(updateAgentRuntimeSpec);
const mockedSetAgentPlacement = vi.mocked(setAgentPlacement);
const mockedEnqueueCredentialDelivery = vi.mocked(enqueueCredentialDelivery);
const mockedListCredentialDeliveries = vi.mocked(listCredentialDeliveries);
const mockedWaitForCredentialDelivery = vi.mocked(waitForCredentialDelivery);
const mockedGetSession = vi.mocked(getSession);
const mockedGetLocalSetupRequired = vi.mocked(getLocalSetupRequired);
const mockedLoginLocalAccount = vi.mocked(loginLocalAccount);
const mockedSetupLocalAccount = vi.mocked(setupLocalAccount);
const mockedLogoutSession = vi.mocked(logoutSession);
const mockedLoadDirectory = vi.mocked(loadDirectory);
const mockedLoadConversation = vi.mocked(loadConversation);
const mockedLoadThread = vi.mocked(loadThread);
const mockedSendSpaceMessage = vi.mocked(sendSpaceMessage);
const mockedSendThreadMessage = vi.mocked(sendThreadMessage);

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
  mockedCreateComputerPairing.mockResolvedValue();
  mockedGetAgentRuntimeSpec.mockResolvedValue(undefined);
  mockedListCredentialDeliveries.mockResolvedValue([]);
  mockedGetSession.mockResolvedValue({
    id: "33333333-3333-4333-8333-333333333333",
    name: "Owner",
  });
  mockedGetLocalSetupRequired.mockResolvedValue(false);
  mockedLoginLocalAccount.mockResolvedValue({
    id: "33333333-3333-4333-8333-333333333333",
    name: "Owner",
  });
  mockedSetupLocalAccount.mockResolvedValue({
    id: "33333333-3333-4333-8333-333333333333",
    name: "Owner",
  });
  mockedLogoutSession.mockResolvedValue();
  mockedLoadDirectory.mockResolvedValue(emptyDirectory());
  mockedLoadConversation.mockReset();
  mockedLoadThread.mockReset();
  mockedSendSpaceMessage.mockReset();
  mockedSendThreadMessage.mockReset();
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
    expect(
      screen.queryByText("No conversation selected"),
    ).not.toBeInTheDocument();

    await act(async () => resolveBootstrap(bootstrap));

    expect(
      await screen.findByText("No conversation selected"),
    ).toBeInTheDocument();
  });

  it("keeps server identity accessible without persistent copy", async () => {
    render(<App />);

    expect(
      await screen.findByLabelText("Server 7ba1a702 · v0.1.0"),
    ).toBeInTheDocument();
    expect(screen.queryByText(/Server 7ba1a702/)).not.toBeInTheDocument();
    expect(
      await screen.findByText("No conversation selected"),
    ).toBeInTheDocument();
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

    expect(await screen.findByText("Welcome back")).toBeInTheDocument();
    expect(
      screen.getByText(
        "Sign in as a Sumi Human. Your password never leaves this Server.",
      ),
    ).toBeInTheDocument();
    expect(
      screen.queryByText("No conversation selected"),
    ).not.toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: "Agents" }));
    expect(await screen.findByText("release-coordinator")).toBeInTheDocument();
    expect(screen.queryByText("Welcome back")).not.toBeInTheDocument();
  });

  it("completes one-time local Owner setup without exposing the setup code", async () => {
    mockedGetSession.mockResolvedValueOnce(undefined);
    mockedGetLocalSetupRequired.mockResolvedValueOnce(true);
    let finishSetup!: (human: { id: string; name: string }) => void;
    mockedSetupLocalAccount.mockImplementationOnce(
      () =>
        new Promise((resolve) => {
          finishSetup = resolve;
        }),
    );

    render(<App />);

    expect(
      await screen.findByRole("heading", { name: "Create your local account" }),
    ).toBeInTheDocument();
    fireEvent.change(screen.getByLabelText("Username"), {
      target: { value: "not valid" },
    });
    fireEvent.change(screen.getByLabelText("Password"), {
      target: { value: "short" },
    });
    fireEvent.change(screen.getByLabelText("Owner setup code"), {
      target: { value: "too-short" },
    });
    fireEvent.click(screen.getByRole("button", { name: "Join Sumi" }));

    expect(
      screen.getByText("Enter a valid 3–32 character username."),
    ).toBeInTheDocument();
    expect(
      screen.getByText("Use 12–256 characters with at least one non-space."),
    ).toBeInTheDocument();
    expect(
      screen.getByText("Paste the complete 43–128 character Owner setup code."),
    ).toBeInTheDocument();
    expect(mockedSetupLocalAccount).not.toHaveBeenCalled();

    fireEvent.change(screen.getByLabelText("Username"), {
      target: { value: "iris" },
    });
    fireEvent.change(screen.getByLabelText("Password"), {
      target: { value: "correct horse battery staple" },
    });
    fireEvent.change(screen.getByLabelText("Owner setup code"), {
      target: { value: "A".repeat(43) },
    });
    fireEvent.click(screen.getByRole("button", { name: "Join Sumi" }));

    expect(mockedSetupLocalAccount).toHaveBeenCalledWith({
      username: "iris",
      password: "correct horse battery staple",
      ownerSetupCode: "A".repeat(43),
    });
    expect(
      screen.getByRole("button", { name: "Creating local account" }),
    ).toBeDisabled();
    expect(screen.queryByText("A".repeat(43))).not.toBeInTheDocument();

    await act(async () =>
      finishSetup({
        id: "33333333-3333-4333-8333-333333333333",
        name: "Owner",
      }),
    );
    expect(
      await screen.findByLabelText("Signed in as Owner"),
    ).toBeInTheDocument();
  });

  it("signs in locally and keeps authentication errors actionable", async () => {
    mockedGetSession.mockResolvedValueOnce(undefined);
    mockedLoginLocalAccount
      .mockRejectedValueOnce(new Error("Username or password is incorrect."))
      .mockResolvedValueOnce({
        id: "33333333-3333-4333-8333-333333333333",
        name: "Owner",
      });

    render(<App />);

    expect(
      await screen.findByRole("heading", { name: "Welcome back" }),
    ).toBeInTheDocument();
    fireEvent.change(screen.getByLabelText("Username"), {
      target: { value: "owner" },
    });
    fireEvent.change(screen.getByLabelText("Password"), {
      target: { value: "incorrect password value" },
    });
    fireEvent.click(screen.getByRole("button", { name: "Sign in" }));
    expect(await screen.findByRole("alert")).toHaveTextContent(
      "Username or password is incorrect.",
    );

    fireEvent.change(screen.getByLabelText("Password"), {
      target: { value: "correct horse battery staple" },
    });
    expect(screen.queryByRole("alert")).not.toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: "Sign in" }));
    expect(
      await screen.findByLabelText("Signed in as Owner"),
    ).toBeInTheDocument();
  });

  it("does not report an unknown or failed session as signed out", async () => {
    let resolveSession!: (value: undefined) => void;
    mockedGetSession.mockImplementationOnce(
      () =>
        new Promise((resolve) => {
          resolveSession = resolve;
        }),
    );

    const first = render(<App />);
    expect(
      await screen.findByLabelText("Checking session"),
    ).toBeInTheDocument();
    expect(screen.queryByText("Checking session")).not.toBeInTheDocument();
    expect(screen.queryByText("Human signed out")).not.toBeInTheDocument();
    await act(async () => resolveSession(undefined));
    expect(
      await screen.findByLabelText("Human signed out"),
    ).toBeInTheDocument();
    expect(screen.queryByText("Human signed out")).not.toBeInTheDocument();
    first.unmount();

    mockedGetSession.mockRejectedValueOnce(new Error("session unavailable"));
    render(<App />);
    expect(await screen.findByText("Session unavailable")).toBeInTheDocument();
    expect(screen.queryByText("Human signed out")).not.toBeInTheDocument();
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
    expect(await screen.findByText("Welcome back")).toBeInTheDocument();
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
    expect(
      screen.queryByText("No conversation selected"),
    ).not.toBeInTheDocument();
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
    expect(
      screen.getByRole("heading", { name: "Connect your first Computer" }),
    ).toBeInTheDocument();
    expect(
      screen.getByRole("button", { name: "Copy Computer pairing command" }),
    ).toBeDisabled();
    expect(
      screen.getByText(/sumi computer start --pairing-code/),
    ).toBeInTheDocument();
  });

  it("opens Computer onboarding instead of trapping a compact empty directory", async () => {
    const originalWidth = window.innerWidth;
    Object.defineProperty(window, "innerWidth", {
      configurable: true,
      value: 390,
    });
    try {
      render(<App />);
      fireEvent.click(await screen.findByRole("button", { name: "Computers" }));

      expect(
        await screen.findByRole("heading", {
          name: "Connect your first Computer",
        }),
      ).toBeVisible();
      await waitFor(() =>
        expect(screen.getByRole("main")).toHaveClass("navigation-collapsed"),
      );
    } finally {
      Object.defineProperty(window, "innerWidth", {
        configurable: true,
        value: originalWidth,
      });
    }
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

    const filesystemBoundary = screen
      .getByText("Host filesystem isolation")
      .closest("div");
    const networkBoundary = screen
      .getByText("Network isolation")
      .closest("div");
    expect(filesystemBoundary).not.toBeNull();
    expect(networkBoundary).not.toBeNull();
    expect(within(filesystemBoundary!).getByText("none")).toBeInTheDocument();
    expect(within(networkBoundary!).getByText("none")).toBeInTheDocument();
  });

  it("configures Agent runtime with browser-sealed BYOK and exact placement", async () => {
    const facts = readyFacts();
    mockedLoadFacts.mockResolvedValue(facts);
    mockedGetAgentDetail.mockResolvedValue({
      agent: facts.agents[0],
      placement: facts.placements[0],
    });
    const delivery = create(CredentialDeliverySchema, {
      id: "44444444-4444-4444-8444-444444444444",
      requestId: "55555555-5555-4555-8555-555555555555",
      computerId: facts.computers[0].id,
      agentId: facts.agents[0].id,
      credentialKind: CredentialKind.OPENAI,
      state: CredentialDeliveryState.SUCCEEDED,
      bindingHandle: "binding-handle-0001",
    });
    const runtimeSpec = create(AgentRuntimeSpecSchema, {
      agentId: facts.agents[0].id,
      revision: 1n,
      engine: EngineKind.BUILTIN,
      providerProtocol: ProviderProtocol.OPENAI_RESPONSES,
      providerEndpoint: "https://api.openai.com",
      model: "gpt-test",
      credentialBindingHandle: delivery.bindingHandle,
      sandboxProvider: RuntimeSandboxProvider.TRUSTED_LOCAL,
      maxRunDurationSeconds: 900,
      maxOutputBytes: 8n << 20n,
      toolPolicy: {},
    });
    const configuredPlacement = create(AgentPlacementSchema, {
      ...facts.placements[0],
      runtimeSpec,
      desiredRevision: 2n,
      state: PlacementState.PENDING,
    });
    mockedEnqueueCredentialDelivery.mockResolvedValue(delivery);
    mockedWaitForCredentialDelivery.mockResolvedValue(delivery);
    mockedUpdateAgentRuntimeSpec.mockResolvedValue(runtimeSpec);
    mockedSetAgentPlacement.mockResolvedValue(configuredPlacement);

    render(<App />);
    fireEvent.click(await screen.findByRole("button", { name: "Agents" }));

    const navigation = screen.getByRole("complementary", {
      name: "Agents navigation",
    });
    const workspace = screen.getByRole("region", { name: "Agents" });
    expect(
      (await within(workspace).findAllByText("Build host")).length,
    ).toBeGreaterThan(0);
    expect(
      within(navigation).queryByRole("button", { name: "Create Agent" }),
    ).not.toBeInTheDocument();
    await waitFor(() =>
      expect(within(workspace).getByLabelText("Runtime Engine")).toHaveValue(
        EngineKind.BUILTIN.toString(),
      ),
    );
    fireEvent.change(within(workspace).getByLabelText("Provider model"), {
      target: { value: "gpt-test" },
    });
    fireEvent.change(within(workspace).getByLabelText("BYOK credential"), {
      target: { value: "sk-browser-only" },
    });
    fireEvent.click(
      within(workspace).getByRole("button", { name: "Configure runtime" }),
    );

    await waitFor(() =>
      expect(mockedEnqueueCredentialDelivery).toHaveBeenCalledTimes(1),
    );
    expect(
      screen.queryByDisplayValue("sk-browser-only"),
    ).not.toBeInTheDocument();
    expect(mockedUpdateAgentRuntimeSpec).toHaveBeenCalledWith(
      expect.objectContaining({
        agentId: facts.agents[0].id,
        expectedRevision: 0n,
        credentialBindingHandle: delivery.bindingHandle,
        engine: EngineKind.BUILTIN,
      }),
    );
    expect(mockedSetAgentPlacement).toHaveBeenCalledWith(
      expect.objectContaining({
        agentId: facts.agents[0].id,
        computerId: facts.computers[0].id,
      }),
    );
    expect(
      await within(workspace).findByText(/desired revision 2 is pending/),
    ).toBeInTheDocument();
  });

  it("selects the placed Computer when Agent detail arrives after facts", async () => {
    const facts = readyFacts();
    const placedComputer = create(ComputerSchema, {
      ...makeComputer(),
      id: "66666666-6666-4666-8666-666666666666",
      name: "Placed host",
    });
    mockedLoadFacts.mockResolvedValue({
      agents: facts.agents,
      computers: [facts.computers[0], placedComputer],
      placements: [],
    });
    mockedGetAgentDetail.mockResolvedValue({
      agent: facts.agents[0],
      placement: makePlacement(facts.agents[0].id, placedComputer.id),
    });

    render(<App />);
    fireEvent.click(await screen.findByRole("button", { name: "Agents" }));

    await waitFor(() =>
      expect(screen.getByLabelText("Runtime Computer")).toHaveValue(
        placedComputer.id,
      ),
    );
  });

  it("loads a real Space snapshot and sends main and first Thread messages", async () => {
    const directory = collaborationDirectory();
    const snapshot = conversationSnapshot(directory.spaces[0]);
    const reply = collaborationMessage(1, "First reply", "reply-1");
    mockedLoadDirectory.mockResolvedValue(directory);
    const followUp = collaborationMessage(2, "Main follow-up", "message-2");
    mockedLoadConversation.mockResolvedValueOnce(snapshot).mockResolvedValue({
      ...snapshot,
      messages: [...snapshot.messages, followUp],
    });
    mockedLoadThread
      .mockResolvedValueOnce({
        root: snapshot.messages[0],
        replies: [],
        hasMore: false,
        nextAfterSequence: 0n,
      })
      .mockResolvedValue({
        root: snapshot.messages[0],
        replies: [reply],
        hasMore: false,
        nextAfterSequence: 1n,
      });
    mockedSendSpaceMessage.mockResolvedValue(followUp);
    mockedSendThreadMessage.mockResolvedValue(reply);

    render(<App />);
    fireEvent.click(
      await screen.findByRole("button", { name: "Open navigation" }),
    );
    fireEvent.click(
      await screen.findByRole("button", { name: "Release room" }),
    );

    expect(await screen.findByText("Initial message")).toBeInTheDocument();
    expect(mockedLoadConversation).toHaveBeenCalledWith(
      "33333333-3333-4333-8333-333333333333",
      directory.spaces[0].id,
      { signal: expect.any(AbortSignal) },
    );
    fireEvent.change(screen.getByLabelText("Message"), {
      target: { value: "Main follow-up" },
    });
    fireEvent.click(screen.getByRole("button", { name: "Send message" }));
    expect(await screen.findByText("Main follow-up")).toBeInTheDocument();

    fireEvent.click(screen.getAllByRole("button", { name: "Open thread" })[0]);
    expect(
      await screen.findByText("No replies yet. Start the Thread below."),
    ).toBeInTheDocument();
    fireEvent.change(screen.getByLabelText("Thread reply"), {
      target: { value: "First reply" },
    });
    fireEvent.click(
      within(screen.getByRole("complementary", { name: "Context" })).getByRole(
        "button",
        { name: "Send message" },
      ),
    );
    expect(await screen.findByText("First reply")).toBeInTheDocument();
  });

  it("clears selection and context when the selected Space becomes inaccessible", async () => {
    const directory = collaborationDirectory();
    const snapshot = conversationSnapshot(directory.spaces[0]);
    mockedLoadDirectory.mockResolvedValue(directory);
    mockedLoadConversation
      .mockResolvedValueOnce(snapshot)
      .mockRejectedValueOnce(
        new ConnectError("membership revoked", Code.PermissionDenied),
      );

    render(<App />);
    fireEvent.click(
      await screen.findByRole("button", { name: "Open navigation" }),
    );
    fireEvent.click(
      await screen.findByRole("button", { name: "Release room" }),
    );
    expect(await screen.findByText("Initial message")).toBeInTheDocument();
    fireEvent.click(
      screen.getByRole("button", { name: "Refresh conversation" }),
    );

    expect(
      await screen.findByText("No conversation selected"),
    ).toBeInTheDocument();
    expect(screen.queryByText("Initial message")).not.toBeInTheDocument();
    for (const button of screen.getAllByRole("button", {
      name: "Open context",
    })) {
      expect(button).toBeDisabled();
    }
  });

  it("rechecks the Human session when Collaboration returns Unauthenticated", async () => {
    const directory = collaborationDirectory();
    const snapshot = conversationSnapshot(directory.spaces[0]);
    mockedLoadDirectory.mockResolvedValue(directory);
    mockedLoadConversation
      .mockResolvedValueOnce(snapshot)
      .mockRejectedValueOnce(
        new ConnectError("session expired", Code.Unauthenticated),
      );

    render(<App />);
    fireEvent.click(
      await screen.findByRole("button", { name: "Open navigation" }),
    );
    fireEvent.click(
      await screen.findByRole("button", { name: "Release room" }),
    );
    expect(await screen.findByText("Initial message")).toBeInTheDocument();
    fireEvent.click(
      screen.getByRole("button", { name: "Refresh conversation" }),
    );

    await waitFor(() => expect(mockedGetSession).toHaveBeenCalledTimes(2));
    expect(
      await screen.findByText("No conversation selected"),
    ).toBeInTheDocument();
    expect(screen.queryByText("Initial message")).not.toBeInTheDocument();
  });
});

describe("direct Agent mutation contract", () => {
  it("submits identity and profile without runtime configuration", async () => {
    const agent = makeAgent("recovery-agent");
    mockedCreateAgent.mockResolvedValue(agent);
    const finished = vi.fn();

    render(
      <CreateAgentForm
        onAgentCreated={() => {}}
        onFinished={finished}
        onCancel={() => {}}
      />,
    );
    const form = screen
      .getByRole("heading", { name: "New Agent identity" })
      .closest("form")!;
    fireEvent.change(within(form).getByLabelText(/^Handle/), {
      target: { value: agent.handle },
    });
    fireEvent.change(within(form).getByLabelText(/^Display name/), {
      target: { value: agent.profile?.displayName },
    });
    fireEvent.change(within(form).getByLabelText(/^Role/), {
      target: { value: agent.profile?.role },
    });
    fireEvent.change(within(form).getByLabelText(/^Mission/), {
      target: { value: agent.profile?.mission },
    });
    fireEvent.change(within(form).getByLabelText(/^Instructions/), {
      target: { value: agent.profile?.instructions },
    });
    fireEvent.submit(form);

    await waitFor(() => expect(mockedCreateAgent).toHaveBeenCalledOnce());
    const input = mockedCreateAgent.mock.calls[0][0];
    expect(input).toEqual({
      requestId: expect.any(String),
      handle: agent.handle,
      displayName: agent.profile?.displayName,
      role: agent.profile?.role,
      mission: agent.profile?.mission,
      instructions: agent.profile?.instructions,
    });
    expect(input.requestId).toMatch(
      /^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/,
    );
    expect(finished).toHaveBeenCalledWith(agent.id);
  });
});

function emptyFacts(): FactsSnapshot {
  return { agents: [], computers: [], placements: [] };
}

function emptyDirectory(): DirectorySnapshot {
  return {
    organization: {
      id: "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa",
      name: "Sumi",
      bootstrapHumanId: "33333333-3333-4333-8333-333333333333",
    } as DirectorySnapshot["organization"],
    humans: [],
    agents: [],
    spaces: [],
    createSpace: { status: "allowed" },
  };
}

function collaborationDirectory(): DirectorySnapshot {
  const space = {
    id: "44444444-4444-4444-8444-444444444444",
    organizationId: "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa",
    kind: SpaceKind.GROUP,
    name: "Release room",
  } as Space;
  return {
    organization: {
      id: "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa",
      name: "Sumi",
      bootstrapHumanId: "33333333-3333-4333-8333-333333333333",
    } as DirectorySnapshot["organization"],
    humans: [
      {
        id: "33333333-3333-4333-8333-333333333333",
        organizationId: "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa",
        name: "Owner",
        status: 1,
        role: 1,
      } as DirectorySnapshot["humans"][number],
    ],
    agents: [makeAgent()],
    spaces: [space],
    createSpace: { status: "allowed" },
  };
}

function conversationSnapshot(space: Space): ConversationSnapshot {
  return {
    space,
    memberships: [
      {
        spaceId: space.id,
        principal: {
          kind: PrincipalKind.HUMAN,
          id: "33333333-3333-4333-8333-333333333333",
        },
      } as ConversationSnapshot["memberships"][number],
    ],
    messages: [collaborationMessage(1, "Initial message", "message-1")],
    permissions: {
      members: { status: "allowed" },
      archive: { status: "allowed" },
      send: { status: "allowed" },
    },
    hasMore: false,
    nextAfterSequence: 1n,
  };
}

function collaborationMessage(
  sequence: number,
  body: string,
  id: string,
): Message {
  return {
    id,
    spaceId: "44444444-4444-4444-8444-444444444444",
    threadRootMessageId: id.startsWith("reply") ? "message-1" : "",
    targetSequence: BigInt(sequence),
    author: {
      kind: PrincipalKind.HUMAN,
      id: "33333333-3333-4333-8333-333333333333",
    },
    body,
    requestId: `request-${id}`,
  } as Message;
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
    handle: name,
    profile: create(AgentProfileSchema, {
      agentId:
        name === "release-coordinator"
          ? "11111111-1111-4111-8111-111111111111"
          : "33333333-3333-4333-8333-333333333333",
      revision: 1n,
      displayName: name,
      role: "release coordinator",
      mission: "Coordinates durable releases",
      instructions: "Keep evidence explicit.",
      createdAt: timestampFromDate(new Date("2026-07-20T10:00:00Z")),
    }),
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
    capabilityInventory: create(CapabilityInventorySchema, {
      revision: 1n,
      engines: [
        create(EngineCapabilitySchema, {
          engine: EngineKind.BUILTIN,
          version: "0.1.0",
          protocolVersion: 1,
          supportsCancel: true,
          providerProtocols: [ProviderProtocol.OPENAI_RESPONSES],
          health: CapabilityHealth.HEALTHY,
        }),
      ],
      sandboxes: [
        create(SandboxCapabilitySchema, {
          provider: SandboxProvider.TRUSTED_LOCAL,
          isolation: SandboxIsolation.TRUSTED_LOCAL,
          workspaceAccess: SandboxWorkspaceAccess.DIRECT_READ_WRITE,
          processControl: SandboxProcessControl.CONTEXT_PROCESS_GROUP,
          filesystemIsolation: SandboxFilesystemIsolation.NONE,
          networkIsolation: SandboxNetworkIsolation.NONE,
          secretMaterialization:
            SandboxSecretMaterialization.EPHEMERAL_ENVIRONMENT,
          daemonCrashCleanup: SandboxDaemonCrashCleanup.NONE,
        }),
      ],
      credentialDelivery: create(CredentialDeliveryCapabilitySchema, {
        health: CapabilityHealth.HEALTHY,
        algorithm: CredentialDeliveryAlgorithm.X25519_XCHACHA20_POLY1305,
        store: CredentialStore.LINUX_SECRET_SERVICE,
        keyId: "credential-key-0001",
        publicKey: Uint8Array.from({ length: 32 }, (_, index) => index + 1),
      }),
    }),
  });
}

function makePlacement(agentId: string, computerId: string): AgentPlacement {
  return create(AgentPlacementSchema, {
    agentId,
    computerId,
    desiredRevision: 1n,
    state: PlacementState.PENDING,
    createdAt: timestampFromDate(new Date("2026-07-20T10:01:00Z")),
    updatedAt: timestampFromDate(new Date("2026-07-20T10:01:00Z")),
  });
}
