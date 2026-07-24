import { Code, ConnectError } from "@connectrpc/connect";
import {
  Check,
  Clipboard,
  KeyRound,
  Laptop,
  LockKeyhole,
  RefreshCw,
  TerminalSquare,
} from "lucide-react";
import { useMemo, useState } from "react";
import {
  getPairingEndpoint,
  maskedComputerPairingCommand,
  prepareComputerPairing,
  UnsafePairingOriginError,
} from "../lib/computerPairing";
import { createComputerPairing } from "../lib/facts";

type PairingState =
  | { status: "ready" }
  | { status: "creating" }
  | { status: "created"; expiresAt: Date; command: string }
  | { status: "error"; message: string };

export function ComputerOnboarding({
  authenticated,
  compact = false,
  onSignIn,
  onRefresh,
}: {
  authenticated: boolean;
  compact?: boolean;
  onSignIn: () => void;
  onRefresh: () => void;
}) {
  const [state, setState] = useState<PairingState>({ status: "ready" });
  const [copied, setCopied] = useState(false);
  const endpoint = useMemo(() => {
    try {
      getPairingEndpoint(location.origin);
      return { safe: true as const };
    } catch (error) {
      return {
        safe: false as const,
        message:
          error instanceof Error
            ? error.message
            : "This Sumi address cannot create a safe connection command.",
      };
    }
  }, []);

  const createCommand = async () => {
    setCopied(false);
    setState({ status: "creating" });
    try {
      const prepared = prepareComputerPairing(location.origin);
      await createComputerPairing(prepared);
      setState({
        status: "created",
        expiresAt: prepared.expiresAt,
        command: prepared.command,
      });
    } catch (error) {
      setState({ status: "error", message: pairingError(error) });
    }
  };

  const copyCommand = async () => {
    if (state.status !== "created") return;
    try {
      await navigator.clipboard.writeText(state.command);
      setCopied(true);
    } catch {
      setState({
        status: "error",
        message:
          "Could not copy the private command. Allow clipboard access, then create a new command.",
      });
    }
  };

  const content = (
    <section
      className={"computer-onboarding " + (compact ? "compact" : "")}
      aria-labelledby={compact ? "connect-another-title" : "connect-title"}
    >
      <header className="computer-onboarding-header">
        <div className="identity-mark computer connect-mark">
          <Laptop size={24} />
        </div>
        <div>
          <span className="eyebrow">Execution node</span>
          <h2 id={compact ? "connect-another-title" : "connect-title"}>
            {compact
              ? "Connect another Computer"
              : "Connect your first Computer"}
          </h2>
          <p>
            Pair this Sumi with a Mac or Linux machine, then Agents can run
            there under explicit placement.
          </p>
        </div>
      </header>

      <ol className="pairing-steps">
        <li className={state.status === "created" ? "complete" : "active"}>
          <span className="pairing-step-number">
            {state.status === "created" ? <Check size={15} /> : "1"}
          </span>
          <div>
            <strong>Create a one-time connection command</strong>
            <p>
              It expires in 10 minutes. The target Mac or Linux machine must
              already have the <code>sumi</code> CLI installed.
            </p>
            {!authenticated ? (
              <button
                className="primary-action"
                type="button"
                onClick={onSignIn}
              >
                Sign in to create pairing
              </button>
            ) : !endpoint.safe ? (
              <div className="pairing-safety-error" role="alert">
                <LockKeyhole size={16} />
                <span>{endpoint.message}</span>
              </div>
            ) : (
              <button
                className="primary-action"
                type="button"
                onClick={() => void createCommand()}
                disabled={state.status === "creating"}
              >
                <KeyRound size={16} />
                {state.status === "creating"
                  ? "Creating connection command"
                  : state.status === "created"
                    ? "Create a new command"
                    : "Create connection command"}
              </button>
            )}
          </div>
        </li>

        <li className={state.status === "created" ? "active" : ""}>
          <span className="pairing-step-number">2</span>
          <div>
            <strong>Run this on the Computer</strong>
            <p>
              One command validates the Server, pairs this machine, and starts
              the current-user Computer service. The short-lived code will be
              present in shell history.
            </p>
            <div className="pairing-command">
              <TerminalSquare size={17} aria-hidden="true" />
              <code>{maskedComputerPairingCommand}</code>
              <button
                type="button"
                aria-label="Copy Computer pairing command"
                onClick={() => void copyCommand()}
                disabled={state.status !== "created"}
              >
                {copied ? <Check size={16} /> : <Clipboard size={16} />}
                <span>{copied ? "Copied" : "Copy"}</span>
              </button>
            </div>
          </div>
        </li>

        <li>
          <span className="pairing-step-number">3</span>
          <div>
            <strong>Confirm it appears here</strong>
            <p>
              After the command reports that the service started, refresh the
              directory. The Server list—not this page—confirms registration.
            </p>
            <button
              className="secondary-action"
              type="button"
              onClick={onRefresh}
            >
              <RefreshCw size={15} />
              Refresh Computers
            </button>
          </div>
        </li>
      </ol>

      <div className="pairing-status" aria-live="polite">
        {state.status === "created" && (
          <p>
            Connection command ready. Use it before{" "}
            <strong>
              {state.expiresAt.toLocaleTimeString([], {
                hour: "2-digit",
                minute: "2-digit",
              })}
            </strong>
            .
          </p>
        )}
        {state.status === "error" && (
          <p className="pairing-error" role="alert">
            {state.message}
          </p>
        )}
      </div>
    </section>
  );

  return compact ? (
    <details className="computer-onboarding-disclosure">
      <summary>Connect another Computer</summary>
      {content}
    </details>
  ) : (
    content
  );
}

function pairingError(error: unknown): string {
  if (error instanceof UnsafePairingOriginError) return error.message;
  const connectError = ConnectError.from(error);
  if (
    connectError.code === Code.Unauthenticated ||
    connectError.code === Code.PermissionDenied
  ) {
    return "Your Human session cannot create pairing. Sign in again, then retry.";
  }
  if (connectError.code === Code.InvalidArgument) {
    return "The pairing request was rejected. Create a new connection command.";
  }
  if (connectError.code === Code.Unavailable) {
    return "The Server is unavailable. Check the connection and retry.";
  }
  return "Could not create a connection command. Retry when the Server is available.";
}
