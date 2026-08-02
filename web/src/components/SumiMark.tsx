// Compact 8×8 pixel "S" for small surfaces (rail badge, favicon). Equal
// top/bottom bars with thin one-row legs keep the original letterform.
// The container supplies background and border; this component only draws ink.
export function SumiMark({ className }: { className?: string }) {
  return (
    <svg viewBox="0 0 16 16" className={className} aria-hidden="true" shapeRendering="crispEdges">
      <g fill="currentColor">
        <rect x="4" y="4" width="8" height="2" />
        <rect x="4" y="6" width="2" height="1" />
        <rect x="4" y="7" width="8" height="2" />
        <rect x="10" y="9" width="2" height="1" />
        <rect x="4" y="10" width="8" height="2" />
      </g>
    </svg>
  );
}
