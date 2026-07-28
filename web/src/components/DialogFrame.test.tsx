import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";

import { DialogFrame } from "./DialogFrame";

afterEach(cleanup);

describe("DialogFrame", () => {
  it("owns focus, traps Tab, closes consistently, and restores its trigger", async () => {
    const close = vi.fn();
    const trigger = document.createElement("button");
    trigger.textContent = "Open";
    document.body.append(trigger);
    trigger.focus();

    const view = render(
      <DialogFrame close={close} labelId="dialog-title" className="test-dialog">
        <h2 id="dialog-title">Test Dialog</h2>
        <button type="button">First</button>
        <input aria-label="Preferred" data-dialog-initial-focus />
        <button type="button">Last</button>
      </DialogFrame>,
    );

    expect(screen.getByLabelText("Preferred")).toHaveFocus();
    expect(document.body).toHaveStyle({ overflow: "hidden" });

    trigger.focus();
    expect(screen.getByLabelText("Preferred")).toHaveFocus();

    screen.getByRole("button", { name: "Last" }).focus();
    fireEvent.keyDown(document, { key: "Tab" });
    expect(screen.getByRole("button", { name: "First" })).toHaveFocus();

    screen.getByRole("button", { name: "First" }).focus();
    fireEvent.keyDown(document, { key: "Tab", shiftKey: true });
    expect(screen.getByRole("button", { name: "Last" })).toHaveFocus();

    fireEvent.click(screen.getByRole("dialog", { name: "Test Dialog" }));
    expect(close).not.toHaveBeenCalled();
    fireEvent.click(screen.getByRole("dialog", { name: "Test Dialog" }).parentElement!);
    expect(close).toHaveBeenCalledTimes(1);

    fireEvent.keyDown(document, { key: "Escape" });
    expect(close).toHaveBeenCalledTimes(2);

    view.unmount();
    await waitFor(() => expect(trigger).toHaveFocus());
    expect(document.body.style.overflow).toBe("");
    trigger.remove();
  });
});
