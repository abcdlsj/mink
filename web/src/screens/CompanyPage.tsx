import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Link, useParams } from "@tanstack/react-router";
import { ArrowUpRight, Building2, FileText, Trash2, Upload } from "lucide-react";
import { useRef, useState } from "react";

import {
  deleteCompanyFile,
  joinChannel,
  listAgents,
  listCompanyFiles,
  uploadCompanyFile,
  type Agent,
  type Channel,
  type CompanyFile,
  type Member,
} from "../api/client";
import { activityLabel } from "../agentActivity";
import { SpaceShell } from "../components/SpaceShell";

export function CompanyPage() {
  const { spaceSlug } = useParams({ from: "/s/$spaceSlug/company" });
  return (
    <SpaceShell spaceSlug={spaceSlug} active="company">
      {({ space, channels, currentMember }) => (
        <CompanyWorkspace
          spaceSlug={space.slug}
          spaceId={space.id}
          channels={channels}
          currentMember={currentMember}
        />
      )}
    </SpaceShell>
  );
}

function CompanyWorkspace({
  spaceSlug,
  spaceId,
  channels,
  currentMember,
}: {
  spaceSlug: string;
  spaceId: string;
  channels: Channel[];
  currentMember: Member;
}) {
  const queryClient = useQueryClient();
  const hq = channels.find((channel) => channel.slug === "hq");
  const files = useQuery({
    queryKey: ["company-files", spaceId],
    queryFn: () => listCompanyFiles(spaceId),
  });
  const agents = useQuery({
    queryKey: ["agents", spaceId],
    queryFn: () => listAgents(spaceId),
    enabled: true,
  });
  const join = useMutation({
    mutationFn: joinChannel,
    onSuccess: () => {
      void queryClient.invalidateQueries({ queryKey: ["channels", spaceId] });
    },
  });
  const upload = useMutation({
    mutationFn: (file: File) => uploadCompanyFile(spaceId, file),
    onSuccess: () => {
      void queryClient.invalidateQueries({ queryKey: ["company-files", spaceId] });
    },
  });
  const remove = useMutation({
    mutationFn: (fileId: string) => deleteCompanyFile(spaceId, fileId),
    onSuccess: () => {
      void queryClient.invalidateQueries({ queryKey: ["company-files", spaceId] });
    },
  });
  const inputRef = useRef<HTMLInputElement>(null);
  const [uploadError, setUploadError] = useState<string | undefined>();
  const canManage = ["owner", "admin"].includes(currentMember.access_level);
  const office = officeCounts(agents.data ?? []);

  function submitUpload(event: React.FormEvent<HTMLFormElement>) {
    event.preventDefault();
    const file = inputRef.current?.files?.[0];
    if (!file) return;
    setUploadError(undefined);
    upload.mutate(file, {
      onError: (error) => setUploadError(error instanceof Error ? error.message : "Upload failed"),
      onSuccess: () => {
        if (inputRef.current) inputRef.current.value = "";
      },
    });
  }

  return (
    <section className="company-workspace" aria-labelledby="company-heading">
      <header className="company-header">
        <div className="page-title">
          <h1 id="company-heading">Company</h1>
          <p>Shared communication, files, and work for every Member of this Space.</p>
        </div>
      </header>

      <div className="company-grid">
        <section className="company-card" aria-labelledby="company-hq-heading">
          <header className="company-card-header">
            <Building2 aria-hidden="true" />
            <div>
              <h2 id="company-hq-heading">HQ Channel</h2>
              <p>#hq is auto-joined by every Member and Agent.</p>
            </div>
          </header>
          {hq ? (
            hq.joined ? (
              <Link
                className="command-button command-button--accent company-hq-link"
                to="/s/$spaceSlug/channels/$channelSlug"
                params={{ spaceSlug, channelSlug: hq.slug }}
              >
                Open #hq <ArrowUpRight aria-hidden="true" />
              </Link>
            ) : (
              <button
                className="command-button"
                type="button"
                disabled={join.isPending}
                onClick={() => join.mutate(hq.id)}
              >
                Join #hq
              </button>
            )
          ) : (
            <p className="company-empty">#hq is not available for this Space.</p>
          )}
        </section>

        <section className="company-card company-drive" aria-labelledby="company-drive-heading">
          <header className="company-card-header">
            <FileText aria-hidden="true" />
            <div>
              <h2 id="company-drive-heading">Company Drive</h2>
              <p>Files appear in every Agent&apos;s workspace/company/ on the paired Computer.</p>
            </div>
          </header>
          <form className="company-upload" onSubmit={submitUpload}>
            <label className="company-upload-field">
              <span>Share a file</span>
              <input ref={inputRef} type="file" name="file" required />
            </label>
            <button className="command-button command-button--accent" type="submit" disabled={upload.isPending}>
              {upload.isPending ? "Uploading…" : "Upload"}
              {upload.isPending ? null : <Upload aria-hidden="true" />}
            </button>
          </form>
          {uploadError ? <p className="form-error" role="alert">{uploadError}</p> : null}
          {files.isPending ? <p className="company-empty">Loading files…</p> : null}
          {files.error ? <p className="company-empty company-empty--error" role="alert">Company Drive is unavailable.</p> : null}
          {!files.isPending && !files.error && (files.data ?? []).length === 0 ? (
            <p className="company-empty">No shared files yet. Upload one and every Agent can read it directly from workspace/company/.</p>
          ) : null}
          <ul className="company-file-list">
            {(files.data ?? []).map((file) => (
              <CompanyFileRow
                key={file.id}
                file={file}
                canDelete={canManage || file.uploader_member_id === currentMember.id}
                pending={remove.isPending && remove.variables === file.id}
                onDelete={() => remove.mutate(file.id)}
              />
            ))}
          </ul>
        </section>

        <section className="company-card" aria-labelledby="company-office-heading">
          <header className="company-card-header">
            <Building2 aria-hidden="true" />
            <div>
              <h2 id="company-office-heading">Office</h2>
              <p>Who is available to claim work right now.</p>
            </div>
          </header>
          {office.length === 0 ? (
            <p className="company-empty">No Agents are paired yet.</p>
          ) : (
            <ul className="company-office">
              {office.map((group) => (
                <li key={group.status}>
                  <span className={`company-office-dot company-office-dot--${group.status}`} aria-hidden="true" />
                  <span>{group.label}</span>
                  <strong>{group.count}</strong>
                </li>
              ))}
            </ul>
          )}
          <div className="company-office-actions">
            <Link className="command-button" to="/s/$spaceSlug/members" params={{ spaceSlug }}>Members <ArrowUpRight aria-hidden="true" /></Link>
            <Link className="command-button" to="/s/$spaceSlug/tasks" params={{ spaceSlug }}>Tasks <ArrowUpRight aria-hidden="true" /></Link>
          </div>
        </section>
      </div>
    </section>
  );
}

