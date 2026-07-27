import { type MouseEvent, type ReactNode, useEffect, useRef } from "react";

const FOCUSABLE_SELECTOR = [
  "a[href]",
  "button:not([disabled])",
  "input:not([disabled])",
  "select:not([disabled])",
  "textarea:not([disabled])",
  "[tabindex]:not([tabindex='-1'])",
].join(",");

export function DialogFrame({
  children,
  className,
  close,
  labelId,
}: {
  children: ReactNode;
  className?: string;
  close: () => void;
  labelId: string;
}) {
  const dialog = useRef<HTMLElement>(null);
  const closeRef = useRef(close);

  useEffect(() => {
    closeRef.current = close;
  }, [close]);

  useEffect(() => {
    const previousFocus = document.activeElement instanceof HTMLElement ? document.activeElement : null;
    const previousOverflow = document.body.style.overflow;
    document.body.style.overflow = "hidden";

    function focusInitialControl() {
      const initialFocus =
        dialog.current?.querySelector<HTMLElement>("[data-dialog-initial-focus]") ??
        dialog.current?.querySelector<HTMLElement>(FOCUSABLE_SELECTOR) ??
        dialog.current;
      initialFocus?.focus();
    }
    focusInitialControl();

    function keepFocusInside(event: FocusEvent) {
      if (dialog.current && !dialog.current.contains(event.target as Node)) focusInitialControl();
    }

    function handleKey(event: KeyboardEvent) {
      if (event.key === "Escape") {
        event.preventDefault();
        closeRef.current();
        return;
      }
      if (event.key !== "Tab" || !dialog.current) return;

      const focusable = [...dialog.current.querySelectorAll<HTMLElement>(FOCUSABLE_SELECTOR)];
      if (!focusable.length) {
        event.preventDefault();
        dialog.current.focus();
        return;
      }
      const first = focusable[0];
      const last = focusable[focusable.length - 1];
      if (event.shiftKey && (document.activeElement === first || !dialog.current.contains(document.activeElement))) {
        event.preventDefault();
        last.focus();
      } else if (!event.shiftKey && (document.activeElement === last || !dialog.current.contains(document.activeElement))) {
        event.preventDefault();
        first.focus();
      }
    }

    document.addEventListener("focusin", keepFocusInside);
    document.addEventListener("keydown", handleKey);
    return () => {
      document.removeEventListener("focusin", keepFocusInside);
      document.removeEventListener("keydown", handleKey);
      document.body.style.overflow = previousOverflow;
      if (previousFocus?.isConnected) previousFocus.focus();
    };
  }, []);

  function closeFromBackdrop(event: MouseEvent<HTMLDivElement>) {
    if (event.target === event.currentTarget) closeRef.current();
  }

  return (
    <div className="dialog-backdrop" role="presentation" onClick={closeFromBackdrop}>
      <section
        ref={dialog}
        className={`dialog-surface${className ? ` ${className}` : ""}`}
        role="dialog"
        aria-modal="true"
        aria-labelledby={labelId}
        tabIndex={-1}
      >
        {children}
      </section>
    </div>
  );
}
