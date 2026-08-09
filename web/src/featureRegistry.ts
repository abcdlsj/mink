import { useSyncExternalStore } from "react";

export type FeatureKind = "experimental";

/** A config field a feature registers. New field kinds can be added and rendered in Settings. */
export interface FeatureConfigField {
  id: string;
  label: string;
  kind: "toggle";
  description?: string;
}

export interface RegisteredFeature {
  id: string;
  name: string;
  kind: FeatureKind;
  description: string;
  storageKey: string;
  /** Feature-specific config fields; experimental features always get an enable toggle. */
  fields: FeatureConfigField[];
}

export const AGENT_GRAPH_FEATURE_ID = "agent-graph";

export const REGISTERED_FEATURES: readonly RegisteredFeature[] = [
  {
    id: AGENT_GRAPH_FEATURE_ID,
    name: "Agent graph",
    kind: "experimental",
    description: "Agent-to-Agent coordination graph shown in the left rail.",
    storageKey: "sumi.feature.agent-graph",
    fields: [],
  },
];

export function registeredFeature(id: string): RegisteredFeature | undefined {
  return REGISTERED_FEATURES.find((feature) => feature.id === id);
}

export function featureEnabled(id: string): boolean {
  if (typeof window === "undefined") return false;
  const feature = registeredFeature(id);
  if (!feature) return false;
  return window.localStorage?.getItem(feature.storageKey) === "1";
}

export function setFeatureEnabled(id: string, enabled: boolean): void {
  const feature = registeredFeature(id);
  if (!feature) return;
  window.localStorage?.setItem(feature.storageKey, enabled ? "1" : "0");
  stateCache?.set(id, enabled);
  for (const listener of featureListeners) listener();
}

const featureListeners = new Set<() => void>();
let stateCache: Map<string, boolean> | undefined;

function subscribe(listener: () => void): () => void {
  featureListeners.add(listener);
  return () => featureListeners.delete(listener);
}

/** Subscribes the current component to one registered feature's persisted state. */
export function useFeatureEnabled(id: string): boolean {
  return useSyncExternalStore(subscribe, () => featureEnabled(id));
}

/** One stable snapshot of every registered feature's enabled state, for list rendering. */
export function useFeatureStates(): ReadonlyMap<string, boolean> {
  return useSyncExternalStore(subscribe, () => {
    stateCache ??= new Map(
      REGISTERED_FEATURES.map((feature) => [feature.id, featureEnabled(feature.id)]),
    );
    return stateCache;
  });
}
