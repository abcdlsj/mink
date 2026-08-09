import { Link, Outlet, useParams } from "@tanstack/react-router";
import { useQuery } from "@tanstack/react-query";

import { getSpaceBySlug } from "../api/client";
import { SpaceShell } from "../components/SpaceShell";
import { AGENT_INSIGHTS_FEATURE_ID, useFeatureEnabled } from "../featureRegistry";
import { AgentGraphWorkspace } from "./AgentGraphPage";

export function InsightsPage() {
  const { spaceSlug } = useParams({ from: "/s/$spaceSlug/insights" });
  const enabled = useFeatureEnabled(AGENT_INSIGHTS_FEATURE_ID);
  if (!enabled) {
    return (
      <SpaceShell spaceSlug={spaceSlug} active="insights">
        {() => (
          <div className="route-status">
            <section className="route-status-panel">
              <p className="section-kicker">EXPERIMENTAL</p>
              <h1>Agent insights is disabled.</h1>
              <p>Enable experimental features in Settings to show this entry in the rail.</p>
              <Link
                className="command-button command-button--accent"
                to="/s/$spaceSlug/settings"
                params={{ spaceSlug }}
              >
                Open Settings
              </Link>
            </section>
          </div>
        )}
      </SpaceShell>
    );
  }
  return (
    <SpaceShell spaceSlug={spaceSlug} active="insights">
      {() => <Outlet />}
    </SpaceShell>
  );
}

export function AgentGraphRouteContent() {
  const { spaceSlug } = useParams({ from: "/s/$spaceSlug/insights/graph" });
  const space = useQuery({
    queryKey: ["space", spaceSlug],
    queryFn: () => getSpaceBySlug(spaceSlug),
  });
  if (space.isPending) return <div className="route-status">Opening Agent graph...</div>;
  if (!space.data) {
    return (
      <div className="route-status route-status--error">
        <p>Space is unavailable.</p>
      </div>
    );
  }
  return <AgentGraphWorkspace spaceId={space.data.id} spaceSlug={space.data.slug} />;
}
