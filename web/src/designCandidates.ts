// Design-lab comparison data. Demo-only; removed when the design is decided.

export interface PaletteCandidate {
  id: string;
  label: string;
  note: string;
  tokens: Record<string, string>;
}

export const PALETTE_CANDIDATES: PaletteCandidate[] = [
  {
    id: "warm-energy",
    label: "B · 能量",
    note: "暖底不变，主色相为橙；深墨 rail，整体更有冲劲。",
    tokens: {
      "--paper": "#FFFBF4",
      "--panel": "#F5EEE1",
      "--ink": "#241F1A",
      "--ink-soft": "#5A5146",
      "--muted": "#8A8071",
      "--surface-strong": "#EFE5D5",
      "--line": "#E8DECD",
      "--line-strong": "#C6B9A6",
      "--accent": "#F0602F",
      "--accent-soft": "#FDE3D5",
      "--indigo": "#3D5AA9",
      "--green": "#5B9253",
      "--orange": "#E08B2C",
      "--amber": "#D48A1D",
      "--red": "#D33F2E",
      "--stone": "#B4AB9D",
      "--rail": "#241F1A",
      "--rail-fg": "#FFFBF4",
    },
  },
];

export interface SpaceAccentSet {
  id: string;
  label: string;
  accents: [string, string, string, string];
}

export const SPACE_ACCENT_SETS: SpaceAccentSet[] = [
  { id: "orange-family", label: "橙主色相", accents: ["#F0602F", "#E8B42D", "#3C9E8F", "#6C5CE7"] },
];

export interface FontCandidate {
  id: string;
  label: string;
  note: string;
  sans: string;
  mono: string;
}

export const FONT_CANDIDATES: FontCandidate[] = [
  {
    id: "neutral-comfort",
    label: "F3 · 中性舒适",
    note: "Manrope：中性舒适；JetBrains Mono 做代码与元数据。",
    sans: '"Manrope Variable", "Manrope", "Noto Sans SC", sans-serif',
    mono: '"JetBrains Mono", ui-monospace, monospace',
  },
];

export const PIXEL_SCALE = [
  { size: 20, unit: 2, use: "行内回复、紧凑列表" },
  { size: 24, unit: 3, use: "消息动作、Action Message" },
  { size: 32, unit: 4, use: "Thread、列表头像" },
  { size: 36, unit: 4, use: "消息、导航、成员行" },
  { size: 48, unit: 6, use: "成员页、空态、登录" },
] as const;
