// Enlarged 12×12 version of the Sumi mark for wordmarks and larger brand
// surfaces; same proportions as the compact icon.
// The container supplies background and border; this component only draws ink.
export function SumiLogo({ className }: { className?: string }) {
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
