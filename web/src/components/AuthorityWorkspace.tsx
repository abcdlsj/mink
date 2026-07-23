import { ShieldCheck } from "lucide-react";
import { useAuthority } from "../hooks/useAuthority";

export function AuthorityWorkspace() {
  const authority = useAuthority(undefined, true);
  if (!authority.data) return <section className="timeline" aria-label="Authority"><p>{authority.status === "error" ? authority.error : "Loading Authority…"}</p><button onClick={() => void authority.refresh()}>Retry</button></section>;
  return <section className="timeline" aria-label="Authority"><header className="workspace-list-header"><div><ShieldCheck size={18} /><strong>Authority</strong></div></header><p>{authority.data.organization?.name || "Organization"}</p><p>{authority.data.humans.length} Humans · {authority.data.grants.length} grants currently visible to this Human.</p></section>;
}
