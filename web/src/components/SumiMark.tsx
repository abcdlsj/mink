// Compact 8×8 pixel "S" for small surfaces (rail badge, favicon). Full-width
// bars with a 1px radius keep it soft without reading as a "5".
// The container supplies background and border; this component only draws ink.
export function SumiMark({ className }: { className?: string }) {
  return (
    <svg viewBox="0 0 16 16" className={className} aria-hidden="true">
      <g fill="currentColor">
        <rect x="4" y="3" width="8" height="2" rx="1" />
        <rect x="4" y="5" width="2" height="2" rx="1" />
        <rect x="4" y="7" width="8" height="2" rx="1" />
        <rect x="10" y="9" width="2" height="2" rx="1" />
        <rect x="4" y="11" width="8" height="2" rx="1" />
      </g>
    </svg>
  );
}
