function hash32(s: string): number {
  let h = 2166136261 >>> 0;
  for (let i = 0; i < s.length; i++) {
    h ^= s.charCodeAt(i);
    h = Math.imul(h, 16777619) >>> 0;
  }
  return h >>> 0;
}

function escapeAttr(value: string): string {
  return value.replace(/[&<>"']/g, (ch) => {
    if (ch === "&") return "&amp;";
    if (ch === "<") return "&lt;";
    if (ch === ">") return "&gt;";
    if (ch === '"') return "&quot;";
    return "&#39;";
  });
}

interface Palette {
  bg: string;
  wash: string;
  main: string;
}

const AGENT_PALETTES: Palette[] = [
  { bg: "#EAF3F1", wash: "#BFDCD6", main: "#0F6B63" },
  { bg: "#F4ECD9", wash: "#E4C98C", main: "#8B4E15" },
  { bg: "#EDEBFA", wash: "#CABFF0", main: "#5A3EA1" },
  { bg: "#E9F0FB", wash: "#B7CCE9", main: "#255C9B" },
  { bg: "#F8E7E2", wash: "#ECB9AD", main: "#A74333" },
  { bg: "#E8F2DF", wash: "#BED69B", main: "#4F7E25" },
  { bg: "#F1E8D8", wash: "#D9BD8E", main: "#7A5524" },
  { bg: "#E6EEF0", wash: "#A8CDD3", main: "#1F7786" },
  { bg: "#F1E5EE", wash: "#D4B0C9", main: "#8A3A70" },
  { bg: "#E7ECDF", wash: "#BBC89C", main: "#61731F" },
  { bg: "#F5E8DF", wash: "#E2B08B", main: "#9D5225" },
  { bg: "#E5EDF8", wash: "#B1C2E0", main: "#4257A4" },
];

const KIND_PALETTES: Record<Exclude<IdenticonKind, "agent">, Palette> = {
  user: { bg: "#ECEFF3", wash: "#CBD2DB", main: "#52606F" },
  channel: { bg: "#E9F3EC", wash: "#BCD7C2", main: "#3F7E52" },
  thread: { bg: "#EFE8F5", wash: "#D0B7E0", main: "#735090" },
};

export type IdenticonKind = "agent" | "user" | "channel" | "thread";

export function identiconSVG(seed: string, kind: IdenticonKind = "agent"): string {
  const stableSeed = `${kind}:${seed || "anon"}`;
  const h = hash32(stableSeed);
  const palette = kind === "agent" ? AGENT_PALETTES[h % AGENT_PALETTES.length] : KIND_PALETTES[kind];
  const auxiliary = (h >>> 8) % 5;
  const shape = (h >>> 13) % 7;
  const scale = 0.88 + (((h >>> 20) % 4) * 0.06);
  const offsetX = (((h >>> 24) % 3) - 1) * 1.5;
  const offsetY = (((h >>> 27) % 3) - 1) * 1.5;
  const title = escapeAttr(seed || "anon");
  const cells: string[] = [
    `<rect width="32" height="32" fill="${palette.bg}"/>`,
    auxiliaryShape(auxiliary, palette),
    mainShape(shape, palette, scale, offsetX, offsetY),
  ];

  return `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 32 32" role="img" aria-label="${title}">${cells.join("")}</svg>`;
}

function auxiliaryShape(variant: number, palette: Palette): string {
  if (variant === 0) return `<circle cx="5" cy="5" r="18" fill="${palette.wash}" opacity="0.78"/>`;
  if (variant === 1) return `<circle cx="28" cy="4" r="18" fill="${palette.wash}" opacity="0.78"/>`;
  if (variant === 2) return `<path d="M0 0h32v12L0 28z" fill="${palette.wash}" opacity="0.75"/>`;
  if (variant === 3) return `<path d="M0 18 32 4v28H0z" fill="${palette.wash}" opacity="0.72"/>`;
  return `<rect x="-4" y="18" width="40" height="18" rx="9" fill="${palette.wash}" opacity="0.76"/>`;
}

function mainShape(variant: number, palette: Palette, scale: number, offsetX: number, offsetY: number): string {
  const cx = 16 + offsetX;
  const cy = 16 + offsetY;
  const s = (value: number) => Number((value * scale).toFixed(2));
  if (variant === 0) {
    return `<circle cx="${cx}" cy="${cy}" r="${s(8.8)}" fill="${palette.main}"/>`;
  }
  if (variant === 1) {
    const size = s(16);
    return `<rect x="${cx - size / 2}" y="${cy - size / 2}" width="${size}" height="${size}" rx="${s(4.2)}" fill="${palette.main}"/>`;
  }
  if (variant === 2) {
    const r = s(10);
    return `<path d="M${cx} ${cy - r}  L${cx + r} ${cy}  L${cx} ${cy + r}  L${cx - r} ${cy}z" fill="${palette.main}"/>`;
  }
  if (variant === 3) {
    return `<rect x="${cx - s(6)}" y="${cy - s(10)}" width="${s(12)}" height="${s(20)}" rx="${s(6)}" fill="${palette.main}"/>`;
  }
  if (variant === 4) {
    const w = s(18);
    const h = s(12);
    return `<rect x="${cx - w / 2}" y="${cy - h / 2}" width="${w}" height="${h}" rx="${s(6)}" fill="${palette.main}" transform="rotate(-24 ${cx} ${cy})"/>`;
  }
  if (variant === 5) {
    return `<path d="M${cx - s(9)} ${cy + s(8)}a${s(9)} ${s(9)} 0 1 1 ${s(18)} 0z" fill="${palette.main}"/>`;
  }
  return `<path d="M${cx - s(10)} ${cy - s(8)}h${s(20)}v${s(11)}c0 ${s(3)}-${s(3)} ${s(5)}-${s(7)} ${s(5)}h-${s(6)}c-${s(4)} 0-${s(7)}-${s(2)}-${s(7)}-${s(5)}z" fill="${palette.main}"/>`;
}
