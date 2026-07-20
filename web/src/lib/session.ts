export type BrowserHuman = {
  id: string;
  name: string;
};

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
