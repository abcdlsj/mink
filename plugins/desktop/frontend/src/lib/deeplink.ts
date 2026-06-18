export type WebRoute =
  | { view: "home" }
  | { view: "channel"; id: string; thread?: string; anchor?: string }
  | { view: "direct"; id: string; anchor?: string }
  | { view: "agent"; id: string; anchor?: string }
  | { view: "tasks"; id?: string; anchor?: string };

export interface RouteWriteOptions {
  replace?: boolean;
}

function browserLocation(): Location | null {
  if (typeof window === "undefined") return null;
  return window.location;
}

export function parseWebRoute(): WebRoute | null {
  const loc = browserLocation();
  if (!loc) return null;
  const q = new URLSearchParams(loc.search);
  const view = q.get("view") || "";
  const id = q.get("id") || "";
  const anchor = q.get("anchor") || undefined;
  if (view === "home") return { view };
  if (view === "tasks") return { view, id: id || undefined, anchor };
  if (!id) return null;
  if (view === "channel") {
    const thread = q.get("thread") || undefined;
    return { view, id, thread, anchor };
  }
  if (view === "direct" || view === "agent") return { view, id, anchor };
  return null;
}

export function currentRouteKey(): string {
  const loc = browserLocation();
  if (!loc) return "";
  return loc.pathname + loc.search;
}

export function writeWebRoute(route: WebRoute, opts: RouteWriteOptions = {}) {
  if (typeof window === "undefined") return;
  const q = new URLSearchParams();
  q.set("view", route.view);
  if (route.view !== "home" && route.id) q.set("id", route.id);
  if (route.view === "channel" && route.thread) q.set("thread", route.thread);
  if (route.view !== "home" && route.anchor) q.set("anchor", route.anchor);
  const next = window.location.pathname + "?" + q.toString();
  if (next === currentRouteKey()) return;
  if (opts.replace) {
    window.history.replaceState(null, "", next);
  } else {
    window.history.pushState(null, "", next);
  }
}

export function writeRouteAnchor(anchor: string | null, opts: RouteWriteOptions = {}) {
  const route = parseWebRoute();
  if (!route) return;
  writeWebRoute({ ...route, anchor: anchor || undefined } as WebRoute, opts);
}
