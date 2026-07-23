import { useEffect, useState } from "react";

export type Theme = "light" | "dark";
type ThemePreference = Theme | "system";

const storageKey = "sumi.theme";

function systemTheme(): Theme {
  return typeof window.matchMedia === "function" &&
    window.matchMedia("(prefers-color-scheme: dark)").matches
    ? "dark"
    : "light";
}

function storedTheme(): Theme | undefined {
  try {
    const value = window.localStorage.getItem(storageKey);
    return value === "light" || value === "dark" ? value : undefined;
  } catch {
    return undefined;
  }
}

function applyTheme(theme: Theme) {
  document.documentElement.dataset.theme = theme;
  document.documentElement.style.colorScheme = theme;
}

export function initializeTheme() {
  applyTheme(storedTheme() ?? systemTheme());
}

export function useTheme() {
  const [preference, setPreference] = useState<ThemePreference>(() => {
    const stored = storedTheme();
    return stored ?? "system";
  });
  const [system, setSystem] = useState<Theme>(systemTheme);
  const theme = preference === "system" ? system : preference;

  useEffect(() => {
    if (typeof window.matchMedia !== "function") return;
    const query = window.matchMedia("(prefers-color-scheme: dark)");
    const update = () => setSystem(query.matches ? "dark" : "light");
    query.addEventListener?.("change", update);
    return () => query.removeEventListener?.("change", update);
  }, []);

  useEffect(() => {
    applyTheme(theme);
    try {
      if (preference === "system") window.localStorage.removeItem(storageKey);
      else window.localStorage.setItem(storageKey, preference);
    } catch {
      // Theme remains usable for this session when storage is unavailable.
    }
  }, [preference, theme]);

  const toggle = () => {
    setPreference(theme === "light" ? "dark" : "light");
  };

  return { theme, toggle };
}
