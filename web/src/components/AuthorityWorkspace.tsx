import { RefreshCw, ShieldCheck } from "lucide-react";
import { useAuthority } from "../hooks/useAuthority";
import { IconButton } from "./IconButton";

export function AuthorityWorkspace() {
  const authority = useAuthority(undefined, true);
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
      <div className="workspace-summary">
        <span className="eyebrow">Organization</span>
        <h2>{authority.data.organization?.name || "Organization"}</h2>
        <p>
          {authority.data.humans.length} Humans · {authority.data.grants.length}{" "}
          grants visible to this Human
        </p>
      </div>
    </section>
  );
}
