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
  // Robots / screens
  rows(["..kkkkkk..", ".kmmmmmmk.", "kmmhmmhmmk", "kmmmmmmmmk", "kmmkkkkmmk", "kmmmmmmmmk", ".kmmssmmk.", "..kkkkkk..", "...k..k...", "...k..k..."]),
  rows(["...kkk....", "...kmk....", "..kkkkkk..", ".kmmmmmmk.", "kmmhmmhmmk", "kmmmmmmmmk", "kmmkmmkmmk", ".kmmssmmk.", "..kkkkkk..", "...k..k..."]),
  rows([".kkkkkkkk.", "kwwwwwwwwk", "kwkkkkkkwk", "kwkmmmmkwk", "kwkmmmmkwk", "kwksssskwk", "kwkkkkkkwk", "kwwwwwwwwk", ".kkkkkkkk.", "....kk...."]),
  rows(["kkkkkk....", "kmmmmk....", "kmhhmk....", "kmmmmkkkk.", "kmmmmmmmmk", "kmmkmmkmmk", "kmmmmmmmmk", ".kmmssmmk.", "..kkkkkk..", "...k..k..."]),
  rows(["...kkkkkk.", "..kmmmmmmk", ".kmmhmmhmk", "kmmmmmmmmk", "kmmkkkkmmk", "kmmmmmmmmk", ".kmmssmmk.", "..kkkkkk..", "...kk..kk.", "...k....k."]),
  rows([".kkkkkkkk.", "kwwwwwwwwk", "kwmmmmmmwk", "kwmkkkkmwk", "kwmkwwkmwk", "kwmkkkkmwk", "kwmmmmmmwk", "kwwsssswwk", ".kkkkkkkk.", "kk......kk"]),

  // Creature faces
  rows(["k..kkkk..k", "kkkmmmmkkk", "kmmmmmmmmk", "kmmhmmhmmk", "kmmmmmmmmk", "kmmkkkkmmk", "kmmmmmmmmk", ".kmmssmmk.", "..kkkkkk..", "...k..k..."]),
  rows(["kk......kk", "kmm....mmk", "kmmkkkkmmk", "kmmmmmmmmk", "kmmhmmhmmk", "kmmmmmmmmk", "kmmkkkkmmk", ".kmmssmmk.", "..kkkkkk..", "...k..k..."]),
  rows(["...kkkk...", "..kmmmmk..", ".kmmmmmmk.", "kmmhmmhmmk", "kmmmmmmmmk", "kmmkkkkmmk", "kmmmmmmmmk", "kk.k..k.kk", "k..k..k..k", "k........k"]),
  rows(["kkk....kkk", "kmmk..kmmk", ".kmmmmmmk.", "kmmhmmhmmk", "kmmmmmmmmk", "kmmkkkkmmk", ".kmmmmmmk.", "..kssssk..", "...kkkk...", "....kk...."]),
  rows(["..........", "..kkkkkk..", ".kmmmmmmk.", "kmmhmmhmmk", "kmmmmmmmmk", "kmmssssmmk", "kssssssssk", "kssssssssk", ".kssksssk.", "..kk..kk.."]),
  rows(["..kk..kk..", "..kmmmmk..", ".kmmmmmmk.", "kmmhmmhmmk", "kmmmmmmmmk", "kmmkkkkmmk", ".kmmmmmmk.", "..kssssk..", "...kkkk...", "....kk...."]),
  rows(["..kkkkkk..", ".kmmmmmmk.", "kmmmmmmmmk", "kmmhmmhmmk", "kmmmmmmmmk", "kmmkkkkmmk", ".kmmmmmmk.", "k..kss..k.", "k...kk...k", "k........k"]),
  rows(["....kk....", "..kkkkkk..", ".kmmmmmmk.", "kmmhmmhmmk", "kmmmmmmmmk", "kmmkkkkmmk", ".kmmmmmmk.", "..kssssk..", ".kk....kk.", "kk......kk"]),

  // Objects
  rows(["....kk....", "...kmmk...", "..kmmmmk..", ".kmmhhmmk.", "kmmmmmmmmk", "kmmmmmmmmk", ".kmmssmmk.", "..kssssk..", "...kkkk...", "....kk...."]),
  rows(["..kkkkkk..", ".kmmmmmmk.", "kmmmmmmmmk", "kmmhmmhmmk", "kmmmmmmmmk", "kmmmmmmmmk", "kssssssssk", ".kksssskk.", "...kkkk...", "..kk..kk.."]),
  rows(["......kkk.", "...kkkkmmk", "..kmmmmmmk", ".kmmmmmmmk", "kmmhmmmmmk", "kmmmmmmmkk", ".kmmmmmkk.", "..kkkkk...", "..........", ".........."]),
  rows(["..kkkk....", ".kmmmmkk..", "kmmmmmmmk.", "kmmhmmmmmk", "kmmmmmmmmk", "kmmmmmssmk", ".kmmssssk.", "..kkkkkk..", ".....kk...", ".........."]),
  rows(["...kk.....", "..kmmkk...", ".kmmmmk...", "kmmhhmmk..", "kmmmmmmk..", ".kmmmssk..", "..kssmk...", "...kk.....", "..........", ".........."]),
  rows(["...kkkk...", "..kmmmmk..", ".kmmmmmmk.", "kmmhhhhmmk", "kmmmmmmmmk", "kmmmmmmmmk", ".kmmmmmmk.", "..kssssk..", "...kkkk...", "..kk..kk.."]),
  rows(["....kk....", "...kmmk...", "..kmmmmk..", ".kmmhhmmk.", "kmmmmmmmmk", "kmmmmmmmmk", ".kmmssmmk.", "..kssssk..", "...kkkk...", "....kk...."]),
  rows(["..kkkkkk..", ".kmmmmmmk.", "kmmmmmmmmk", "kmmhhhhmmk", "kmmmmmmmmk", ".kmmmmmmk.", "..kssssk..", "...kssk...", "....kk....", "....kk...."]),

  // Tools / roles
  rows(["kkkkkkkkk.", "kwwwwwwwk.", "kwmmmmmwk.", "kwmsssmwk.", "kwmmmmmwk.", "kwwwwwwwk.", "kkkkkkkkk.", "k........k", "kk......kk", ".........."]),
  rows(["..kkkk....", ".kmmmmk...", "kmmkkmmk..", "kmkkkkmk..", "kmmkkmmk..", ".kmmmmk...", "..kkkkkk..", "......kmmk", ".......kkk", ".........."]),
  rows(["......kkk.", ".....kmmk.", "....kmmk..", "kk.kmmk...", "kmmkmmk...", ".kmmmk....", "..kmmk....", "...kmmk...", "....kk....", ".........."]),
  rows(["....kkkk..", "...kmmmmk.", "..kmmmmkk.", ".kmmmmk...", "kmmmmk....", ".kmmmmk...", "..kmmmmk..", "...ksssk..", "....kkk...", ".........."]),
  rows(["....kk....", "...kmmk...", "..kmmmmk..", ".kmmhhmmk.", "kmmmmmmmmk", "kkmmmmmmkk", "..kmmmmk..", "...kssk...", "....kk....", "....kk...."]),
  rows(["..kkkkkk..", ".kmmmmmmk.", "kmmhhhhmmk", "kmmmmmmmmk", "kmmssssmmk", "kmmmmmmmmk", "kssssssssk", ".kkkkkkkk.", "..k....k..", "kk......kk"]),
  rows(["kkkkkk....", "kmmmmkk...", "kmhhhhmk..", "kmmmmmmk..", "kmssssmk..", "kmmmmmmk..", "kmmmmkk...", "kkkkkk....", "....kkk...", ".....kkk.."]),
  rows(["..kkkk....", ".kmmmmk...", "kmmhmmmk..", "kmmmmmmk..", ".kmmssmk..", "..kkkkkk..", ".....kmmk.", "......kmmk", ".......kkk", ".........."]),

  // Abstract badges
  rows(["...kkkk...", "..kmmmmk..", ".kmmhhmmk.", "kmmmmmmmmk", "kmmmkkmmmk", "kmmmmmmmmk", ".kmmssmmk.", "..kkkkkk..", ".kksssskk.", "kk......kk"]),
  rows([".....kk...", "....kmmk..", "...kmmk...", "..kmmmk...", "..kkmmk...", "...kmmmk..", "...kssk...", "..kssk....", "..kk......", ".........."]),
  rows(["....kk....", "...kmmk...", "..kmmmmk..", ".kmmmmmmk.", "kmmmmmmmmk", "kkmmmmmmkk", "..kssssk..", "..kkkkkk..", "....kk....", ".........."]),
  rows(["kkk.......", "kmmkkkk...", "kmmmmmmk..", "kmmhhmmmk.", "kmmmmmmmk.", "kmmssmmk..", "kmmmmkk...", "kkkkk.....", "kk........", ".........."]),
  rows(["kkkkkkkk..", "kmkmkmk...", "kkmkmk....", "kmkmkmk...", "kkmkmkmk..", "kmkmkmkmk.", "kkmkmkmkmk", ".kkkkkkkk.", "..........", ".........."]),
  rows(["kkk.......", "kmmkk.....", "kmmmmkk...", "kmmmmmmk..", "kmmhmmhmk.", "kmmmmmmk..", ".kmmssmk..", "..kkkkk...", "...kk.....", ".........."]),
  rows(["...kkkkkk.", "..kmmmmmmk", ".kmmkkkkmk", "kmmkwwkmmk", "kmmkkkkmmk", "kmmmmmmmmk", ".kmmssmmk.", "..kkkkkk..", "..........", ".........."]),
  rows(["..kkkkkk..", ".kmmmmmmk.", "kmmhhhhmmk", "kmmmmmmmmk", ".kmmmmmmk.", "..kssssk..", "...kssk...", "....kk....", "....kk....", "....kk...."]),
];

export function identiconSVG(seed: string, kind: IdenticonKind = "agent"): string {
  const stableSeed = `${kind}:${seed || "anon"}`;
  const h = hash32(stableSeed);
  const palette = kind === "agent" ? AGENT_PALETTES[h % AGENT_PALETTES.length] : KIND_PALETTES[kind];
  const template = TEMPLATES[(h >>> 8) % TEMPLATES.length];
  const accentShift = (h >>> 16) & 1;
  const title = escapeAttr(seed || "anon");
  const cellSize = 4;
  const cells = [`<rect width="40" height="40" fill="${PAPER}"/>`];

  for (let y = 0; y < template.length; y++) {
    for (let x = 0; x < template[y].length; x++) {
      const pixel = template[y][x];
      if (pixel === ".") continue;
      cells.push(`<rect x="${x * cellSize}" y="${y * cellSize}" width="${cellSize}" height="${cellSize}" fill="${pixelColor(pixel, palette, accentShift)}"/>`);
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
