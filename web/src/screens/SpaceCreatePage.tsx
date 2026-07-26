import { useMutation } from "@tanstack/react-query";
import { Link, useNavigate } from "@tanstack/react-router";
import { ArrowRight, Asterisk, LoaderCircle } from "lucide-react";
import { type FormEvent, useState } from "react";

import { createSpace } from "../api/client";

const accents = ["#FFD447", "#FF6FAE", "#64D9E8", "#86D96F"];

export function SpaceCreatePage() {
  const navigate = useNavigate();
  const [slug, setSlug] = useState("");
  const [slugEdited, setSlugEdited] = useState(false);
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
      accent: String(form.get("accent") ?? accents[0]),
    });
  }

  return (
    <main className="onboarding-shell">
      <header className="brand-bar">
        <Link className="wordmark" to="/" search={{ redirect: undefined }} aria-label="Sumi home">
          <span className="wordmark-mark" aria-hidden="true">
            <Asterisk strokeWidth={3} />
          </span>
          SUMI
        </Link>
        <span className="step-index">02 / 03</span>
      </header>

      <section className="register-stage" aria-labelledby="space-title">
        <div className="stage-copy stage-copy--space">
          <p className="eyebrow">NAME THE ROOM</p>
          <h1 id="space-title">Create your Space.</h1>
          <p className="space-address">/s/{slug || "your-space"}</p>
        </div>

        <form className="register-form" onSubmit={submit}>
          <label>
            Space name
            <input
              name="name"
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
              {accents.map((accent, index) => (
                <label key={accent} className="accent-option" title={accent}>
                  <input
                    type="radio"
                    name="accent"
                    value={accent}
                    defaultChecked={index === 0}
                  />
                  <span style={{ backgroundColor: accent }} />
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
