import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";

import type { Message } from "../../api/client";
import { MessageComposer } from "./MessageComposer";

afterEach(() => {
  cleanup();
  window.localStorage.clear();
  vi.restoreAllMocks();
});

describe("MessageComposer draft", () => {
  it("restores the last unsent body for the same conversation", () => {
    const first = renderComposer("channel:1");
    fireEvent.change(screen.getByLabelText("Message"), { target: { value: "not sent yet" } });
    first.unmount();

    renderComposer("channel:1");

    expect(screen.getByLabelText("Message")).toHaveValue("not sent yet");
  });

  it("keeps drafts separate per conversation", () => {
    const first = renderComposer("channel:1");
    fireEvent.change(screen.getByLabelText("Message"), { target: { value: "draft a" } });
    first.unmount();

    renderComposer("channel:2");

    expect(screen.getByLabelText("Message")).toHaveValue("");
  });

  it("clears the draft after a successful send", async () => {
    const send = vi.fn().mockResolvedValue(message());
    const first = renderComposer("channel:1", send);
    const input = screen.getByLabelText("Message");
    fireEvent.change(input, { target: { value: "send me" } });
    fireEvent.submit(input.closest("form")!);

    await waitFor(() => expect(send).toHaveBeenCalled());
    await waitFor(() => expect(input).toHaveValue(""));
    first.unmount();

    renderComposer("channel:1");

    expect(screen.getByLabelText("Message")).toHaveValue("");
  });
});

function renderComposer(draftKey: string, send = vi.fn().mockResolvedValue(message())) {
  return render(
    <QueryClientProvider client={new QueryClient({ defaultOptions: { mutations: { retry: false } } })}>
      <MessageComposer
        spaceId="space-1"
        draftKey={draftKey}
        members={[]}
        placeholder="Message"
        ariaLabel="Message"
        attachmentAriaLabel="Choose Attachment"
        attachButtonLabel="Attach file"
        sendButtonLabel="Send message"
        attachmentsAriaLabel="Attachments ready to send"
        send={send}
        onSent={vi.fn()}
      />
    </QueryClientProvider>,
  );
}

function message(): Message {
  return {
    id: "message-1",
    channel_id: "channel-1",
    seq: 1,
    author: { id: "human-ada", kind: "human", display_name: "Ada" },
    placement: "root",
    content: { type: "text", body_markdown: "sent" },
    mentions: [],
    mention_all: false,
    attachments: [],
    attention_failures: [],
    task_refs: [],
    thread_id: "thread-1",
    created_at: "2026-07-25T12:00:00Z",
    reply_count: 0,
  };
}
