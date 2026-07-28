const IDENTICON_PALETTES = [
  { background: "#FFE0EA", foreground: "#6B1839" },
  { background: "#DDF7FD", foreground: "#0E5364" },
  { background: "#FFF0B3", foreground: "#5A4312" },
  { background: "#E4F2CF", foreground: "#315517" },
  { background: "#E3DEFF", foreground: "#40346F" },
  { background: "#FBE0CF", foreground: "#63392D" },
  { background: "#D8ECE9", foreground: "#244D48" },
  { background: "#EEEAE5", foreground: "#4B4540" },
] as const;

export interface AgentIdenticon {
  background: string;
  foreground: string;
  cells: ReadonlyArray<readonly [number, number]>;
  signature: string;
}

export function buildAgentIdenticon(memberId: string): AgentIdenticon {
  const shapeHash = stableHash(memberId, 0x9e3779b9);
  const detailHash = stableHash(memberId, 0x85ebca6b);
  const palette = identityPalette(memberId);
  const cells = new Set<string>();

  for (let row = 0; row < 6; row += 1) {
    const rowHash = mix(shapeHash ^ Math.imul(detailHash, row + 1));
    const width = 1 + (rowHash % 3);
    const offset = (rowHash >>> 8) % (4 - width);
    for (let column = offset; column < offset + width; column += 1) {
      addMirroredCell(cells, column + 1, row + 1);
    }
  }

  // A short central bridge keeps the generated mark visually coherent at 22px.
  const bridgeRow = 2 + (shapeHash % 3);
  addMirroredCell(cells, 3, bridgeRow);
  addMirroredCell(cells, 3, bridgeRow + 1);

  const orderedCells = [...cells]
    .map((cell) => cell.split(",").map(Number) as [number, number])
    .sort(([leftX, leftY], [rightX, rightY]) => leftY - rightY || leftX - rightX);
  const signature = `${palette.foreground}:${orderedCells.map(([x, y]) => `${x}${y}`).join("")}`;
  return { ...palette, cells: orderedCells, signature };
}

export function identityPalette(memberId: string) {
  return IDENTICON_PALETTES[stableHash(memberId, 0xc2b2ae35) % IDENTICON_PALETTES.length];
}

function addMirroredCell(cells: Set<string>, x: number, y: number) {
  cells.add(`${x},${y}`);
  cells.add(`${7 - x},${y}`);
}

function stableHash(value: string, salt: number): number {
  let hash = (0x811c9dc5 ^ salt) >>> 0;
  for (const character of value) {
    hash ^= character.codePointAt(0)!;
    hash = Math.imul(hash, 0x01000193) >>> 0;
  }
  return mix(hash);
}

function mix(value: number): number {
  let mixed = value >>> 0;
  mixed ^= mixed >>> 16;
  mixed = Math.imul(mixed, 0x7feb352d) >>> 0;
  mixed ^= mixed >>> 15;
  mixed = Math.imul(mixed, 0x846ca68b) >>> 0;
  return (mixed ^ (mixed >>> 16)) >>> 0;
}
