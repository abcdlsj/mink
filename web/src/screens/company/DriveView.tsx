import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import {
  Cloud,
  File,
  FileArchive,
  FileAudio,
  FileImage,
  FileSpreadsheet,
  FileText,
  FileVideo,
  Trash2,
  Upload,
} from "lucide-react";
import { useRef, useState, type DragEvent, type ReactNode } from "react";

import {
  deleteCompanyFile,
  listCompanyFiles,
  uploadCompanyFile,
  type CompanyFile,
  type Member,
} from "../../api/client";
import { PixelIdentity } from "../../components/PixelIdentity";

export function CompanyDriveView({
  spaceId,
  currentMember,
}: {
  spaceSlug: string;
  spaceId: string;
  currentMember: Member;
}) {
  const queryClient = useQueryClient();
  const files = useQuery({
    queryKey: ["company-files", spaceId],
    queryFn: () => listCompanyFiles(spaceId),
  });
  const inputRef = useRef<HTMLInputElement>(null);
  const [uploadError, setUploadError] = useState<string | undefined>();
  const [dragging, setDragging] = useState(false);
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
  const canManage = ["owner", "admin"].includes(currentMember.access_level);

  async function shareFiles(fileList: FileList | File[]) {
    const pending = [...fileList];
    if (pending.length === 0) return;
    setUploadError(undefined);
    const results = await Promise.allSettled(
      pending.map((file) => upload.mutateAsync(file)),
    );
    const firstFailure = results.find(
      (result): result is PromiseRejectedResult => result.status === "rejected",
    );
    if (firstFailure) {
      const reason = firstFailure.reason;
      setUploadError(reason instanceof Error ? reason.message : "Upload failed");
    }
    if (inputRef.current) inputRef.current.value = "";
  }

  function onDrop(event: DragEvent) {
    event.preventDefault();
    setDragging(false);
    void shareFiles(event.dataTransfer.files);
  }

  return (
    <section className="drive-workspace" aria-labelledby="drive-heading">
      <header className="drive-header">
        <span className="drive-header-glyph" aria-hidden="true"><Cloud /></span>
        <div className="page-title">
          <h1 id="drive-heading" tabIndex={-1}>Company Drive</h1>
          <p>Files appear in every Agent&apos;s workspace/company/ on the paired Computer.</p>
        </div>
        <button
          className="command-button command-button--accent drive-upload-button"
          type="button"
          disabled={upload.isPending}
          onClick={() => inputRef.current?.click()}
        >
          {upload.isPending ? <span className="drive-upload-spinner" aria-hidden="true" /> : <Upload aria-hidden="true" />}
          {upload.isPending ? "Uploading…" : "Upload"}
        </button>
      </header>

      <div
        className="drive-body"
        onDragOver={(event) => {
          event.preventDefault();
          setDragging(true);
        }}
        onDragLeave={(event) => {
          if (event.currentTarget === event.target) setDragging(false);
        }}
        onDrop={onDrop}
      >
        <input
          ref={inputRef}
          className="visually-hidden"
          type="file"
          name="file"
          multiple
          aria-label="Upload files to Company Drive"
          onChange={(event) => {
            if (event.currentTarget.files?.length) void shareFiles(event.currentTarget.files);
          }}
        />
        {dragging ? (
          <div className="drive-drop-overlay" role="status" aria-live="polite">
            <Upload aria-hidden="true" />
            <strong>Drop to share</strong>
            <span>Every Agent can read it from workspace/company/</span>
          </div>
        ) : null}

        <div className="drive-toolbar">
          <span className="drive-breadcrumb"><FolderGlyph />All files</span>
          <span className="drive-count">
            {files.data ? `${files.data.length} file${files.data.length === 1 ? "" : "s"}` : ""}
          </span>
        </div>
        {uploadError ? <p className="form-error drive-error" role="alert">{uploadError}</p> : null}

        {files.isPending ? (
          <div className="drive-state"><p>Loading files…</p></div>
        ) : files.error ? (
          <div className="drive-state drive-state--error" role="alert">
            <p>Company Drive is unavailable.</p>
          </div>
        ) : (files.data ?? []).length === 0 ? (
          <div className="drive-empty">
            <Cloud aria-hidden="true" />
            <h2>Company Drive is empty</h2>
            <p>Upload a file and every Agent can read it directly from workspace/company/.</p>
            <button
              className="command-button command-button--accent"
              type="button"
              onClick={() => inputRef.current?.click()}
            >
              <Upload aria-hidden="true" />Upload a file
            </button>
          </div>
        ) : (
          <div className="drive-table" role="table" aria-label="Company Drive files">
            <div className="drive-table-row drive-table-head" role="row">
              <span role="columnheader">Name</span>
              <span role="columnheader">Size</span>
              <span role="columnheader">Uploaded by</span>
              <span role="columnheader">Modified</span>
              <span role="columnheader" aria-label="Actions" />
            </div>
            {(files.data ?? []).map((file) => (
              <DriveFileRow
                key={file.id}
                file={file}
                canDelete={canManage || file.uploader_member_id === currentMember.id}
                pending={remove.isPending && remove.variables === file.id}
                onDelete={() => remove.mutate(file.id)}
              />
            ))}
          </div>
        )}
      </div>
    </section>
  );
}

