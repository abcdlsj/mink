import { Link, useLocation } from "@tanstack/react-router";
import { BarChart3, Network } from "lucide-react";

export function InsightsNavigation({ spaceSlug }: { spaceSlug: string }) {
  const location = useLocation();
  const path = location.pathname;
  return (
    <div className="insights-navigation" aria-label="Agent insights">
      <p className="nav-label">AGENT INSIGHTS</p>
      <Link
        className={`context-entity-row insights-nav-item${
          path.endsWith("/insights/stats") ? " context-entity-row--active" : ""
        }`}
        to="/s/$spaceSlug/insights/stats"
        params={{ spaceSlug }}
      >
        <BarChart3 aria-hidden="true" />
        <span>
          <strong>Statistics</strong>
          <small>Token usage per Agent</small>
        </span>
      </Link>
      <Link
        className={`context-entity-row insights-nav-item${
          path.endsWith("/insights/graph") ? " context-entity-row--active" : ""
        }`}
        to="/s/$spaceSlug/insights/graph"
        params={{ spaceSlug }}
      >
        <Network aria-hidden="true" />
        <span>
          <strong>Graph</strong>
          <small>Agent relationships</small>
        </span>
      </Link>
    </div>
  );
}
