import { useState, type FormEvent } from "react";
import { RefreshCw, ShieldCheck, UserPlus } from "lucide-react";
import { HumanRole } from "../gen/sumi/organization/v1/organization_pb";
import { useAuthority } from "../hooks/useAuthority";
import { IconButton } from "./IconButton";

export function AuthorityWorkspace() {
  const authority = useAuthority(undefined, true);
  const [name, setName] = useState("");
  const [username, setUsername] = useState("");
  const [role, setRole] = useState(HumanRole.MEMBER);
  const [initialPassword, setInitialPassword] = useState("");
  const [feedback, setFeedback] = useState<
    { kind: "success" | "error"; message: string } | undefined
  >();

  const submit = async (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault();
    setFeedback(undefined);
    try {
      await authority.createHuman({ name, username, role, initialPassword });
      setInitialPassword("");
      setFeedback({ kind: "success", message: "Human registered." });
    } catch (error) {
      setFeedback({
        kind: "error",
        message:
          error instanceof Error ? error.message : "Human registration failed.",
      });
    }
  };
  if (!authority.data) {
    return (
      <section className="timeline" aria-label="Authority">
        <p>
          {authority.status === "error"
            ? authority.error
            : "Loading Authority…"}
        </p>
        <button onClick={() => void authority.refresh()}>Retry</button>
      </section>
    );
  }
  return (
    <section className="timeline workspace-list-view" aria-label="Authority">
      <header className="workspace-list-header">
        <div>
          <ShieldCheck size={18} />
          <strong>Authority</strong>
        </div>
        <IconButton
          label="Refresh Authority"
          tooltipPlacement="left"
          onClick={() => void authority.refresh()}
        >
          <RefreshCw size={16} />
        </IconButton>
      </header>
      <div className="authority-content">
        <div className="workspace-summary">
          <span className="eyebrow">Organization</span>
          <h2>{authority.data.organization?.name || "Organization"}</h2>
          <p>
            {authority.data.humans.length} Humans ·{" "}
            {authority.data.grants.length} grants visible to this Human
          </p>
        </div>
        <form className="authority-registration" onSubmit={submit}>
          <div className="authority-registration-heading">
            <UserPlus size={18} aria-hidden="true" />
            <div>
              <h3>Register Human</h3>
              <p>Create a local account for a new collaborator.</p>
            </div>
          </div>
          <div className="authority-registration-grid">
            <label>
              <span>Display name</span>
              <input
                value={name}
                onChange={(event) => setName(event.target.value)}
                maxLength={100}
                autoComplete="off"
                required
              />
            </label>
            <label>
              <span>Username</span>
              <input
                value={username}
                onChange={(event) => setUsername(event.target.value)}
                minLength={3}
                maxLength={32}
                pattern="[A-Za-z0-9][A-Za-z0-9._-]{2,31}"
                autoComplete="off"
                spellCheck={false}
                required
              />
            </label>
            <label>
              <span>Role</span>
              <select
                value={role}
                onChange={(event) => setRole(Number(event.target.value))}
              >
                <option value={HumanRole.MEMBER}>Member</option>
                <option value={HumanRole.OWNER}>Owner</option>
              </select>
            </label>
            <label>
              <span>Initial password</span>
              <input
                type="password"
                value={initialPassword}
                onChange={(event) => setInitialPassword(event.target.value)}
                minLength={12}
                maxLength={256}
                autoComplete="new-password"
                required
              />
              <small>
                Deliver it through a secure out-of-band channel. Password
                changes are not available yet.
              </small>
            </label>
          </div>
          <div className="authority-registration-actions">
            {feedback && (
              <p className={feedback.kind} role="status">
                {feedback.message}
              </p>
            )}
            <button
              className="primary-action"
              type="submit"
              disabled={authority.action.pending}
            >
              <UserPlus size={15} aria-hidden="true" />
              {authority.action.pending ? "Registering" : "Register Human"}
            </button>
          </div>
        </form>
      </div>
    </section>
  );
}
