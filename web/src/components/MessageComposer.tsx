import { useEffect, useRef, useState } from "react";
import { RotateCw, Send } from "lucide-react";
import {
  PayloadRequestLifecycle,
  collaborationErrorMessage,
} from "../lib/collaboration";
import { IconButton } from "./IconButton";

export function MessageComposer({
  targetKey,
  label,
  placeholder,
  disabledReason,
  onSend,
}: {
  targetKey: string;
  label: string;
  placeholder: string;
  disabledReason?: string;
  onSend: (requestId: string, body: string) => Promise<void>;
}) {
  const [body, setBody] = useState("");
  const [pending, setPending] = useState(false);
  const [error, setError] = useState<string>();
  const lifecycle = useRef(
    new PayloadRequestLifecycle<{ target: string; body: string }>(),
  ).current;

  useEffect(() => {
    lifecycle.sync({ target: targetKey, body: body.trim() });
    setError(undefined);
  }, [body, lifecycle, targetKey]);

  const send = async () => {
    const trimmed = body.trim();
    if (!trimmed || disabledReason) return;
    const payload = { target: targetKey, body: trimmed };
    const requestId = lifecycle.sync(payload);
    setPending(true);
    setError(undefined);
    try {
      await onSend(requestId, trimmed);
      lifecycle.complete();
      setBody("");
    } catch (cause) {
      setError(collaborationErrorMessage(cause, "send message"));
    } finally {
      setPending(false);
    }
  };

  return (
    <footer className="composer" data-testid={`${label}-composer`}>
      <textarea
        aria-label={label === "main" ? "Message" : "Thread reply"}
        placeholder={disabledReason || placeholder}
        value={body}
        onChange={(event) => {
          const nextBody = event.target.value;
          setBody(nextBody);
          lifecycle.sync({ target: targetKey, body: nextBody.trim() });
          setError(undefined);
        }}
        disabled={pending || !!disabledReason}
        rows={2}
        onKeyDown={(event) => {
          if (event.key === "Enter" && (event.metaKey || event.ctrlKey)) {
            event.preventDefault();
            void send();
          }
        }}
      />
      {error && (
        <p className="composer-error" role="alert">
          {error}
        </p>
      )}
      <div className="composer-toolbar">
        <span>
          {pending
            ? "Sending…"
            : error
              ? "Message kept for retry"
              : "⌘/Ctrl + Enter"}
        </span>
        <IconButton
          className="compact send-button"
          label={error ? "Retry message" : "Send message"}
          tooltip={error ? "Retry with the same request ID" : "Send message"}
          tooltipPlacement="top"
          disabled={pending || !!disabledReason || body.trim().length === 0}
          onClick={() => void send()}
        >
          {error ? <RotateCw size={16} /> : <Send size={17} />}
        </IconButton>
      </div>
    </footer>
  );
}
