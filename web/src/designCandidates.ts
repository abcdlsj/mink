// Design-lab comparison data. Demo-only; removed when the design is decided.

export interface PaletteCandidate {
  id: string;
  label: string;
  note: string;
  tokens: Record<string, string>;
}

export const PALETTE_CANDIDATES: PaletteCandidate[] = [
  {
    id: "warm-candy",
    label: "A · 暖糖",
    note: "延续现有暖甜气质，色值重新调和；主色相为珊瑚粉。",
    tokens: {
      "--paper": "#FFFDF8",
      "--panel": "#FFF2E4",
      "--ink": "#26221C",
      "--ink-soft": "#5C554A",
      "--muted": "#8A8172",
      "--surface-strong": "#F6EAD8",
      "--line": "#EADFCB",
      "--line-strong": "#CBBFA8",
      "--accent": "#E85D75",
      "--accent-soft": "#FDE3E8",
      "--indigo": "#4E63B8",
      "--green": "#5F9852",
      "--orange": "#D98B32",
      "--amber": "#C0821F",
      "--red": "#D24A40",
      "--stone": "#B6AC9E",
      "--rail": "#FFC83D",
      "--rail-fg": "#26221C",
    },
  },
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
  {
    id: "cool-signal",
    label: "C · 信号",
    note: "冷中性底 + 蓝主色相，暖色只做状态信号；最冷静的一组。",
    tokens: {
      "--paper": "#F9FAF7",
      "--panel": "#EDF1EE",
      "--ink": "#1F2628",
      "--ink-soft": "#515C5E",
      "--muted": "#7F8A8A",
      "--surface-strong": "#E2E8E3",
      "--line": "#DDE3DF",
      "--line-strong": "#B7C1BB",
      "--accent": "#3D6FE8",
      "--accent-soft": "#E1E8FB",
      "--indigo": "#3D5AA9",
      "--green": "#4E8F5E",
      "--orange": "#C98A2C",
      "--amber": "#B47F1B",
      "--red": "#D0483C",
      "--stone": "#A8B0AD",
      "--rail": "#1F2628",
      "--rail-fg": "#F9FAF7",
    },
  },
];

export interface SpaceAccentSet {
  id: string;
  label: string;
  accents: [string, string, string, string];
}

export const SPACE_ACCENT_SETS: SpaceAccentSet[] = [
  { id: "pink-family", label: "粉主色相", accents: ["#E85D75", "#F2A03D", "#57B8A0", "#8B7BD8"] },
  { id: "orange-family", label: "橙主色相", accents: ["#F0602F", "#E8B42D", "#3C9E8F", "#6C5CE7"] },
  { id: "blue-family", label: "蓝主色相", accents: ["#3D6FE8", "#4FB3A0", "#D97A3D", "#A06CDB"] },
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
    id: "geometric-warm",
    label: "F1 · 几何温暖",
    note: "Hanken Grotesk：几何骨架带一点圆润；IBM Plex Mono 做元数据。",
    sans: '"Hanken Grotesk Variable", "Hanken Grotesk", "Noto Sans SC", sans-serif',
    mono: '"IBM Plex Mono", ui-monospace, monospace',
  },
  {
    id: "modern-humanist",
    label: "F2 · 现代人文",
    note: "Instrument Sans：现代人文曲线；Spline Sans Mono 配套。",
    sans: '"Instrument Sans Variable", "Instrument Sans", "Noto Sans SC", sans-serif',
    mono: '"Spline Sans Mono", ui-monospace, monospace',
  },
  {
    id: "neutral-comfort",
    label: "F3 · 中性舒适",
    note: "Manrope：中性舒适；JetBrains Mono 做代码与元数据。",
    sans: '"Manrope Variable", "Manrope", "Noto Sans SC", sans-serif',
    mono: '"JetBrains Mono", ui-monospace, monospace',
  },
];

export interface TaskBadgeCandidate {
  id: string;
  label: string;
  note: string;
}

export const TASK_BADGE_CANDIDATES: TaskBadgeCandidate[] = [
  {
    id: "current",
    label: "T0 · 现状",
    note: "右上角大徽章，含 title / status / assignee。用户反馈：太大、与消息格格不入。",
  },
  {
    id: "inline-pill",
    label: "T1 · 行内胶囊",
    note: "消息头时间旁的小胶囊：!3 + 状态图形 + IN REVIEW。不占正文位置。",
  },
  {
    id: "metadata-row",
    label: "T2 · 元数据行",
    note: "与时间/seq 同一元数据行：!3 · IN REVIEW · Nora。最轻，hover 显示 title。",
  },
];

export const PIXEL_SCALE = [
  { size: 16, unit: 2, use: "小图标、焦点标记" },
  { size: 24, unit: 3, use: "消息内头像、状态图形" },
  { size: 32, unit: 4, use: "列表头像、Agent 印章" },
  { size: 48, unit: 6, use: "成员页头像、空态" },
  { size: 64, unit: 8, use: "详情页、登录装饰" },
] as const;
