import { useMutation } from "@tanstack/react-query";
import { Link, useNavigate } from "@tanstack/react-router";
import { ArrowRight, LoaderCircle } from "lucide-react";
import { type FormEvent, useState } from "react";

import { createSpace } from "../api/client";
import { OnboardingProgress } from "./RegisterPage";
import { SumiLogo } from "../components/SumiLogo";

const accents = [
  { value: "#F0602F", label: "Flame" },
  { value: "#E8B42D", label: "Gold" },
  { value: "#3C9E8F", label: "Teal" },
  { value: "#6C5CE7", label: "Iris" },
] as const;

export function SpaceCreatePage() {
  const navigate = useNavigate();
  const [slug, setSlug] = useState("");
  const [slugEdited, setSlugEdited] = useState(false);
  const [accent, setAccent] = useState<string>(accents[0].value);
  const creation = useMutation({
    mutationFn: createSpace,
    onSuccess: (space) =>
      navigate({
        to: "/s/$spaceSlug/channels/$channelSlug",
        params: { spaceSlug: space.slug, channelSlug: "general" },
      }),
  });

  function submit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    const form = new FormData(event.currentTarget);
    creation.mutate({
      name: String(form.get("name") ?? ""),
      slug: String(form.get("slug") ?? ""),
      accent: String(form.get("accent") ?? accents[0].value),
    });
  }

  return (
    <main className="onboarding-shell">
      <header className="brand-bar">
        <Link className="wordmark" to="/" search={{ redirect: undefined }} aria-label="Sumi home">
          <span className="wordmark-mark" aria-hidden="true">
            <SumiLogo />
          </span>
          SUMI
        </Link>
        <OnboardingProgress current={2} />
      </header>

      <section className="register-stage" aria-labelledby="space-title">
        <div className="stage-copy stage-copy--space" style={{ backgroundColor: accent }}>
          <p className="eyebrow">02 · COLLABORATION BOUNDARY</p>
          <h1 id="space-title">Create your Space.</h1>
          <p className="stage-note">Members, Channels and Computers live inside this boundary.</p>
          <p className="space-address" aria-live="polite">/s/{slug || "your-space"}</p>
        </div>

        <form className="register-form" onSubmit={submit}>
          <label>
            Space name
            <input
              name="name"
              autoFocus
              required
              maxLength={60}
              onChange={(event) => {
                if (!slugEdited) setSlug(toSlug(event.target.value));
              }}
            />
          </label>
          <label>
            URL slug
            <span className="slug-input">
              <span aria-hidden="true">/s/</span>
              <input
                name="slug"
                value={slug}
                required
                minLength={3}
                maxLength={32}
                pattern="[a-z0-9]+(?:-[a-z0-9]+)*"
                onChange={(event) => {
                  setSlugEdited(true);
                  setSlug(event.target.value.toLowerCase());
                }}
              />
            </span>
          </label>
          <fieldset className="accent-fieldset">
            <legend>Space accent</legend>
            <div className="accent-options">
              {accents.map((option, index) => (
                <label key={option.value} className="accent-option" title={option.label}>
                  <input
                    type="radio"
                    name="accent"
                    value={option.value}
                    aria-label={`${option.label} accent`}
                    defaultChecked={index === 0}
                    onChange={() => setAccent(option.value)}
                  />
                  <span style={{ backgroundColor: option.value }} aria-hidden="true" />
                </label>
              ))}
            </div>
          </fieldset>
          {creation.error ? (
            <p className="form-error" role="alert">
              {creation.error.message}
            </p>
          ) : null}
          <button className="primary-action" type="submit" disabled={creation.isPending}>
            {creation.isPending ? "Building Space" : "Enter general"}
            {creation.isPending ? (
              <LoaderCircle className="spin" aria-hidden="true" />
            ) : (
              <ArrowRight aria-hidden="true" />
            )}
          </button>
        </form>
      </section>

      <footer className="onboarding-footer">
        <span>SPACE IS THE BOUNDARY</span>
        <span className="status-light">SESSION ACTIVE</span>
      </footer>
    </main>
  );
}

function toSlug(value: string): string {
  return value
    .normalize("NFKD")
    .toLowerCase()
    .replace(/[^a-z0-9]+/g, "-")
    .replace(/^-+|-+$/g, "")
    .slice(0, 32)
    .replace(/-+$/g, "");
}
