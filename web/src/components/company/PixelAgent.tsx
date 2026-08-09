import { buildAgentIdenticon, identityPalette } from "../agentIdenticon";

export type AgentPose = "sit" | "stand" | "walk" | "typing";

const LEG_COLOR = "#4B443A";
const SHOE_COLOR = "#241F1A";

/**
 * Pixel sprite built from the same identity seal as the 8x8 avatar: the
 * identicon IS the head, so every Agent keeps one consistent identity.
 */
export function PixelAgent({
  seed,
  pose,
  working = false,
  talking = false,
}: {
  seed: string;
  pose: AgentPose;
  working?: boolean;
  talking?: boolean;
}) {
  const palette = identityPalette(seed);
  const identicon = buildAgentIdenticon(seed);
  const walking = pose === "walk";
  const typing = pose === "typing" || (pose === "sit" && working);
  const sitting = pose === "sit" && !working;

  return (
    <span
      className={`pixel-agent pixel-agent--${pose}${walking ? " pixel-agent--walk" : ""}${typing ? " pixel-agent--typing" : ""}`}
      role="img"
      aria-label="Agent sprite"
    >
      <svg viewBox="0 0 12 14" aria-hidden="true" shapeRendering="crispEdges">
        <rect x="2" y="0" width="8" height="8" fill={palette.background} />
        {identicon.cells
          .filter(([, y]) => y < 8)
          .map(([x, y]) => (
            <rect key={`${x}-${y}`} x={x + 2} y={y} width="1" height="1" fill={identicon.foreground} />
          ))}
        <rect x="2" y="8" width="8" height="1" fill={identicon.foreground} />
        <rect x="4" y="9" width="4" height="1" fill={palette.foreground} />
        <rect x="2" y="10" width="8" height="1" fill={palette.foreground} />
        <g className="pixel-agent__arms">
          <rect x="0" y="10" width="1" height="2" fill={palette.foreground} />
          <rect x="11" y="10" width="1" height="2" fill={palette.foreground} />
        </g>
        {sitting ? (
          <g className="pixel-agent__sit-legs">
            <rect x="2" y="11" width="8" height="1" fill={LEG_COLOR} />
            <rect x="1" y="12" width="10" height="1" fill={SHOE_COLOR} />
          </g>
        ) : (
          <>
            <g className="pixel-agent__legs-a">
              <rect x="2" y="12" width="3" height="1" fill={LEG_COLOR} />
              <rect x="7" y="12" width="3" height="1" fill={LEG_COLOR} />
              <rect x="2" y="13" width="3" height="1" fill={SHOE_COLOR} />
              <rect x="7" y="13" width="3" height="1" fill={SHOE_COLOR} />
            </g>
            <g className="pixel-agent__legs-b">
              <rect x="3" y="12" width="2" height="1" fill={LEG_COLOR} />
              <rect x="7" y="12" width="2" height="1" fill={LEG_COLOR} />
              <rect x="3" y="13" width="2" height="1" fill={SHOE_COLOR} />
              <rect x="7" y="13" width="2" height="1" fill={SHOE_COLOR} />
            </g>
          </>
        )}
      </svg>
      {talking ? (
        <span className="pixel-agent__bubble" aria-hidden="true">
          <i />
          <i />
          <i />
        </span>
      ) : null}
    </span>
  );
}
