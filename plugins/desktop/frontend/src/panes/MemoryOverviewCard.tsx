import { useEffect, useState } from "react";
import { api } from "@/lib/api";
import { relTime } from "@/lib/utils";
import type { MemoryDocDetail, MemoryOverviewView } from "@/lib/types";

interface MemoryOverviewCardProps {
  personaID?: string;
  source?: string;
  spaceID?: string;
}

export function MemoryOverviewCard({ personaID, source, spaceID }: MemoryOverviewCardProps) {
  const [overview, setOverview] = useState<MemoryOverviewView | null>(null);
  const [error, setError] = useState("");
  const [copied, setCopied] = useState("");
  const [deleting, setDeleting] = useState("");
  const [editing, setEditing] = useState<MemoryDocDetail | null>(null);
  const [editScope, setEditScope] = useState("");
  const [loadingEdit, setLoadingEdit] = useState("");
  const [saving, setSaving] = useState(false);

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
  const copyDeleteCommand = async (scopeLabel: string, id: string) => {
    const cmd = `!memory delete ${scopeLabel} ${id}`;
    if (navigator.clipboard?.writeText) {
      await navigator.clipboard.writeText(cmd);
    }
    setCopied(id);
    window.setTimeout(() => setCopied((current) => current === id ? "" : current), 1800);
  };
  const deleteMemory = async (scopeLabel: string, id: string) => {
    if (deleting) return;
    setError("");
    setDeleting(id);
    try {
      const res = await api.deleteMemory({ persona_id: personaID, source, space_id: spaceID, scope: scopeLabel, id });
      setOverview(res.overview);
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err));
    } finally {
      setDeleting("");
    }
  };
  const openEditor = async (scopeLabel: string, id: string) => {
    if (loadingEdit || saving) return;
    setError("");
    setLoadingEdit(id);
    try {
      const detail = await api.memoryDoc({ scope: scopeLabel, id });
      setEditing(detail);
      setEditScope(scopeLabel);
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err));
    } finally {
      setLoadingEdit("");
    }
  };
  const updateEditing = (patch: Partial<MemoryDocDetail>) => {
    setEditing((current) => current ? { ...current, ...patch } : current);
  };
  const saveEditing = async () => {
    if (!editing || saving) return;
    setError("");
    setSaving(true);
    try {
      const res = await api.updateMemory({
        persona_id: personaID,
        source,
        space_id: spaceID,
        scope: editScope || editing.scope_label,
        id: editing.id,
        title: editing.title,
        body: editing.body,
        summary: editing.summary || "",
        kind: editing.kind || "",
        confidence: editing.confidence || "",
      });
      setOverview(res.overview);
      setEditing(null);
      setEditScope("");
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err));
    } finally {
      setSaving(false);
    }
  };

  return (
    <div className="border border-border bg-panel px-2.5 py-2">
      <div className="mb-1.5 flex items-center justify-between gap-2">
        <div className="font-mono text-[10.5px] font-extrabold uppercase tracking-[0.45px] text-text-muted">
          Memory list
        </div>
        <div className="text-[10.5px] text-text-faint">recent ids</div>
      </div>
      {scopes.length > 0 ? (
        <div className="flex flex-wrap gap-1">
          {scopes.map((scope) => (
            <span key={scope.label} className="border border-border-soft px-1.5 py-px font-mono text-[10.5px] font-medium text-text-muted">
              {scope.label}
            </span>
          ))}
        </div>
      ) : (
        <div className="text-[12px] text-text-faint">Loading memory scopes...</div>
      )}

      {scopes.some((scope) => scope.recent.length > 0) ? (
        <div className="mt-2 flex flex-col gap-2">
          {scopes.filter((scope) => scope.recent.length > 0).map((scope, idx) => (
            <div key={scope.label} className="border border-border-soft bg-panel-2 px-2 py-1.5">
              <div className="mb-1 flex items-center justify-between gap-2">
                <div className="min-w-0">
                  <div className="truncate font-mono text-[10.5px] font-extrabold uppercase tracking-[0.45px] text-text-muted">
                    {memoryScopeGroupLabel(scope.kind, idx)}
                  </div>
                  <div className="truncate font-mono text-[10px] text-text-faint">{scope.label}</div>
                </div>
                <span className="shrink-0 font-mono text-[10px] text-text-faint">{scope.recent.length} recent</span>
              </div>
              <div className="flex flex-col gap-1.5">
                {scope.recent.map((doc) => (
                  <div key={scope.label + ":" + doc.id} className="border border-border-soft bg-panel px-2 py-1.5">
                    <div className="flex items-center justify-between gap-2">
                      <span className="min-w-0 truncate text-[12px] font-semibold text-text">{doc.title}</span>
                      <span className="shrink-0 font-mono text-[10px] text-text-faint">{relTime(doc.updated_at)}</span>
                    </div>
                    {doc.summary && (
                      <div className="mt-0.5 line-clamp-2 text-[11.5px] leading-[1.35] text-text-muted">
                        {doc.summary}
                      </div>
                    )}
                    <div className="mt-1 flex min-w-0 items-center gap-1.5">
                      <span className="min-w-0 truncate border border-border-soft bg-bg px-1.5 py-px font-mono text-[10px] text-text-muted" title={doc.id}>
                        id {doc.id}
                      </span>
                      <button
                        type="button"
                        onClick={() => void copyDeleteCommand(scope.label, doc.id)}
                        className="shrink-0 border border-border-soft bg-panel px-1.5 py-px font-mono text-[10px] font-semibold text-text-muted hover:border-error-border hover:text-error"
                        title={`Copy !memory delete ${scope.label} ${doc.id}`}
                      >
                        {copied === doc.id ? "Copied" : "Copy delete"}
                      </button>
                      <button
                        type="button"
                        onClick={() => void openEditor(scope.label, doc.id)}
                        disabled={!!loadingEdit || saving}
                        className="shrink-0 border border-border-soft bg-panel px-1.5 py-px font-mono text-[10px] font-semibold text-text-muted hover:border-action-border hover:text-action disabled:cursor-wait disabled:opacity-50"
                        title={`Edit memory ${doc.id}`}
                      >
                        {loadingEdit === doc.id ? "Loading" : "Edit"}
                      </button>
                      <button
                        type="button"
                        onClick={() => void deleteMemory(scope.label, doc.id)}
                        disabled={!!deleting}
                        className="shrink-0 border border-error-border bg-error-bg px-1.5 py-px font-mono text-[10px] font-semibold text-error hover:bg-panel disabled:cursor-not-allowed disabled:opacity-50"
                        title={`Delete memory ${doc.id}`}
                      >
                        {deleting === doc.id ? "Deleting" : "Delete"}
                      </button>
                    </div>
                    {editing?.id === doc.id && (
                      <div className="mt-2 border border-border bg-panel px-2 py-2">
                        <div className="mb-1.5 flex items-center justify-between gap-2">
                          <div className="font-mono text-[10.5px] font-extrabold uppercase tracking-[0.45px] text-text-muted">
                            Edit memory
                          </div>
                          <button
                            type="button"
                            onClick={() => {
                              setEditing(null);
                              setEditScope("");
                            }}
                            className="border border-border-soft px-1.5 py-px font-mono text-[10px] text-text-muted hover:text-text"
                          >
                            Cancel
                          </button>
                        </div>
                        <input
                          value={editing.title}
                          onChange={(ev) => updateEditing({ title: ev.target.value })}
                          className="mb-1.5 w-full border border-border-soft bg-bg px-2 py-1 text-[12px] text-text outline-none focus:border-action"
                          placeholder="Title"
                        />
                        <textarea
                          value={editing.body}
                          onChange={(ev) => updateEditing({ body: ev.target.value })}
                          rows={7}
                          className="mb-1.5 w-full resize-y border border-border-soft bg-bg px-2 py-1 font-mono text-[11.5px] leading-[1.45] text-text outline-none focus:border-action"
                          placeholder="Memory body"
                        />
                        <input
                          value={editing.summary || ""}
                          onChange={(ev) => updateEditing({ summary: ev.target.value })}
                          className="mb-1.5 w-full border border-border-soft bg-bg px-2 py-1 text-[11.5px] text-text-muted outline-none focus:border-action"
                          placeholder="Summary (optional)"
                        />
                        <div className="flex flex-wrap items-center gap-1.5">
                          <input
                            value={editing.kind || ""}
                            onChange={(ev) => updateEditing({ kind: ev.target.value })}
                            className="min-w-0 flex-1 border border-border-soft bg-bg px-2 py-1 font-mono text-[11px] text-text-muted outline-none focus:border-action"
                            placeholder="kind"
                          />
                          <select
                            value={editing.confidence || "medium"}
                            onChange={(ev) => updateEditing({ confidence: ev.target.value })}
                            className="border border-border-soft bg-bg px-2 py-1 font-mono text-[11px] text-text-muted outline-none focus:border-action"
                          >
                            <option value="low">low</option>
                            <option value="medium">medium</option>
                            <option value="high">high</option>
                          </select>
                          <button
                            type="button"
                            onClick={() => void saveEditing()}
                            disabled={saving || !editing.title.trim() || !editing.body.trim()}
                            className="border border-action-border bg-action px-2 py-1 font-mono text-[11px] font-semibold text-panel disabled:cursor-not-allowed disabled:opacity-50"
                          >
                            {saving ? "Saving" : "Save"}
                          </button>
                        </div>
                      </div>
                    )}
                  </div>
                ))}
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
        View with !memory recent &lt;scope&gt;. Edit updates the same memory id.
      </div>
      {error && <div className="mt-2 text-[11px] text-error">{error}</div>}
    </div>
  );
}

function memoryScopeGroupLabel(kind: string, index: number): string {
  if (index === 0 && kind === "channel") return "This chat";
  if (kind === "channel") return "Channel";
  if (kind === "persona") return "Agent";
  if (kind === "workspace") return "Workspace";
  if (kind === "global") return "Global";
  return kind || "Memory";
}
