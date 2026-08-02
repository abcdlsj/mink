// 12×12 pixel "S" for larger brand surfaces (wordmarks). Rounded ends on
// the top and bottom bars give it a softer letterform distinct from a "5".
// The container supplies background and border; this component only draws ink.
export function SumiLogo({ className }: { className?: string }) {
  return (
    <svg viewBox="0 0 16 16" className={className} aria-hidden="true" shapeRendering="crispEdges">
      <g fill="currentColor">
        <rect x="3" y="2" width="10" height="2" />
        <rect x="2" y="4" width="2" height="3" />
        <rect x="2" y="7" width="12" height="2" />
        <rect x="12" y="9" width="2" height="3" />
        <rect x="3" y="12" width="10" height="2" />
      </g>
    </svg>
  );
}