function DriveFileRow({
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
    <div className="drive-table-row" role="row">
      <div className="drive-file-name" role="cell">
        <span className="drive-file-icon" aria-hidden="true">
          {file.media_type.startsWith("image/") ? (
            <img src={file.download_path} alt="" />
          ) : (
            <FileTypeIcon mediaType={file.media_type} />
          )}
        </span>
        <span className="drive-file-copy">
          <a href={file.download_path} download={file.name} title={`Download ${file.name}`}>
            {file.name}
          </a>
          <small>{file.media_type || "file"}</small>
        </span>
      </div>
      <span className="drive-file-size" role="cell">{formatFileSize(file.size)}</span>
      <span className="drive-file-uploader" role="cell">
        <PixelIdentity name={file.uploader_name} kind="human" seed={file.uploader_member_id} />
        <span title={file.uploader_name}>{file.uploader_name}</span>
      </span>
      <time className="drive-file-time" role="cell" dateTime={file.created_at}>
        {formatFileTime(file.created_at)}
      </time>
      <span className="drive-file-actions" role="cell">
        {canDelete ? (
          <button
            className="icon-button drive-file-delete"
            type="button"
            aria-label={`Delete ${file.name}`}
            title={`Delete ${file.name}`}
            disabled={pending}
            onClick={onDelete}
          >
            <Trash2 aria-hidden="true" />
          </button>
        ) : null}
      </span>
    </div>
  );
}

function FileTypeIcon({ mediaType }: { mediaType: string }) {
  const icon = fileTypeIcon(mediaType);
  return icon;
}

function fileTypeIcon(mediaType: string): ReactNode {
  if (mediaType.startsWith("image/")) return <FileImage />;
  if (mediaType.startsWith("video/")) return <FileVideo />;
  if (mediaType.startsWith("audio/")) return <FileAudio />;
  if (mediaType.includes("pdf")) return <FileText />;
  if (mediaType.includes("spreadsheet") || mediaType.includes("sheet") || mediaType.includes("csv")) {
    return <FileSpreadsheet />;
  }
  if (
    mediaType.includes("zip") ||
    mediaType.includes("tar") ||
    mediaType.includes("gzip") ||
    mediaType.includes("rar") ||
    mediaType.includes("7z")
  ) {
    return <FileArchive />;
  }
  if (mediaType.startsWith("text/")) return <FileText />;
  return <File />;
}

function FolderGlyph() {
  return (
    <svg className="drive-folder-glyph" viewBox="0 0 16 12" aria-hidden="true" shapeRendering="crispEdges">
      <path d="M0 2h6l2 2h8v8H0z" fill="currentColor" />
    </svg>
  );
}

function formatFileSize(size: number): string {
  if (size < 1024) return `${size} B`;
  if (size < 1024 * 1024) return `${(size / 1024).toFixed(1)} KB`;
  return `${(size / (1024 * 1024)).toFixed(1)} MB`;
}

function formatFileTime(value: string): string {
  return new Intl.DateTimeFormat(undefined, {
    month: "short",
    day: "numeric",
    hour: "2-digit",
    minute: "2-digit",
  }).format(new Date(value));
}
