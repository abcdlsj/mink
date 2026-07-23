import { useState, type FormEvent } from "react";
import { KeyRound, LogIn, ShieldCheck, UserRound } from "lucide-react";
import type { useSession } from "../hooks/useSession";

type Session = ReturnType<typeof useSession>;

export function LocalAuthPanel({ session }: { session: Session }) {
  const setupRequired =
    (session.status === "unauthenticated" ||
      session.status === "authenticating") &&
    session.setupRequired;
  const pending = session.status === "authenticating";
  const error =
    session.status === "unauthenticated" ? session.authError : undefined;
  const [username, setUsername] = useState(setupRequired ? "owner" : "");
  const [password, setPassword] = useState("");
  const [bootstrapCredential, setBootstrapCredential] = useState("");

  const submit = (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault();
    if (setupRequired) {
      void session.setup({ username, password, bootstrapCredential });
    } else {
      void session.login({ username, password });
    }
  };

  const edit = (setter: (value: string) => void) => (value: string) => {
    setter(value);
    session.clearAuthError();
  };

  return (
    <div className="timeline authentication-timeline">
      <section className="local-auth-panel" aria-labelledby="local-auth-title">
        <div className="local-auth-context">
          <span className="local-auth-kicker">
            <ShieldCheck size={14} aria-hidden="true" />
            Local control plane
          </span>
          <h2 id="local-auth-title">
            {setupRequired ? "Set up local access" : "Sign in to Sumi"}
          </h2>
          <p>
            {setupRequired
              ? "Bind a local account to the existing Owner. This setup can run only once."
              : "Continue as a Sumi Human. Your password stays on this Server."}
          </p>
          <dl className="local-auth-facts">
            <div>
              <dt>Principal</dt>
              <dd>Human</dd>
            </div>
            <div>
              <dt>Session</dt>
              <dd>12 hours</dd>
            </div>
            <div>
              <dt>Scope</dt>
              <dd>Loopback only</dd>
            </div>
          </dl>
        </div>

        <form className="local-auth-form" onSubmit={submit}>
          <label>
            <span>Username</span>
            <span className="auth-input-frame">
              <UserRound size={16} aria-hidden="true" />
              <input
                name="username"
                autoComplete="username"
                value={username}
                onChange={(event) => edit(setUsername)(event.target.value)}
                minLength={3}
                maxLength={32}
                pattern="[A-Za-z0-9][A-Za-z0-9._-]{2,31}"
                spellCheck={false}
                disabled={pending}
                required
              />
            </span>
          </label>

          <label>
            <span>Password</span>
            <span className="auth-input-frame">
              <KeyRound size={16} aria-hidden="true" />
              <input
                name="password"
                type="password"
                autoComplete={
                  setupRequired ? "new-password" : "current-password"
                }
                value={password}
                onChange={(event) => edit(setPassword)(event.target.value)}
                minLength={12}
                maxLength={256}
                disabled={pending}
                required
              />
            </span>
          </label>

          {setupRequired && (
            <label>
              <span>Owner setup key</span>
              <span className="auth-input-frame">
                <ShieldCheck size={16} aria-hidden="true" />
                <input
                  name="bootstrapCredential"
                  aria-label="Owner setup key"
                  type="password"
                  autoComplete="off"
                  value={bootstrapCredential}
                  onChange={(event) =>
                    edit(setBootstrapCredential)(event.target.value)
                  }
                  minLength={43}
                  maxLength={128}
                  disabled={pending}
                  required
                />
              </span>
              <small>
                Use your local Human credential from human.key (or an explicitly
                configured owner.key). Sumi verifies it once and never stores it
                with this account.
              </small>
            </label>
          )}

          {error && (
            <p className="local-auth-error" role="alert">
              {error}
            </p>
          )}

          <button
            className="local-auth-submit"
            type="submit"
            disabled={pending}
          >
            {setupRequired ? <ShieldCheck size={16} /> : <LogIn size={16} />}
            {pending
              ? setupRequired
                ? "Creating local account"
                : "Signing in"
              : setupRequired
                ? "Create local account"
                : "Sign in"}
          </button>
        </form>
      </section>
    </div>
  );
}
