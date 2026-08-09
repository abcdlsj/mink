export type AgentPose = "sit" | "stand" | "walk" | "typing";

/**
 * Renders Julia from Arlan_TR's Free office pixel art
 * (https://arlantr.itch.io/free-office-pixel-art). Pose classes switch between
 * the idle, walk and PC typing sprite sheets; direction is handled by flip.
 */
export function PixelAgent({
  pose,
  working = false,
  talking = false,
  flip = false,
  variant = 0,
}: {
  pose: AgentPose;
  working?: boolean;
  talking?: boolean;
  flip?: boolean;
  variant?: number;
}) {
  const effectivePose: AgentPose = pose === "sit" && working ? "typing" : pose;
  const walking = effectivePose === "walk";
  const typing = effectivePose === "typing";

  return (
    <span
      className={[
        "pixel-agent",
        `pixel-agent--${effectivePose}`,
        walking ? "pixel-agent--walk" : "",
        typing ? "pixel-agent--typing" : "",
        `pixel-agent--variant-${variant % 4}`,
        flip ? "pixel-agent--flip" : "",
      ].filter(Boolean).join(" ")}
      role="img"
      aria-label="Office worker sprite"
    >
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
