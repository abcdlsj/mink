// 8×8 pixel "S" drawn on a 16-grid, matching the Neo-brutalist pixel identity.
// The container supplies background and border; this component only draws ink.
export function SumiMark({ className }: { className?: string }) {
  return (
    <svg viewBox="0 0 16 16" className={className} aria-hidden="true" shapeRendering="crispEdges">
      <path d="M4 4h8v2H6v1h5v5H4v-2h5V9H4z" fill="currentColor" />
    </svg>
  );
}
