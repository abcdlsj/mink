import { useLocation, useParams } from "@tanstack/react-router";
import { useState } from "react";

import { SpaceShell } from "../components/SpaceShell";
import {
  type FeatureConfigField,
  type RegisteredFeature,
  registeredFeature,
  setFeatureEnabled,
  useFeatureEnabled,
} from "../featureRegistry";
import "./settings.css";

export function SettingsPage() {
  const { spaceSlug } = useParams({ from: "/s/$spaceSlug/settings" });
  const location = useLocation();
  const hash = String(location.hash).replace(/^#/, "");
  const selectedFeatureId = hash.startsWith("feature-")
    ? hash.slice("feature-".length)
    : undefined;
  return (
    <SpaceShell spaceSlug={spaceSlug} active="settings">
      {() => <SettingsWorkspace selectedFeatureId={selectedFeatureId} />}
    </SpaceShell>
  );
}

export function SettingsWorkspace({ selectedFeatureId }: { selectedFeatureId?: string }) {
  const feature = selectedFeatureId ? registeredFeature(selectedFeatureId) : undefined;
  if (!feature) {
    return (
      <div className="settings-page settings-page--empty">
        <header className="settings-header">
          <h1>Settings</h1>
          <p>Choose a feature from the list to configure it.</p>
        </header>
        <p className="settings-note">
          Every feature registers its own config items; experimental features include an enable
          switch.
        </p>
      </div>
    );
  }
  return <FeatureDetail feature={feature} />;
}

function FeatureDetail({ feature }: { feature: RegisteredFeature }) {
  const enabled = useFeatureEnabled(feature.id);
  return (
    <div className="settings-page">
      <header className="settings-detail-header">
        <h1>{feature.name}</h1>
        <span className={`settings-kind settings-kind--${feature.kind}`}>{feature.kind}</span>
      </header>
      <p className="settings-description">{feature.description}</p>

      <section className="settings-section" aria-labelledby="feature-config-heading">
        <div>
          <h2 id="feature-config-heading">Configuration</h2>
          <p>Settings are stored in this browser only.</p>
        </div>
        <div className="settings-config">
          {feature.kind === "experimental" ? (
            <label className="settings-toggle">
              <input
                type="checkbox"
                checked={enabled}
                onChange={(event) => setFeatureEnabled(feature.id, event.target.checked)}
              />
              <span>Enabled</span>
            </label>
          ) : null}
          {feature.fields.map((field) => (
            <FeatureField key={field.id} feature={feature} field={field} />
          ))}
        </div>
      </section>
    </div>
  );
}

function FeatureField({
  feature,
  field,
}: {
  feature: RegisteredFeature;
  field: FeatureConfigField;
}) {
  const storageKey = `${feature.storageKey}.${field.id}`;
  const [checked, setChecked] = useState(
    () => window.localStorage?.getItem(storageKey) === "1",
  );
  if (field.kind !== "toggle") return null;
  return (
    <label className="settings-toggle">
      <input
        type="checkbox"
        checked={checked}
        onChange={(event) => {
          const value = event.target.checked;
          setChecked(value);
          window.localStorage?.setItem(storageKey, value ? "1" : "0");
        }}
      />
      <span>{field.label}</span>
    </label>
  );
}
