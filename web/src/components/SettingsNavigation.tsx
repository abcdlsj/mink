import { Link, useLocation } from "@tanstack/react-router";

import { REGISTERED_FEATURES, useFeatureStates } from "../featureRegistry";

export function SettingsNavigation({ spaceSlug }: { spaceSlug: string }) {
  const location = useLocation();
  const hash = String(location.hash).replace(/^#/, "");
  const states = useFeatureStates();
  return (
    <div className="settings-navigation" aria-label="Settings features">
      <p className="nav-label">FEATURES</p>
      {REGISTERED_FEATURES.map((feature) => {
        const enabled = states.get(feature.id) ?? false;
        const active = hash === `feature-${feature.id}`;
        return (
          <Link
            key={feature.id}
            className={`context-entity-row settings-nav-item${active ? " context-entity-row--active" : ""}`}
            to="/s/$spaceSlug/settings"
            params={{ spaceSlug }}
            hash={`feature-${feature.id}`}
          >
            <span>
              <strong>{feature.name}</strong>
              <small>{feature.kind}</small>
            </span>
            <span className={`settings-nav-state settings-nav-state--${enabled ? "on" : "off"}`}>
              {enabled ? "On" : "Off"}
            </span>
          </Link>
        );
      })}
    </div>
  );
}
