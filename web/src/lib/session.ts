export type BrowserHuman = {
  id: string;
  name: string;
};

export type LocalSetupInput = {
  username: string;
  password: string;
  bootstrapCredential: string;
};

export type LocalLoginInput = {
  username: string;
  password: string;
};

export type LocalAuthErrorCode =
  | "invalid_credentials"
  | "rate_limited"
  | "setup_complete"
  | "invalid_input"
  | "unavailable";

export class LocalAuthError extends Error {
  constructor(
    readonly code: LocalAuthErrorCode,
    message: string,
  ) {
    super(message);
    this.name = "LocalAuthError";
  }
}

export async function getSession(): Promise<BrowserHuman | undefined> {
  const response = await fetch("/auth/session", {
    credentials: "same-origin",
    cache: "no-store",
    headers: { Accept: "application/json" },
  });
  if (response.status === 401 || response.status === 404) return undefined;
  if (!response.ok) throw new Error("Session status unavailable");
  const payload: unknown = await response.json();
  if (!isSessionPayload(payload)) throw new Error("Session response invalid");
  return payload.human;
}

export async function logoutSession(): Promise<void> {
  const response = await fetch("/auth/logout", {
    method: "POST",
    credentials: "same-origin",
    cache: "no-store",
  });
  if (response.status !== 204 && response.status !== 401) {
    throw new Error("Logout unavailable");
  }
}

export async function getLocalSetupRequired(): Promise<boolean> {
  const response = await fetch("/auth/local", {
    credentials: "same-origin",
    cache: "no-store",
    headers: { Accept: "application/json" },
  });
  if (!response.ok) throw new Error("Local authentication status unavailable");
  const payload: unknown = await response.json();
  if (!isLocalStatusPayload(payload)) {
    throw new Error("Local authentication status invalid");
  }
  return payload.setup_required;
}

export async function setupLocalAccount(
  input: LocalSetupInput,
): Promise<BrowserHuman> {
  return submitLocalAuth("/auth/local/setup", {
    username: input.username,
    password: input.password,
    bootstrap_credential: input.bootstrapCredential,
  });
}

export async function loginLocalAccount(
  input: LocalLoginInput,
): Promise<BrowserHuman> {
  return submitLocalAuth("/auth/local/login", input);
}

async function submitLocalAuth(
  path: string,
  input: Record<string, string>,
): Promise<BrowserHuman> {
  const response = await fetch(path, {
    method: "POST",
    credentials: "same-origin",
    cache: "no-store",
    headers: { Accept: "application/json", "Content-Type": "application/json" },
    body: JSON.stringify(input),
  });
  if (!response.ok) throw localAuthFailure(path, response.status);
  const payload: unknown = await response.json();
  if (!isSessionPayload(payload)) throw new Error("Session response invalid");
  return payload.human;
}

function localAuthFailure(path: string, status: number): LocalAuthError {
  if (status === 429) {
    return new LocalAuthError(
      "rate_limited",
      "Too many attempts. Wait a minute, then try again.",
    );
  }
  if (path.endsWith("/setup") && status === 409) {
    return new LocalAuthError(
      "setup_complete",
      "Local setup is already complete. Sign in instead.",
    );
  }
  if (status === 401) {
    return new LocalAuthError(
      "invalid_credentials",
      path.endsWith("/setup")
        ? "The setup key was not accepted."
        : "Username or password is incorrect.",
    );
  }
  if (status === 400 || status === 403) {
    return new LocalAuthError(
      "invalid_input",
      "Check the fields and submit from this local Sumi window.",
    );
  }
  return new LocalAuthError(
    "unavailable",
    "Local authentication is unavailable.",
  );
}

function isSessionPayload(value: unknown): value is { human: BrowserHuman } {
  if (!value || typeof value !== "object" || !("human" in value)) return false;
  const human = value.human;
  return (
    !!human &&
    typeof human === "object" &&
    "id" in human &&
    typeof human.id === "string" &&
    human.id.length > 0 &&
    "name" in human &&
    typeof human.name === "string" &&
    human.name.length > 0
  );
}

function isLocalStatusPayload(
  value: unknown,
): value is { setup_required: boolean } {
  return (
    !!value &&
    typeof value === "object" &&
    "setup_required" in value &&
    typeof value.setup_required === "boolean"
  );
}
