import {
  act,
  cleanup,
  fireEvent,
  render,
  screen,
} from "@testing-library/react";
import { create } from "@bufbuild/protobuf";
import { afterEach, describe, expect, it, vi } from "vitest";
import App from "./App";
import {
  GetBootstrapResponseSchema,
  type GetBootstrapResponse,
} from "./gen/sumi/system/v1/system_pb";
import { getBootstrap } from "./lib/bootstrap";

vi.mock("./lib/bootstrap", () => ({ getBootstrap: vi.fn() }));

const mockedBootstrap = vi.mocked(getBootstrap);

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

    await act(async () => {
      resolveBootstrap(
        create(GetBootstrapResponseSchema, {
          serverId: "7ba1a702-8df6-4a35-993f-122261797262",
          version: "0.1.0",
        }),
      );
    });

    expect(screen.getByText("No conversations yet")).toBeInTheDocument();
  });

  it("shows the persistent server identity", async () => {
    mockedBootstrap.mockResolvedValue(
      create(GetBootstrapResponseSchema, {
        serverId: "7ba1a702-8df6-4a35-993f-122261797262",
        version: "0.1.0",
        platforms: ["macos", "linux"],
        capabilities: ["conversation-shell"],
      }),
    );

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
    expect(screen.getByRole("button", { name: "Retry" })).toBeEnabled();

    fireEvent.click(screen.getByRole("button", { name: "Retry" }));

    expect(
      await screen.findByRole("button", { name: "Retrying" }),
    ).toBeDisabled();
    expect(screen.getByRole("alert")).toHaveTextContent("Server unavailable");
    expect(screen.queryByText("No conversations yet")).not.toBeInTheDocument();
  });
});
