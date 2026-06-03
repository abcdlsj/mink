import type { CSSProperties } from "react";
import { useStore } from "@/lib/store";

const isWails = typeof window !== "undefined" && !!(window as any).runtime;
const dragStyle = {
  paddingLeft: isWails ? 104 : 12,
  "--wails-draggable": "drag",
} as CSSProperties & Record<"--wails-draggable", string>;

export function TopBar() {
  const state = useStore((s) => s.state);
  const detail = useStore((s) => s.detail);

  let label = "Ready";
  if (!state?.ready) label = "Offline";
  else if (detail?.item?.running) label = "Running";

  return (
    <header
      className="flex h-10 select-none items-center justify-between border-b-hard border-border bg-panel pr-4"
      style={dragStyle}
    >
      <div className="flex items-center gap-2">
        <img src="/sumi-icon.svg" alt="" className="size-[18px] rounded-sm border border-border bg-panel" />
        <div className="text-[14px] font-display font-black uppercase tracking-[0.5px] text-text">Sumi</div>
      </div>
      <div className="border border-border bg-bg px-2 py-0.5 font-mono text-[11px] uppercase tracking-[0.6px] text-text tabular-nums">
        {label}
      </div>
    </header>
  );
}
