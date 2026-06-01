import { useStore } from "@/lib/store";

const isWails = typeof window !== "undefined" && !!(window as any).runtime;

export function TopBar() {
  const state = useStore((s) => s.state);
  const detail = useStore((s) => s.detail);

  let label = "Ready";
  if (!state?.ready) label = "Offline";
  else if (detail?.item?.running) label = "Running";

  return (
    <header
      className="flex items-center justify-between border-b border-border bg-bg pr-3.5 h-9 select-none"
      style={{ paddingLeft: isWails ? 78 : 12 }}
    >
      <div className="flex items-center gap-2">
        <img src="/sumi-icon.svg" alt="" className="size-[18px] rounded-[4px]" />
        <div className="text-[14px] font-display font-semibold text-text">Sumi</div>
      </div>
      <div className="text-[12px] text-text-muted tabular-nums">{label}</div>
    </header>
  );
}
