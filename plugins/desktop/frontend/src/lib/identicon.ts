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

export type IdenticonKind = "agent" | "user" | "channel" | "thread";

type Pixel = "k" | "m" | "s" | "h" | "w" | ".";

interface Palette {
  bg: string;
  main: string;
  shade: string;
  hi: string;
}

const INK = "#111111";
const PAPER = "#F7F5EF";

const AGENT_PALETTES: Palette[] = [
  { bg: "#30C7E8", main: "#1EA7CA", shade: "#147A94", hi: "#F8F6EA" },
  { bg: "#FFD33F", main: "#F2B51F", shade: "#A46B13", hi: "#FFF4A8" },
  { bg: "#FF7AA9", main: "#EF5B8C", shade: "#A42D55", hi: "#FFE1EC" },
  { bg: "#A9D58F", main: "#7DBF63", shade: "#427A37", hi: "#E8F7DB" },
  { bg: "#BDA8F2", main: "#8D73D8", shade: "#554399", hi: "#F2ECFF" },
  { bg: "#FFA876", main: "#EF7F49", shade: "#A94728", hi: "#FFE2CF" },
  { bg: "#9EC7F3", main: "#5D91D8", shade: "#2F5792", hi: "#E4F0FF" },
  { bg: "#F28D7E", main: "#D95F50", shade: "#8C302C", hi: "#FFE3DE" },
  { bg: "#D7C06A", main: "#B5952F", shade: "#735A16", hi: "#FFF2A8" },
  { bg: "#86D8C6", main: "#3FB59D", shade: "#237566", hi: "#D9FFF6" },
];

const KIND_PALETTES: Record<Exclude<IdenticonKind, "agent">, Palette> = {
  user: { bg: "#D8DEE7", main: "#8C99AA", shade: "#536170", hi: "#FFFFFF" },
  channel: { bg: "#A9D58F", main: "#70B85A", shade: "#3F7635", hi: "#ECF8DF" },
  thread: { bg: "#BDA8F2", main: "#9175D8", shade: "#59469C", hi: "#F2ECFF" },
};

const TEMPLATES: Pixel[][][] = [
  rows([
    "..........",
    "..kkkkkk..",
    ".kmmmmmmk.",
    ".kmhmmhmk.",
    ".kmmmmmmk.",
    ".kmkmmkmk.",
    ".kmmkkmmk.",
    "..kmmmmk..",
    "..kskkks..",
    "..........",
  ]),
  rows([
    "..........",
    "...kkkk...",
    "..kmmmmk..",
    ".kmmhhmmk.",
    ".kmmkkmmk.",
    ".kmmmmmmk.",
    "..kmssmk..",
    "...kkkk...",
    "..kssssk..",
    "..........",
  ]),
  rows([
    "..........",
    "..kkkkkk..",
    ".kmmmmmmk.",
    ".kmkkkkmk.",
    ".kmkwwkmk.",
    ".kmkkkkmk.",
    ".kmmmmmmk.",
    "..kssssk..",
    "..kkkkkk..",
    "..........",
  ]),
  rows([
    "..........",
    "..kkkkkk..",
    ".kmmmmmmk.",
    ".kmmhmmmk.",
    ".kmmmmhmk.",
    "..kmmmmk..",
    "...kmmk...",
    "..ksmmk...",
    ".ksskk....",
    "..........",
  ]),
  rows([
    "..........",
    "...kkkk...",
    "..kmmmmk..",
    ".kmmmmmmk.",
    ".kmkhhkmk.",
    ".kmmmmmmk.",
    "..kssssk..",
    "...kkkk...",
    "..ks..sk..",
    "..........",
  ]),
  rows([
    "..........",
    "..kkkkkk..",
    ".kmmmmmmk.",
    ".kmmkkmmk.",
    ".kmmmmmmk.",
    ".kksmmskk.",
    "..kssssk..",
    "..kssssk..",
    "...kkkk...",
    "..........",
  ]),
  rows([
    "..........",
    "..kkkkkk..",
    ".kmmmmmmk.",
    ".kmhkkhmk.",
    ".kmmmmmmk.",
    ".kmkkkkmk.",
    ".kmmmmmmk.",
    "..ksmmk...",
    "...kkkk...",
    "..........",
  ]),
  rows([
    "..........",
    "...kkkk...",
    "..kmmmmk..",
    ".kmmmmmmk.",
    ".kmkhhkmk.",
    ".kmmmmmmk.",
    ".kkmssmkk.",
    "...kssk...",
    "..ks..sk..",
    "..........",
  ]),
];

export function identiconSVG(seed: string, kind: IdenticonKind = "agent"): string {
  const stableSeed = `${kind}:${seed || "anon"}`;
  const h = hash32(stableSeed);
  const palette = kind === "agent" ? AGENT_PALETTES[h % AGENT_PALETTES.length] : KIND_PALETTES[kind];
  const template = TEMPLATES[(h >>> 8) % TEMPLATES.length];
  const accentShift = (h >>> 16) & 1;
  const title = escapeAttr(seed || "anon");
  const cells = [`<rect width="40" height="40" fill="${PAPER}"/>`, `<rect x="2" y="2" width="36" height="36" fill="${INK}"/>`, `<rect x="5" y="5" width="30" height="30" fill="${palette.bg}"/>`];

  for (let y = 0; y < template.length; y++) {
    for (let x = 0; x < template[y].length; x++) {
      const pixel = template[y][x];
      if (pixel === ".") continue;
      cells.push(`<rect x="${x * 3 + 5}" y="${y * 3 + 5}" width="3" height="3" fill="${pixelColor(pixel, palette, accentShift)}"/>`);
    }
  }

  return `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 40 40" role="img" aria-label="${title}" shape-rendering="crispEdges">${cells.join("")}</svg>`;
}

function rows(lines: string[]): Pixel[][] {
  return lines.map((line) => line.split("") as Pixel[]);
}

function pixelColor(pixel: Pixel, palette: Palette, accentShift: number): string {
  if (pixel === "k") return INK;
  if (pixel === "m") return palette.main;
  if (pixel === "s") return palette.shade;
  if (pixel === "h") return palette.hi;
  if (pixel === "w") return accentShift ? palette.hi : PAPER;
  return palette.bg;
}
