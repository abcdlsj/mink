export type TaskColumn = "todo" | "doing" | "review";

export function normalizeCapability(cap: string): string {
  const s = cap.trim().toLowerCase().replaceAll("_", ".").replaceAll(":", ".");
  if (s === "execute" || s === "exec") return "task.execute";
  if (s === "assign") return "task.assign";
  if (s === "create") return "task.create";
  if (s === "review") return "task.review";
  if (s === "plan") return "task.plan";
  return s;
}

export function taskColumn(status?: string): TaskColumn {
  const s = (status || "").toLowerCase();
  if (s === "queued" || s === "todo") return "todo";
  if (s === "in_review" || s === "in-review" || s === "review") return "review";
  return "doing";
}

export function failureStatus(status?: string): boolean {
  const s = (status || "").toLowerCase();
  return s === "failed" || s === "error" || s === "canceled" || s === "rollback_failed" || s === "no_output";
}

export function statusLabel(status?: string): string {
  const s = (status || "").toLowerCase();
  if (s === "running" || s === "working") return "working";
  if (s === "queued" || s === "todo") return "queued";
  if (s === "in_review" || s === "in-review" || s === "review") return "in review";
  if (failureStatus(s)) return "attention";
  return s || "idle";
}

export function shortenText(text: string, max: number): string {
  const s = text.replace(/\s+/g, " ").trim();
  if (s.length <= max) return s;
  return s.slice(0, max - 1).trimEnd() + "...";
}
