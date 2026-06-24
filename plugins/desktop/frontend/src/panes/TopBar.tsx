import type { CSSProperties } from "react";
import { useEffect, useRef, useState } from "react";
import { RefreshCw, Settings } from "lucide-react";
import { useStore } from "@/lib/store";
import type { ModelItem } from "@/lib/types";
import { cn } from "@/lib/utils";

const isWails = typeof window !== "undefined" && !!(window as any).runtime;
const dragStyle = {
  paddingLeft: isWails ? 104 : 12,
  "--wails-draggable": "drag",
} as CSSProperties & Record<"--wails-draggable", string>;

export function TopBar({
  detailsEnabled = false,
  onOpenDetails,
}: {
  detailsEnabled?: boolean;
  onOpenDetails?: () => void;
}) {
  const state = useStore((s) => s.state);
  const detail = useStore((s) => s.detail);
  const models = useStore((s) => s.models);
  const syncNow = useStore((s) => s.syncNow);
  const send = useStore((s) => s.send);
  const [refreshing, setRefreshing] = useState(false);
  const [settingsOpen, setSettingsOpen] = useState(false);
  const [modelBusy, setModelBusy] = useState(false);
  const [modelError, setModelError] = useState<string | null>(null);
  const settingsRef = useRef<HTMLDivElement | null>(null);

  let label = "Ready";
  if (!state?.ready) label = "Offline";
  else if (detail?.item?.running) label = "Running";

  useEffect(() => {
    if (!settingsOpen) return;
    const onPointerDown = (ev: PointerEvent) => {
      if (!settingsRef.current?.contains(ev.target as Node)) setSettingsOpen(false);
    };
    window.addEventListener("pointerdown", onPointerDown);
    return () => window.removeEventListener("pointerdown", onPointerDown);
  }, [settingsOpen]);

  const currentModelValue = state?.provider && state?.model
    ? state.provider + "\t" + state.model
    : "";

  const refresh = async () => {
    if (refreshing) return;
    setRefreshing(true);
    try {
      await syncNow();
    } finally {
      setRefreshing(false);
    }
  };

  const changeModel = async (value: string) => {
    const [provider, model] = value.split("\t");
    if (!provider || !model || modelBusy || value === currentModelValue) return;
    setModelBusy(true);
    setModelError(null);
    try {
      await send(`/model ${provider} ${model}`);
      await syncNow();
    } catch (err) {
      setModelError(err instanceof Error ? err.message : "Model switch failed.");
    } finally {
      setModelBusy(false);
    }
  };

  return (
    <header
      className="flex h-10 select-none items-center justify-between border-b-hard border-border bg-panel pr-4"
      style={dragStyle}
    >
      <div className="flex items-center gap-2">
        <img src="/sumi-icon.svg" alt="" className="size-[18px] rounded-sm border border-border bg-panel" />
        <div className="text-[14px] font-display font-extrabold uppercase text-text">Sumi</div>
      </div>
      <div className="flex items-center gap-2">
        <button
          type="button"
          onClick={() => void refresh()}
          disabled={refreshing}
          className="inline-flex size-7 items-center justify-center border border-border bg-panel-2 text-text-muted hover:bg-accent hover:text-text disabled:cursor-wait disabled:text-text-whisper"
          aria-label="Refresh desktop state"
          title="Refresh"
        >
          <RefreshCw className={cn("size-3.5", refreshing && "animate-spin")} />
        </button>
        <div className="relative" ref={settingsRef}>
          <button
            type="button"
            onClick={() => setSettingsOpen((v) => !v)}
            className="inline-flex size-7 items-center justify-center border border-border bg-panel-2 text-text-muted hover:bg-accent hover:text-text"
            aria-label="Open settings"
            title="Settings"
          >
            <Settings className="size-3.5" />
          </button>
          {settingsOpen && (
            <SettingsPanel
              models={models}
              currentModelValue={currentModelValue}
              disabled={!detail || modelBusy}
              busy={modelBusy}
              error={modelError}
              onChange={(value) => void changeModel(value)}
            />
          )}
        </div>
        {onOpenDetails && (
          <button
            type="button"
            disabled={!detailsEnabled}
            onClick={onOpenDetails}
            className="border border-border bg-panel-2 px-2 py-0.5 font-mono text-[11px] uppercase text-text-muted hover:bg-accent hover:text-text disabled:cursor-not-allowed disabled:text-text-whisper disabled:hover:bg-panel-2"
          >
            Details
          </button>
        )}
        <div className="border border-border bg-bg px-2 py-0.5 font-mono text-[11px] uppercase text-text tabular-nums">
          {label}
        </div>
      </div>
    </header>
  );
}

function SettingsPanel({
  models,
  currentModelValue,
  disabled,
  busy,
  error,
  onChange,
}: {
  models: ModelItem[];
  currentModelValue: string;
  disabled: boolean;
  busy: boolean;
  error: string | null;
  onChange: (value: string) => void;
}) {
  const readyModels = models.filter((m) => m.ready !== false);
  return (
    <div className="absolute right-0 top-8 z-50 w-[280px] border border-border bg-panel shadow-card">
      <div className="border-b border-border-soft px-3 py-2">
        <div className="font-display text-[12px] font-extrabold uppercase text-text">Settings</div>
        <div className="mt-0.5 font-mono text-[10.5px] text-text-faint">Applies to the current conversation.</div>
      </div>
      <div className="grid gap-2 px-3 py-3">
        <label className="grid gap-1.5">
          <span className="font-mono text-[10.5px] font-semibold uppercase text-text-muted">Model</span>
          <select
            value={currentModelValue}
            disabled={disabled || readyModels.length === 0}
            onChange={(ev) => onChange(ev.target.value)}
            className="w-full border border-border bg-bg px-2 py-1.5 text-[12.5px] text-text outline-none focus:border-action disabled:cursor-not-allowed disabled:text-text-whisper"
          >
            {!currentModelValue && <option value="">Choose a model</option>}
            {readyModels.map((m) => (
              <option key={m.provider + "\t" + m.model} value={m.provider + "\t" + m.model}>
                {modelLabel(m)}
              </option>
            ))}
          </select>
        </label>
        <div className="font-mono text-[10.5px] leading-4 text-text-faint">
          {disabled ? "Open a conversation before switching models." : busy ? "Switching model..." : "Uses the existing /model command."}
        </div>
        {error && (
          <div className="border border-error-border bg-error-bg px-2 py-1 font-mono text-[10.5px] text-error">
            {error}
          </div>
        )}
      </div>
    </div>
  );
}

function modelLabel(model: ModelItem): string {
  const name = model.name || model.model;
  if (!model.provider) return name;
  return `${name} · ${model.provider}`;
}
