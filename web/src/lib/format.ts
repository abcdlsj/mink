import { timestampDate, type Timestamp } from "@bufbuild/protobuf/wkt";
import { Driver } from "../gen/sumi/agent/v1/agent_pb";
import {
  Architecture,
  OperatingSystem,
} from "../gen/sumi/computer/v1/computer_pb";
import { PlacementState } from "../gen/sumi/placement/v1/placement_pb";

export function driverLabel(driver: Driver) {
  if (driver === Driver.NATIVE) return "Native";
  if (driver === Driver.CODEX) return "Codex";
  if (driver === Driver.CLAUDE) return "Claude";
  return "Unspecified";
}

export function operatingSystemLabel(os: OperatingSystem) {
  if (os === OperatingSystem.MACOS) return "macOS";
  if (os === OperatingSystem.LINUX) return "Linux";
  return "Unknown OS";
}

export function architectureLabel(arch: Architecture) {
  if (arch === Architecture.ARM64) return "arm64";
  if (arch === Architecture.AMD64) return "amd64";
  return "unknown arch";
}

export function placementStateLabel(state?: PlacementState) {
  if (state === PlacementState.PENDING) return "pending";
  if (state === PlacementState.ACTIVE) return "active";
  if (state === PlacementState.FAILED) return "failed";
  return "unplaced";
}

export function formatTimestamp(value?: Timestamp) {
  if (!value) return "Not recorded";
  return new Intl.DateTimeFormat(undefined, {
    dateStyle: "medium",
    timeStyle: "short",
  }).format(timestampDate(value));
}

export function shortId(value: string) {
  return value.slice(0, 8);
}
