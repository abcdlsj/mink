import { AlertTriangle, X } from "lucide-react";
import { Button } from "@/components/ui/button";

type ConfirmDialogProps = {
  open: boolean;
  title: string;
  body: string;
  confirmLabel?: string;
  cancelLabel?: string;
  busy?: boolean;
  danger?: boolean;
  error?: string | null;
  onConfirm: () => void;
  onCancel: () => void;
};

export function ConfirmDialog({
  open,
  title,
  body,
  confirmLabel = "Confirm",
  cancelLabel = "Cancel",
  busy = false,
  danger = false,
  error,
  onConfirm,
  onCancel,
}: ConfirmDialogProps) {
  if (!open) return null;

  return (
    <div
      className="fixed inset-0 z-[70] flex items-start justify-center bg-black/35 pt-[18vh]"
      role="presentation"
      onMouseDown={(e) => {
        if (e.target === e.currentTarget && !busy) onCancel();
      }}
      onKeyDown={(e) => {
        if (e.key === "Escape" && !busy) onCancel();
      }}
    >
      <section
        role="dialog"
        aria-modal="true"
        aria-labelledby="confirm-dialog-title"
        className="w-[460px] max-w-[calc(100vw-32px)] overflow-hidden border-hard border-border bg-panel shadow-hard"
      >
        <div className={danger ? "border-b-hard border-border bg-action-bg px-4 pb-2.5 pt-3.5" : "border-b-hard border-border bg-accent px-4 pb-2.5 pt-3.5"}>
          <div className="flex items-start gap-3">
            <span className={danger ? "mt-0.5 inline-flex size-7 items-center justify-center border-2 border-error bg-panel text-error" : "mt-0.5 inline-flex size-7 items-center justify-center border-2 border-border bg-panel text-text"}>
              <AlertTriangle className="size-4" />
            </span>
            <div className="min-w-0 flex-1">
              <h2 id="confirm-dialog-title" className="font-display text-[13px] font-extrabold uppercase text-text">
                {title}
              </h2>
              <p className="mt-1 whitespace-pre-line font-mono text-[11.5px] leading-relaxed text-text-muted">{body}</p>
            </div>
            <button
              type="button"
              onClick={onCancel}
              disabled={busy}
              className="border border-transparent p-1 text-text-muted hover:border-border hover:bg-panel disabled:cursor-not-allowed disabled:opacity-50"
              aria-label="Close dialog"
            >
              <X className="size-4" />
            </button>
          </div>
        </div>
        <div className="px-4 py-3.5">
          {error && (
            <div className="mb-3 border border-error bg-action-bg px-3 py-2 font-mono text-[11.5px] leading-relaxed text-error">
              {error}
            </div>
          )}
          <div className="flex justify-end gap-2">
            <Button type="button" variant="default" onClick={onCancel} disabled={busy}>
              {cancelLabel}
            </Button>
            <Button
              type="button"
              variant={danger ? "danger" : "primary"}
              onClick={onConfirm}
              disabled={busy}
              className={danger ? "border-error bg-error text-bg hover:bg-error hover:text-bg" : ""}
            >
              {busy ? "Working..." : confirmLabel}
            </Button>
          </div>
        </div>
      </section>
    </div>
  );
}
