import { useState, type FormEvent } from "react";
import { KeyRound, LogIn, ShieldCheck, UserRound } from "lucide-react";
import type { useSession } from "../hooks/useSession";

type Session = ReturnType<typeof useSession>;
type FieldErrors = Partial<
  Record<"username" | "password" | "bootstrapCredential", string>
>;

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
  const [fieldErrors, setFieldErrors] = useState<FieldErrors>({});

  const submit = (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault();
    const invalid = validateLocalAuth(
      { username, password, bootstrapCredential },
      setupRequired,
    );
    setFieldErrors(invalid);
    if (Object.keys(invalid).length > 0) return;
    if (setupRequired) {
      void session.setup({ username, password, bootstrapCredential });
    } else {
      void session.login({ username, password });
    }
  };

  const edit =
    (field: keyof FieldErrors, setter: (value: string) => void) =>
    (value: string) => {
      setter(value);
      setFieldErrors((current) => ({ ...current, [field]: undefined }));
      session.clearAuthError();
    };

  return (
    <div className="timeline authentication-timeline">
      <section className="local-auth-panel" aria-labelledby="local-auth-title">
        <div className="local-auth-context">
          <span className="local-auth-kicker">
            <ShieldCheck size={14} aria-hidden="true" />
            Your Sumi workspace
          </span>
          <h2 id="local-auth-title">
            {setupRequired ? "Create your local account" : "Welcome back"}
          </h2>
          <p>
            {setupRequired
              ? "Join this Sumi as its first Human. The Owner setup key is used once, then stays out of your account."
              : "Sign in as a Sumi Human. Your password never leaves this Server."}
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

        <form className="local-auth-form" onSubmit={submit} noValidate>
          <label>
            <span>Username</span>
            <span
              className={`auth-input-frame ${fieldErrors.username ? "invalid" : ""}`}
            >
              <UserRound size={16} aria-hidden="true" />
              <input
                name="username"
                aria-label="Username"
                aria-describedby="username-rule username-error"
                aria-invalid={!!fieldErrors.username}
                autoComplete="username"
                value={username}
                onChange={(event) =>
                  edit("username", setUsername)(event.target.value)
                }
                minLength={3}
                maxLength={32}
                pattern="[A-Za-z0-9][A-Za-z0-9._-]{2,31}"
                spellCheck={false}
                disabled={pending}
                required
              />
            </span>
            <small id="username-rule">
              3–32 characters. Start with a letter or number; then use letters,
              numbers, dot, underscore, or hyphen.
            </small>
            {fieldErrors.username && (
              <small className="auth-field-error" id="username-error">
                {fieldErrors.username}
              </small>
            )}
          </label>

          <label>
            <span>Password</span>
            <span
              className={`auth-input-frame ${fieldErrors.password ? "invalid" : ""}`}
            >
              <KeyRound size={16} aria-hidden="true" />
              <input
                name="password"
                aria-label="Password"
                aria-describedby="password-rule password-error"
                aria-invalid={!!fieldErrors.password}
                type="password"
                autoComplete={
                  setupRequired ? "new-password" : "current-password"
                }
                value={password}
                onChange={(event) =>
                  edit("password", setPassword)(event.target.value)
                }
                disabled={pending}
                required
              />
            </span>
            <small id="password-rule">
              {setupRequired
                ? "12–256 characters. Spaces and non-Latin characters are allowed."
                : "Use the password for this local account."}
            </small>
            {fieldErrors.password && (
              <small className="auth-field-error" id="password-error">
                {fieldErrors.password}
              </small>
            )}
          </label>

          {setupRequired && (
            <label>
              <span>Owner setup key</span>
              <span
                className={`auth-input-frame ${fieldErrors.bootstrapCredential ? "invalid" : ""}`}
              >
                <ShieldCheck size={16} aria-hidden="true" />
                <input
                  name="bootstrapCredential"
                  aria-label="Owner setup key"
                  aria-describedby="setup-key-rule setup-key-error"
                  aria-invalid={!!fieldErrors.bootstrapCredential}
                  type="password"
                  autoComplete="off"
                  value={bootstrapCredential}
                  onChange={(event) =>
                    edit(
                      "bootstrapCredential",
                      setBootstrapCredential,
                    )(event.target.value)
                  }
                  minLength={43}
                  maxLength={128}
                  disabled={pending}
                  required
                />
              </span>
              <small id="setup-key-rule">
                Paste the credential from human.key (or owner.key). Sumi
                verifies it once and never stores it with this account.
              </small>
              {fieldErrors.bootstrapCredential && (
                <small className="auth-field-error" id="setup-key-error">
                  {fieldErrors.bootstrapCredential}
                </small>
              )}
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
                ? "Join Sumi"
                : "Sign in"}
          </button>
        </form>
      </section>
    </div>
  );
}

function validateLocalAuth(
  input: {
    username: string;
    password: string;
    bootstrapCredential: string;
  },
  setupRequired: boolean,
): FieldErrors {
  const errors: FieldErrors = {};
  if (!/^[A-Za-z0-9][A-Za-z0-9._-]{2,31}$/.test(input.username)) {
    errors.username = "Enter a valid 3–32 character username.";
  }
  if (!input.password) {
    errors.password = "Enter your password.";
  } else if (setupRequired) {
    const characters = Array.from(input.password).length;
    if (
      characters < 12 ||
      characters > 256 ||
      input.password.trim().length === 0
    ) {
      errors.password = "Use 12–256 characters with at least one non-space.";
    }
  }
  if (
    setupRequired &&
    !/^[A-Za-z0-9_-]{43,128}$/.test(input.bootstrapCredential)
  ) {
    errors.bootstrapCredential =
      "Paste the complete 43–128 character Owner setup key.";
  }
  return errors;
}
