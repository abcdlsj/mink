import { useEffect, useState } from "react";
import { api } from "@/lib/api";
import { relTime } from "@/lib/utils";
import type { MemoryOverviewView } from "@/lib/types";

interface MemoryOverviewCardProps {
  personaID?: string;
  source?: string;
  spaceID?: string;
}

export function MemoryOverviewCard({ personaID, source, spaceID }: MemoryOverviewCardProps) {
  const [overview, setOverview] = useState<MemoryOverviewView | null>(null);
  const [error, setError] = useState("");

  useEffect(() => {
    let alive = true;
    setError("");
    api.memoryOverview({ persona_id: personaID, source, space_id: spaceID })
      .then((next) => {
        if (alive) setOverview(next);
      })
      .catch((err) => {
        if (alive) setError(err instanceof Error ? err.message : String(err));
      });
    return () => {
      alive = false;
    };
  }, [personaID, source, spaceID]);

  const scopes = overview?.scopes || [];
  const recent = scopes.flatMap((scope) => scope.recent.map((doc) => ({ scope, doc })));

  return (
    <div className="border border-border bg-panel px-2.5 py-2">
      {scopes.length > 0 ? (
        <div className="flex flex-wrap gap-1">
          {scopes.map((scope) => (
            <span key={scope.label} className="border border-border-soft px-1.5 py-px font-mono text-[10.5px] text-text-faint">
              {scope.label}
            </span>
          ))}
        </div>
      ) : (
        <div className="text-[12px] text-text-faint">Loading memory scopes...</div>
      )}

      {recent.length > 0 ? (
        <div className="mt-2 flex flex-col gap-1.5">
          {recent.slice(0, 5).map(({ scope, doc }) => (
            <div key={scope.label + ":" + doc.id} className="border border-border-soft bg-panel-2 px-2 py-1.5">
              <div className="flex items-center justify-between gap-2">
                <span className="min-w-0 truncate text-[12px] font-semibold text-text">{doc.title}</span>
                <span className="shrink-0 font-mono text-[10px] text-text-faint">{relTime(doc.updated_at)}</span>
              </div>
              {doc.summary && (
                <div className="mt-0.5 line-clamp-2 text-[11.5px] leading-[1.35] text-text-muted">
                  {doc.summary}
                </div>
              )}
              <div className="mt-1 font-mono text-[10.5px] text-text-faint">
                {scope.label} · id {doc.id}
              </div>
            </div>
          ))}
        </div>
      ) : overview ? (
        <div className="mt-2 border border-dashed border-border-soft bg-panel-2 px-2 py-1.5 text-[11.5px] text-text-faint">
          No remembered items in these scopes.
        </div>
      ) : null}

      <div className="mt-2 text-[11px] leading-[1.35] text-text-faint">
        View with !memory recent &lt;scope&gt;. Delete with !memory delete &lt;scope&gt; &lt;id&gt;.
      </div>
      {error && <div className="mt-2 text-[11px] text-error">{error}</div>}
    </div>
  );
}
