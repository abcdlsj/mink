import { useStore } from "@/lib/store";

export function TopBar() {
  const state = useStore((s) => s.state);
  const detail = useStore((s) => s.detail);

  let label = "Ready";
  if (!state?.ready) label = "Offline";
  else if (detail?.item?.running) label = "Running";

  return (
    <header className="flex items-center justify-between border-b border-border bg-bg px-3.5 pl-[78px] h-9 select-none">
      <div className="text-[13px] font-semibold tracking-[0.2px] text-text">Sumi</div>
      <div className="text-[12px] text-text-muted tabular-nums">{label}</div>
    </header>
  );
}
