// A relaxed 12×12 pixel "S" on a 16-grid. Equal top/bottom bars and a tall
// waist keep it from reading as a "5"; wide bars give it a distinct rhythm.
// The container supplies background and border; this component only draws ink.
export function SumiMark({ className }: { className?: string }) {
  return (
    <svg viewBox="0 0 16 16" className={className} aria-hidden="true" shapeRendering="crispEdges">
      <g fill="currentColor">
        <rect x="2" y="2" width="12" height="2" />
        <rect x="2" y="4" width="2" height="3" />
        <rect x="2" y="7" width="12" height="2" />
        <rect x="12" y="9" width="2" height="3" />
        <rect x="2" y="12" width="12" height="2" />
      </g>
    </svg>
  );
}
