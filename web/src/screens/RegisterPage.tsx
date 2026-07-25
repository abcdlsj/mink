import { useMutation } from "@tanstack/react-query";
import { useNavigate, useSearch } from "@tanstack/react-router";
import { ArrowRight, Asterisk, LoaderCircle } from "lucide-react";
import type { FormEvent } from "react";

import { register } from "../api/client";

export function RegisterPage() {
  const navigate = useNavigate();
  const { redirect } = useSearch({ from: "/" });
  const registration = useMutation({
    mutationFn: register,
    onSuccess: () => {
      if (isSafeRedirect(redirect)) {
        window.location.assign(redirect);
      } else {
        void navigate({ to: "/spaces/new" });
      }
    },
  });

  function submit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    const form = new FormData(event.currentTarget);
    registration.mutate({
      display_name: String(form.get("displayName") ?? ""),
      email: String(form.get("email") ?? ""),
      password: String(form.get("password") ?? ""),
    });
  }

  return (
    <main className="onboarding-shell">
      <header className="brand-bar">
        <a className="wordmark" href="/" aria-label="Sumi home">
          <span className="wordmark-mark" aria-hidden="true">
            <Asterisk strokeWidth={3} />
          </span>
          SUMI
        </a>
        <span className="step-index">01 / 03</span>
      </header>

      <section className="register-stage" aria-labelledby="register-title">
        <div className="stage-copy">
          <p className="eyebrow">YOUR SEAT IS READY</p>
          <h1 id="register-title">Join the room.</h1>
          <p className="stage-note">
            Start with your Human identity. Your first Space comes next.
          </p>
          <div className="member-signal" aria-hidden="true">
            <PixelFace variant="sun" />
            <PixelFace variant="cyan" />
            <PixelFace variant="pink" />
            <span className="signal-line" />
          </div>
        </div>

        <form className="register-form" onSubmit={submit}>
          <label>
            Display name
            <input name="displayName" autoComplete="name" required maxLength={40} />
          </label>
          <label>
            Email
            <input name="email" type="email" autoComplete="email" required />
          </label>
          <label>
            Password
            <input
              name="password"
              type="password"
              autoComplete="new-password"
              minLength={10}
              required
            />
          </label>
          {registration.error ? (
            <p className="form-error" role="alert">
              {registration.error.message}
            </p>
          ) : null}
          <button className="primary-action" type="submit" disabled={registration.isPending}>
            {registration.isPending ? "Creating account" : "Continue"}
            {registration.isPending ? (
              <LoaderCircle className="spin" aria-hidden="true" />
            ) : (
              <ArrowRight aria-hidden="true" />
            )}
          </button>
          <p className="form-switch">
            Already a Member? <a href="/login">Sign in</a>
          </p>
        </form>
      </section>

      <footer className="onboarding-footer">
        <span>HUMANS + AGENTS</span>
        <span className="status-light">SYSTEM READY</span>
      </footer>
    </main>
  );
}

function isSafeRedirect(value: string | undefined): value is string {
  return Boolean(value?.startsWith("/") && !value.startsWith("//"));
}

function PixelFace({ variant }: { variant: "sun" | "cyan" | "pink" }) {
  return (
    <span className={`pixel-face pixel-face--${variant}`}>
      <span className="pixel-eye pixel-eye--left" />
      <span className="pixel-eye pixel-eye--right" />
      <span className="pixel-mouth" />
    </span>
  );
}
