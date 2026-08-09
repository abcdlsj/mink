import { useParams } from "@tanstack/react-router";

import { SpaceShell } from "../components/SpaceShell";
import {
  setExperimentalFeaturesEnabled,
  useExperimentalFeatures,
} from "../featureFlags";
import "./settings.css";

export function SettingsPage() {
  const { spaceSlug } = useParams({ from: "/s/$spaceSlug/settings" });
  return (
    <SpaceShell spaceSlug={spaceSlug} active="settings">
      {() => <SettingsWorkspace />}
    </SpaceShell>
  );
}

export function SettingsWorkspace() {
  const experimental = useExperimentalFeatures();
  return (
    <div className="settings-page">
      <header className="settings-header">
        <h1>Settings</h1>
        <p>Preferences for this Space and this browser.</p>
      </header>

      <section className="settings-section" aria-labelledby="experimental-heading">
        <div>
          <h2 id="experimental-heading">Experimental features</h2>
          <p>
            Unstable features stay hidden by default. Turn this on to show experimental entries in
            the left rail.
          </p>
        </div>
        <label className="settings-toggle">
          <input
            type="checkbox"
            checked={experimental}
            onChange={(event) => setExperimentalFeaturesEnabled(event.target.checked)}
          />
          <span>Enable experimental features</span>
        </label>
      </section>

      <p className="settings-note">
        Agent graph is the first experimental feature; it appears in the left rail only while this
        toggle is on.
      </p>
    </div>
  );
}
