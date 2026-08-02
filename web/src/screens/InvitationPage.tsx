import { useMutation, useQuery } from "@tanstack/react-query";
import { Link, useNavigate, useParams } from "@tanstack/react-router";
import { ArrowRight, KeyRound, LoaderCircle, LogIn } from "lucide-react";

import {
  ApiRequestError,
  acceptInvitation,
  currentUser,
  getInvitation,
} from "../api/client";
import { SumiLogo } from "../components/SumiLogo";

export function InvitationPage() {
  const { inviteToken } = useParams({ from: "/invite/$inviteToken" });
  const navigate = useNavigate();
  const invitation = useQuery({
    queryKey: ["invitation", inviteToken],
    queryFn: () => getInvitation(inviteToken),
    retry: false,
  });
  const session = useQuery({
    queryKey: ["current-user"],
    queryFn: currentUser,
    retry: false,
  });
  const acceptance = useMutation({
    mutationFn: () => acceptInvitation(inviteToken),
    onSuccess: () => {
      if (!invitation.data) return;
      void navigate({
        to: "/s/$spaceSlug/channels/$channelSlug",
        params: { spaceSlug: invitation.data.space_slug, channelSlug: "general" },
      });
    },
  });

  if (invitation.isPending) {
    return <div className="route-status">Checking invitation...</div>;
  }
  if (invitation.error || !invitation.data) {
    return (
      <div className="route-status route-status--error">
        {invitation.error?.message ?? "Invitation unavailable."}
      </div>
    );
  }

  const redirect = `/invite/${encodeURIComponent(inviteToken)}`;
  const unauthenticated =
    session.error instanceof ApiRequestError && session.error.status === 401;
  const sessionUnavailable = Boolean(session.error && !unauthenticated);

  return (
    <main className="onboarding-shell">
      <header className="brand-bar">
        <Link className="wordmark" to="/" search={{ redirect: undefined }} aria-label="Sumi home">
          <span className="wordmark-mark" aria-hidden="true">
            <SumiLogo />
          </span>
          SUMI
        </Link>
        <span className="step-index">SPACE INVITATION</span>
      </header>

      <section className="register-stage invitation-stage" aria-labelledby="invitation-title">
        <div className="stage-copy stage-copy--invite">
          <p className="eyebrow">A SEAT IN</p>
          <h1 id="invitation-title">{invitation.data.space_name}</h1>
          <p className="space-address">/s/{invitation.data.space_slug}</p>
          <KeyRound className="invitation-key" aria-hidden="true" />
        </div>
        <div className="invitation-action">
          <p className="invitation-label">INVITED EMAIL</p>
          <strong>{invitation.data.email}</strong>
          {sessionUnavailable ? (
            <p className="form-error" role="alert">
              {session.error?.message ?? "Session unavailable."}
            </p>
          ) : unauthenticated ? (
            <div className="invitation-buttons">
              <Link className="primary-action" to="/" search={{ redirect }}>
                Register
                <ArrowRight aria-hidden="true" />
              </Link>
              <Link className="secondary-action" to="/login" search={{ redirect }}>
                <LogIn aria-hidden="true" />
                Sign in
              </Link>
            </div>
          ) : session.isPending ? (
            <div className="invitation-waiting">
              <LoaderCircle className="spin" aria-hidden="true" />
              Checking Session
            </div>
          ) : (
            <button
              className="primary-action"
              type="button"
              disabled={acceptance.isPending}
              onClick={() => acceptance.mutate()}
            >
              {acceptance.isPending ? "Joining Space" : "Accept invitation"}
              {acceptance.isPending ? (
                <LoaderCircle className="spin" aria-hidden="true" />
              ) : (
                <ArrowRight aria-hidden="true" />
              )}
            </button>
          )}
          {acceptance.error ? (
            <p className="form-error" role="alert">
              {acceptance.error.message}
            </p>
          ) : null}
        </div>
      </section>

      <footer className="onboarding-footer">
        <span>INVITATION / 7 DAYS</span>
        <span className="status-light">TOKEN VALID</span>
      </footer>
    </main>
  );
}
