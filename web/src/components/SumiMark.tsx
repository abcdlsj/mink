// Compact 8×8 pixel "S" for small surfaces (rail badge, favicon). Equal
// top/bottom bars and two-row legs keep it from reading as a "5".
// The container supplies background and border; this component only draws ink.
export function SumiMark({ className }: { className?: string }) {
  return (
    <svg viewBox="0 0 16 16" className={className} aria-hidden="true" shapeRendering="crispEdges">
      <g fill="currentColor">
        <rect x="4" y="3" width="8" height="2" />
        <rect x="4" y="5" width="2" height="2" />
        <rect x="4" y="7" width="8" height="2" />
        <rect x="10" y="9" width="2" height="2" />
        <rect x="4" y="11" width="8" height="2" />
      </g>
    </svg>
  );
}
