import type { CSSProperties } from "react";

export type AgentPose = "sit" | "stand" | "walk" | "typing";

const FRAME_PX = 32;

/**
 * Renders the animated office worker from Arlan_TR's Free office pixel art
 * (https://arlantr.itch.io/free-office-pixel-art). The 416x32 sheet holds 13
 * frames; frame 8 and 11 form the typing animation, frame 0/1 the walk.
 */
export function PixelAgent({
  pose,
  working = false,
  talking = false,
  flip = false,
}: {
  pose: AgentPose;
  working?: boolean;
  talking?: boolean;
  flip?: boolean;
}) {
  const effectivePose: AgentPose = pose === "sit" && working ? "typing" : pose;
  const walking = effectivePose === "walk";
  const typing = effectivePose === "typing";
  const frame = typing ? 8 : 0;
  const style = { "--office-x": `${-frame * FRAME_PX}px` } as CSSProperties;

  return (
    <span
      className={[
        "pixel-agent",
        `pixel-agent--${effectivePose}`,
        walking ? "pixel-agent--walk" : "",
        typing ? "pixel-agent--typing" : "",
        flip ? "pixel-agent--flip" : "",
      ].filter(Boolean).join(" ")}
      style={style}
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
