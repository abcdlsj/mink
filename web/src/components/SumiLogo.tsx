// 12×12 pixel "S" for larger brand surfaces (wordmarks). Full-width bars
// with a 1px radius keep the letterform soft and distinct from "5".
// The container supplies background and border; this component only draws ink.
export function SumiLogo({ className }: { className?: string }) {
  return (
    <svg viewBox="0 0 16 16" className={className} aria-hidden="true">
      <g fill="currentColor">
        <rect x="2" y="2" width="12" height="2" rx="1" />
        <rect x="2" y="4" width="2" height="3" rx="1" />
        <rect x="2" y="7" width="12" height="2" rx="1" />
        <rect x="12" y="9" width="2" height="3" rx="1" />
        <rect x="2" y="12" width="12" height="2" rx="1" />
      </g>
    </svg>
  );
}
