import { Outlet, useLocation } from "@tanstack/react-router";
import { useEffect } from "react";

function routeTitle(pathname: string): string | undefined {
  if (pathname === "/login") return "Sign in";
  if (pathname === "/") return "Register";
  if (pathname === "/spaces/new") return "Create Space";
  if (pathname.startsWith("/invite/")) return "Invitation";
  if (pathname.startsWith("/pair-computer/")) return "Pair Computer";
  if (pathname.includes("/members")) return "Members";
  if (pathname.includes("/inbox")) return "Inbox";
  if (pathname.includes("/tasks")) return "Tasks";
  if (pathname.includes("/computers")) return "Computers";
  if (pathname.includes("/agents/")) return "Agent";
  if (pathname.includes("/channels/") || pathname.includes("/dm/")) return "Conversation";
  return undefined;
}

export function RouteChrome() {
  const location = useLocation();
  useEffect(() => {
    const page = routeTitle(location.pathname);
    document.title = page ? `${page} · Sumi` : "Sumi";
  }, [location.pathname]);
  return <Outlet />;
}
