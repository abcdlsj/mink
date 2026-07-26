import { useMutation, useQuery } from "@tanstack/react-query";
import { Link, useNavigate, useParams, useSearch } from "@tanstack/react-router";
import { Asterisk, Fingerprint, LoaderCircle, MonitorCheck } from "lucide-react";
import type { FormEvent } from "react";

import { confirmPairing, getPairingDetails, listSpaces } from "../api/client";

export function PairComputerPage() {
  const { pairingId } = useParams({ from: "/pair-computer/$pairingId" });
  const { code } = useSearch({ from: "/pair-computer/$pairingId" });
  const navigate = useNavigate();
  const details = useQuery({ queryKey: ["computer-pairing", pairingId, code], queryFn: () => getPairingDetails(pairingId, code), retry: false, enabled: Boolean(code) });
  const spaces = useQuery({ queryKey: ["spaces"], queryFn: listSpaces, retry: false });
  const confirmation = useMutation({
    mutationFn: (input: { space_id: string; name: string; code: string }) => confirmPairing(pairingId, input),
    onSuccess: (computer) => {
      const space = spaces.data?.find((candidate) => candidate.id === computer.space_id);
      if (space) void navigate({ to: "/s/$spaceSlug/computers", params: { spaceSlug: space.slug } });
    },
  });

  function submit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    const form = new FormData(event.currentTarget);
    confirmation.mutate({ space_id: String(form.get("space_id") ?? ""), name: String(form.get("name") ?? ""), code });
  }

  if (!code) return <div className="route-status route-status--error">The pairing code is missing from this URL.</div>;
  if (details.isPending || spaces.isPending) return <div className="route-status">Checking Computer...</div>;
  if (details.error || spaces.error || !details.data) return <div className="route-status route-status--error">{details.error?.message ?? spaces.error?.message ?? "Pairing unavailable."}</div>;

  return (
    <main className="onboarding-shell">
      <header className="brand-bar">
        <Link className="wordmark" to="/" search={{ redirect: undefined }} aria-label="Sumi home"><span className="wordmark-mark"><Asterisk strokeWidth={3} /></span>SUMI</Link>
        <span className="step-index">PAIR COMPUTER</span>
      </header>
      <section className="pairing-stage" aria-labelledby="pairing-title">
        <div className="pairing-machine">
          <MonitorCheck aria-hidden="true" />
          <p className="eyebrow">CONFIRM THIS MACHINE</p>
          <h1 id="pairing-title">{details.data.hostname}</h1>
          <dl>
            <div><dt>Operating system</dt><dd>{details.data.os}</dd></div>
            <div><dt>Daemon</dt><dd>v{details.data.daemon_version}</dd></div>
            <div className="fingerprint"><dt><Fingerprint /> Public key fingerprint</dt><dd>{details.data.public_key_fingerprint}</dd></div>
          </dl>
        </div>
        <form className="pairing-form" onSubmit={submit}>
          <label htmlFor="pair-space">Space</label>
          <select id="pair-space" name="space_id" required defaultValue="">
            <option value="" disabled>Select a Space</option>
            {spaces.data?.map((space) => <option key={space.id} value={space.id}>{space.name} /s/{space.slug}</option>)}
          </select>
          <label htmlFor="computer-name">Computer name</label>
          <input id="computer-name" name="name" required maxLength={80} defaultValue={details.data.hostname} />
          <button className="primary-action" type="submit" disabled={confirmation.isPending || !code}>
            {confirmation.isPending ? "Pairing" : "Pair Computer"}
            {confirmation.isPending ? <LoaderCircle className="spin" /> : <MonitorCheck />}
          </button>
          {!code ? <p className="form-error">The pairing code is missing from this URL.</p> : null}
          {confirmation.error ? <p className="form-error" role="alert">{confirmation.error.message}</p> : null}
        </form>
      </section>
    </main>
  );
}
