function hash32(s: string): number {
  let h = 2166136261 >>> 0;
  for (let i = 0; i < s.length; i++) {
    h ^= s.charCodeAt(i);
    h = Math.imul(h, 16777619) >>> 0;
  }
  return h >>> 0;
}

function mix32(n: number): number {
  n ^= n >>> 16;
  n = Math.imul(n, 0x7feb352d) >>> 0;
  n ^= n >>> 15;
  n = Math.imul(n, 0x846ca68b) >>> 0;
  n ^= n >>> 16;
  return n >>> 0;
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
  panel: string;
  ink: string;
  accent: string;
  mark: string;
}

const AGENT_PALETTES: Palette[] = [
  { bg: "#EAF3F1", panel: "#B9DCD5", ink: "#0F6B63", accent: "#F0A33A", mark: "#234E52" },
  { bg: "#F4ECD9", panel: "#E7C781", ink: "#8B4E15", accent: "#2F7D88", mark: "#3F3326" },
  { bg: "#EDEBFA", panel: "#C6B8F2", ink: "#5A3EA1", accent: "#D56B45", mark: "#35274D" },
  { bg: "#E9F0FB", panel: "#AFC7EA", ink: "#255C9B", accent: "#D99B29", mark: "#223A59" },
  { bg: "#F8E7E2", panel: "#F0B3A4", ink: "#A74333", accent: "#2E7F67", mark: "#53302B" },
  { bg: "#E8F2DF", panel: "#B8D98F", ink: "#4F7E25", accent: "#B15D8A", mark: "#2F4B24" },
  { bg: "#F1E8D8", panel: "#DDBE8A", ink: "#7A5524", accent: "#3F75B5", mark: "#473721" },
  { bg: "#E6EEF0", panel: "#9ECED5", ink: "#1F7786", accent: "#C46C2A", mark: "#243F45" },
  { bg: "#F1E5EE", panel: "#D9A9CA", ink: "#8A3A70", accent: "#4B8C4E", mark: "#4B2A40" },
  { bg: "#E7ECDF", panel: "#B9C792", ink: "#61731F", accent: "#C05C44", mark: "#374221" },
  { bg: "#F5E8DF", panel: "#E5AD86", ink: "#9D5225", accent: "#4277A9", mark: "#4D3328" },
  { bg: "#E5EDF8", panel: "#A8BFE1", ink: "#4257A4", accent: "#C88E2E", mark: "#2A3153" },
];

const KIND_PALETTES: Record<Exclude<IdenticonKind, "agent">, Palette> = {
  user: { bg: "#ECEFF3", panel: "#C8D0DA", ink: "#52606F", accent: "#7D8792", mark: "#313A45" },
  channel: { bg: "#E9F3EC", panel: "#B9D8C1", ink: "#3F7E52", accent: "#D8A13B", mark: "#284C35" },
  thread: { bg: "#EFE8F5", panel: "#D0B7E0", ink: "#735090", accent: "#D47B44", mark: "#432C56" },
};

export type IdenticonKind = "agent" | "user" | "channel" | "thread";

export function identiconSVG(seed: string, kind: IdenticonKind = "agent"): string {
  const stableSeed = `${kind}:${seed || "anon"}`;
  const h = hash32(stableSeed);
  const palette = kind === "agent" ? AGENT_PALETTES[h % AGENT_PALETTES.length] : KIND_PALETTES[kind];
  const variant = (h >>> 8) % 4;
  const corner = (h >>> 12) % 4;
  const title = escapeAttr(seed || "anon");
  const cells: string[] = [
    `<rect width="32" height="32" fill="${palette.bg}"/>`,
    backgroundPanel(variant, palette),
  ];

  const cell = 4;
  const grid = 8;
  const bits = mix32(h ^ 0xa5a5a5a5);
  for (let y = 0; y < grid; y++) {
    for (let x = 0; x < grid / 2; x++) {
      const bit = (bits >>> ((y * 4 + x) % 32)) & 1;
      const diagonal = (x + y + (h & 3)) % 5 === 0;
      if (!bit && !diagonal) continue;
      const fill = ((x + y + h) & 1) === 0 ? palette.ink : palette.accent;
      const opacity = bit ? "0.94" : "0.62";
      cells.push(`<rect x="${x * cell}" y="${y * cell}" width="${cell}" height="${cell}" fill="${fill}" opacity="${opacity}"/>`);
      const rx = grid - 1 - x;
      if (rx !== x) {
        cells.push(`<rect x="${rx * cell}" y="${y * cell}" width="${cell}" height="${cell}" fill="${fill}" opacity="${opacity}"/>`);
      }
    }
  }

  cells.push(motif(h, palette));
  cells.push(cornerMark(corner, palette));

  return `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 32 32" role="img" aria-label="${title}" shape-rendering="crispEdges">${cells.join("")}</svg>`;
}

function backgroundPanel(variant: number, palette: Palette): string {
  if (variant === 0) return `<path d="M0 0h32L0 32z" fill="${palette.panel}"/>`;
  if (variant === 1) return `<path d="M32 0v32H0z" fill="${palette.panel}"/>`;
  if (variant === 2) return `<rect x="0" y="0" width="16" height="32" fill="${palette.panel}"/>`;
  return `<rect x="0" y="0" width="32" height="16" fill="${palette.panel}"/>`;
}

function motif(h: number, palette: Palette): string {
  const variant = (h >>> 20) % 4;
  if (variant === 0) {
    return `<path d="M8 8h16v4H12v12H8z" fill="${palette.mark}" opacity="0.9"/>`;
  }
  if (variant === 1) {
    return `<path d="M16 5l11 11-11 11L5 16z" fill="none" stroke="${palette.mark}" stroke-width="4" stroke-linejoin="miter"/>`;
  }
  if (variant === 2) {
    return `<path d="M6 10h20v4H6zm0 8h20v4H6z" fill="${palette.mark}" opacity="0.86"/>`;
  }
  return `<path d="M8 8h16v16H8zm4 4v8h8v-8z" fill="${palette.mark}" fill-rule="evenodd" opacity="0.88"/>`;
}

function cornerMark(corner: number, palette: Palette): string {
  const marks = [
    `<path d="M0 0h9v3H3v6H0z" fill="${palette.accent}"/>`,
    `<path d="M32 0v9h-3V3h-6V0z" fill="${palette.accent}"/>`,
    `<path d="M32 32h-9v-3h6v-6h3z" fill="${palette.accent}"/>`,
    `<path d="M0 32v-9h3v6h6v3z" fill="${palette.accent}"/>`,
  ];
  return marks[corner];
}
