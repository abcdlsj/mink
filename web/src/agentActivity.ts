import type { Agent } from "./api/client";

export function activityLabel(status?: Agent["activity_status"]): string {
  return status ? status.charAt(0).toUpperCase() + status.slice(1) : "Agent";
}
