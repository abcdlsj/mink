import { FileStack, RefreshCw } from "lucide-react";
import { useArtifacts } from "../hooks/useArtifacts";

export function ArtifactWorkspace() {
  const artifacts = useArtifacts(undefined, undefined, true);
  if (!artifacts.data) return <section className="timeline" aria-label="Artifacts"><p>{artifacts.status === "error" ? artifacts.error : "Loading Artifacts…"}</p><button onClick={() => void artifacts.refresh()}>Retry</button></section>;
  return <section className="timeline" aria-label="Artifacts"><header className="workspace-list-header"><div><FileStack size={18} /><strong>Artifacts</strong></div><button className="icon-button" aria-label="Refresh Artifacts" onClick={() => void artifacts.refresh()}><RefreshCw size={16} /></button></header>{artifacts.data.views.length === 0 ? <p className="empty-state">No readable Artifacts.</p> : <ul className="work-inbox-list">{artifacts.data.views.map((view) => <li key={view.artifact?.id}><strong>{view.artifact?.name || "Artifact"}</strong><span>{view.version?.summary || "No summary"}</span></li>)}</ul>}</section>;
}