function CompanyFileRow({
  file,
  canDelete,
  pending,
  onDelete,
}: {
  file: CompanyFile;
  canDelete: boolean;
  pending: boolean;
  onDelete: () => void;
}) {
  return (
    <li className="company-file-row">
      <div className="company-file-preview">
        {file.media_type.startsWith("image/") ? (
          <img src={file.download_path} alt={file.name} />
        ) : (
          <FileText aria-hidden="true" />
        )}
      </div>
      <div className="company-file-main">
        <a href={file.download_path} download={file.name} title={`Download ${file.name}`}>
          <strong>{file.name}</strong>
        </a>
        <span>{formatFileSize(file.size)} · {file.uploader_name} · {formatFileTime(file.created_at)}</span>
      </div>
      {canDelete ? (
        <button
          className="icon-button company-file-delete"
          type="button"
          aria-label={`Delete ${file.name}`}
          title={`Delete ${file.name}`}
          disabled={pending}
          onClick={onDelete}
        >
          <Trash2 aria-hidden="true" />
        </button>
      ) : null}
    </li>
  );
}

function officeCounts(agents: Agent[]): Array<{ status: string; label: string; count: number }> {
  const counts = new Map<string, number>();
  for (const agent of agents) {
    if (agent.desired_lifecycle !== "active") continue;
    const status = agent.activity_status;
    counts.set(status, (counts.get(status) ?? 0) + 1);
  }
  return [...counts.entries()].map(([status, count]) => ({
    status,
    label: activityLabel(status as Agent["activity_status"]),
    count,
  }));
}

function formatFileSize(size: number): string {
  if (size < 1024) return `${size} B`;
  if (size < 1024 * 1024) return `${(size / 1024).toFixed(1)} KB`;
  return `${(size / (1024 * 1024)).toFixed(1)} MB`;
}

function formatFileTime(value: string): string {
  return new Intl.DateTimeFormat(undefined, { month: "short", day: "numeric", hour: "2-digit", minute: "2-digit" }).format(new Date(value));
}
