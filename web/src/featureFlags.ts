import { useSyncExternalStore } from "react";

export function designLabEnabled(
  value: string | undefined = import.meta.env.VITE_DESIGN_LAB_ENABLED,
): boolean {
  return value === "true" || value === "1";
}

const EXPERIMENTAL_STORAGE_KEY = "sumi.experimental_features";

let experimentalEnabled = loadExperimentalEnabled();

const experimentalListeners = new Set<() => void>();

function loadExperimentalEnabled(): boolean {
  if (typeof window === "undefined") return false;
  return window.localStorage?.getItem(EXPERIMENTAL_STORAGE_KEY) === "1";
}

export function experimentalFeaturesEnabled(): boolean {
  return experimentalEnabled;
}

export function setExperimentalFeaturesEnabled(value: boolean): void {
  experimentalEnabled = value;
  window.localStorage?.setItem(EXPERIMENTAL_STORAGE_KEY, value ? "1" : "0");
  for (const listener of experimentalListeners) listener();
}

/** Subscribes the current component to the experimental-features toggle. */
export function useExperimentalFeatures(): boolean {
  return useSyncExternalStore(
    (listener) => {
      experimentalListeners.add(listener);
      return () => experimentalListeners.delete(listener);
    },
    experimentalFeaturesEnabled,
  );
}
