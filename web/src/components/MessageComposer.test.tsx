import {
  cleanup,
  fireEvent,
  render,
  screen,
  waitFor,
} from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { MessageComposer } from "./MessageComposer";

afterEach(cleanup);

describe("MessageComposer request lifecycle", () => {
  it("keeps body and request ID on failure, then rotates the ID after editing", async () => {
    const send = vi.fn().mockRejectedValue(new Error("offline"));
    render(
      <MessageComposer
        targetKey="space:one"
        label="main"
        placeholder="Message"
        onSend={send}
      />,
    );
    const input = screen.getByLabelText("Message");
    fireEvent.change(input, { target: { value: "First body" } });
    fireEvent.click(screen.getByRole("button", { name: "Send message" }));
    await waitFor(() => expect(send).toHaveBeenCalledTimes(1));
    expect(input).toHaveValue("First body");
    fireEvent.click(screen.getByRole("button", { name: "Retry message" }));
    await waitFor(() => expect(send).toHaveBeenCalledTimes(2));
    expect(send.mock.calls[1][0]).toBe(send.mock.calls[0][0]);

    fireEvent.change(input, { target: { value: "Edited body" } });
    fireEvent.click(screen.getByRole("button", { name: "Send message" }));
    await waitFor(() => expect(send).toHaveBeenCalledTimes(3));
    expect(send.mock.calls[2][0]).not.toBe(send.mock.calls[1][0]);
  });

  it("locks the payload while a send is pending", async () => {
    let resolve!: () => void;
    const send = vi.fn(
      () =>
        new Promise<void>((done) => {
          resolve = done;
        }),
    );
    render(
      <MessageComposer
        targetKey="thread:root"
        label="thread"
        placeholder="Reply"
        onSend={send}
      />,
    );
    const input = screen.getByLabelText("Thread reply");
    fireEvent.change(input, { target: { value: "Reply body" } });
    fireEvent.click(screen.getByRole("button", { name: "Send message" }));
    expect(input).toBeDisabled();
    resolve();
    await waitFor(() => expect(input).toHaveValue(""));
  });

  it("does not let an old target completion clear the new target draft", async () => {
    let resolveOld!: () => void;
    const send = vi.fn(
      () =>
        new Promise<void>((resolve) => {
          resolveOld = resolve;
        }),
    );
    const view = render(
      <MessageComposer
        key="space-a"
        targetKey="space:a"
        label="main"
        placeholder="Message"
        onSend={send}
      />,
    );
    fireEvent.change(screen.getByLabelText("Message"), {
      target: { value: "Old target body" },
    });
    fireEvent.click(screen.getByRole("button", { name: "Send message" }));
    view.rerender(
      <MessageComposer
        key="space-b"
        targetKey="space:b"
        label="main"
        placeholder="Message"
        onSend={send}
      />,
    );
    const newDraft = screen.getByLabelText("Message");
    fireEvent.change(newDraft, { target: { value: "New target body" } });
    resolveOld();
    await waitFor(() => expect(newDraft).toHaveValue("New target body"));
  });
});
