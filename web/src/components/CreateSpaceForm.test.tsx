import {
  cleanup,
  fireEvent,
  render,
  screen,
  waitFor,
} from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import type { DirectorySnapshot } from "../lib/collaboration";
import { CreateSpaceForm } from "./CreateSpaceForm";

afterEach(cleanup);

describe("CreateSpaceForm request lifecycle", () => {
  it("retries Create Group with the same ID and rotates it after a name edit", async () => {
    const createGroup = vi.fn().mockRejectedValue(new Error("offline"));
    render(
      <CreateSpaceForm
        directory={directory()}
        currentHumanId="human-owner"
        onCreateDM={vi.fn()}
        onCreateGroup={createGroup}
        onCreated={() => {}}
        onClose={() => {}}
      />,
    );
    fireEvent.click(screen.getByRole("tab", { name: /Group/ }));
    const name = screen.getByLabelText("Group name");
    fireEvent.change(name, { target: { value: "Release" } });
    fireEvent.click(screen.getByRole("button", { name: "Create Group" }));
    await waitFor(() => expect(createGroup).toHaveBeenCalledTimes(1));
    fireEvent.click(screen.getByRole("button", { name: "Retry" }));
    await waitFor(() => expect(createGroup).toHaveBeenCalledTimes(2));
    expect(createGroup.mock.calls[1][0]).toBe(createGroup.mock.calls[0][0]);

    fireEvent.change(name, { target: { value: "Release room" } });
    fireEvent.click(screen.getByRole("button", { name: "Create Group" }));
    await waitFor(() => expect(createGroup).toHaveBeenCalledTimes(3));
    expect(createGroup.mock.calls[2][0]).not.toBe(createGroup.mock.calls[1][0]);
  });
});

function directory(): DirectorySnapshot {
  return {
    organization: {
      id: "org",
      name: "Sumi",
      bootstrapHumanId: "human-owner",
    } as DirectorySnapshot["organization"],
    humans: [],
    agents: [],
    spaces: [],
    createSpace: { status: "allowed" },
  };
}
