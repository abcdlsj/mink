type PixelAvatarKind = "agent" | "human";
type PixelAvatarSize = "xs" | "sm" | "md" | "lg";

export function PixelAvatar({
  seed,
  kind,
  size = "md",
  className,
}: {
  seed: string;
  kind: PixelAvatarKind;
  size?: PixelAvatarSize;
  className?: string;
}) {
  const variant = avatarVariant(seed);
  const classes = ["pixel-avatar", `pixel-avatar-${size}`, className]
    .filter(Boolean)
    .join(" ");

  return (
    <span
      className={classes}
      data-kind={kind}
      data-palette={variant % 4}
      data-variant={variant}
      aria-hidden="true"
    >
      <svg viewBox="0 0 16 16" shapeRendering="crispEdges">
        <rect className="pixel-avatar-background" width="16" height="16" />
        {kind === "agent" ? (
          <AgentSprite variant={variant} />
        ) : (
          <HumanSprite variant={variant} />
        )}
      </svg>
    </span>
  );
}

function AgentSprite({ variant }: { variant: number }) {
  const roundEyes = variant % 2 === 0;
  const sideAntenna = variant % 3 === 0;
  return (
    <>
      <rect className="pixel-avatar-outline" x="7" y="1" width="2" height="3" />
      <rect className="pixel-avatar-accent" x="7" y="1" width="2" height="1" />
      {sideAntenna && (
        <rect
          className="pixel-avatar-outline"
          x="11"
          y="2"
          width="2"
          height="2"
        />
      )}
      <rect
        className="pixel-avatar-outline"
        x="2"
        y="5"
        width="12"
        height="7"
      />
      <rect
        className="pixel-avatar-outline"
        x="4"
        y="3"
        width="8"
        height="10"
      />
      <rect className="pixel-avatar-face" x="5" y="5" width="6" height="6" />
      <rect className="pixel-avatar-accent" x="5" y="5" width="6" height="2" />
      <rect
        className="pixel-avatar-eye"
        x="5"
        y="7"
        width={roundEyes ? 2 : 1}
        height="2"
      />
      <rect
        className="pixel-avatar-eye"
        x={roundEyes ? 9 : 10}
        y="7"
        width={roundEyes ? 2 : 1}
        height="2"
      />
      <rect
        className="pixel-avatar-outline"
        x="7"
        y="10"
        width="2"
        height="1"
      />
      <rect
        className="pixel-avatar-outline"
        x="3"
        y="13"
        width="10"
        height="3"
      />
      <rect className="pixel-avatar-accent" x="5" y="13" width="6" height="3" />
    </>
  );
}

function HumanSprite({ variant }: { variant: number }) {
  const longHair = variant % 2 === 1;
  const fringeRight = variant % 3 === 0;
  return (
    <>
      {longHair && (
        <rect className="pixel-avatar-hair" x="3" y="4" width="10" height="9" />
      )}
      <rect
        className="pixel-avatar-outline"
        x="4"
        y="3"
        width="8"
        height="10"
      />
      <rect className="pixel-avatar-skin" x="4" y="5" width="8" height="7" />
      <rect className="pixel-avatar-skin" x="3" y="7" width="1" height="3" />
      <rect className="pixel-avatar-skin" x="12" y="7" width="1" height="3" />
      <rect className="pixel-avatar-hair" x="4" y="3" width="8" height="3" />
      <rect
        className="pixel-avatar-hair"
        x={fringeRight ? 9 : 4}
        y="5"
        width="3"
        height="2"
      />
      <rect className="pixel-avatar-eye" x="5" y="8" width="1" height="1" />
      <rect className="pixel-avatar-eye" x="10" y="8" width="1" height="1" />
      <rect className="pixel-avatar-mouth" x="7" y="10" width="2" height="1" />
      <rect
        className="pixel-avatar-outline"
        x="3"
        y="13"
        width="10"
        height="3"
      />
      <rect className="pixel-avatar-accent" x="4" y="13" width="8" height="3" />
    </>
  );
}

function avatarVariant(seed: string) {
  let hash = 2166136261;
  for (const character of seed) {
    hash ^= character.codePointAt(0) ?? 0;
    hash = Math.imul(hash, 16777619);
  }
  return (hash >>> 0) % 12;
}
