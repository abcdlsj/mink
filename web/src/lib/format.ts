import { timestampDate, type Timestamp } from "@bufbuild/protobuf/wkt";
import { EngineKind, type Agent } from "../gen/sumi/agent/v1/agent_pb";
import {
  Architecture,
  OperatingSystem,
} from "../gen/sumi/computer/v1/computer_pb";
import { PlacementState } from "../gen/sumi/placement/v1/placement_pb";

export function agentDisplayName(agent: Agent) {
  return agent.profile?.displayName || agent.handle || "Unknown Agent";
}

export function engineKindLabel(engine?: EngineKind) {
  if (engine === EngineKind.BUILTIN) return "Builtin";
  if (engine === EngineKind.CODEX_ADAPTER) return "Codex adapter";
  if (engine === EngineKind.CLAUDE_ADAPTER) return "Claude adapter";
  return "Not configured";
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
  if (state === PlacementState.READY) return "ready";
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
