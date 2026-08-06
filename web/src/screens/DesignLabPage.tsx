import { Link, useParams } from "@tanstack/react-router";
import { ArrowLeft, ExternalLink } from "lucide-react";
import { useState, type CSSProperties, type ComponentType } from "react";

// Candidate typefaces are only loaded for the comparison board.
import "@fontsource-variable/hanken-grotesk";
import "@fontsource-variable/instrument-sans";
import "@fontsource-variable/manrope";
import "@fontsource/ibm-plex-mono/400.css";
import "@fontsource/ibm-plex-mono/600.css";
import "@fontsource/spline-sans-mono/400.css";
import "@fontsource/spline-sans-mono/600.css";
import "@fontsource/jetbrains-mono/400.css";
import "@fontsource/jetbrains-mono/600.css";

import { AX_DEMOS } from "../components/designLab/ax";
import { CHANNEL_DEMOS } from "../components/designLab/channel";
import { COMPOSER_DEMOS } from "../components/designLab/composer";
import { COMPUTERS_DEMOS } from "../components/designLab/computers";
import type { SurfaceDemo } from "../components/designLab/demoTypes";
import { INBOX_DEMOS } from "../components/designLab/inbox";
import { MEMBER_MANAGEMENT_DEMOS } from "../components/designLab/memberManagement";
import { MEMBERS_DEMOS } from "../components/designLab/members";
import { MIDDLE_NAV_DEMOS } from "../components/designLab/middleNav";
import { MESSAGES_DEMOS } from "../components/designLab/messages";
import { ONBOARDING_DEMOS } from "../components/designLab/onboarding";
import { SHELL_DEMOS } from "../components/designLab/shell";
import { SPACE_WORDMARK_DEMOS } from "../components/designLab/spaceWordmark";
import { THREAD_DEMOS } from "../components/designLab/thread";
import { TASK_DETAIL_DEMOS } from "../components/designLab/taskDetail";
import {
  FONT_CANDIDATES,
  PALETTE_CANDIDATES,
  SPACE_ACCENT_SETS,
} from "../designCandidates";
import "../styles/design-lab.css";

interface Surface {
  id: string;
  label: string;
  description: string;
  demos: SurfaceDemo[];
}

const SURFACES: Surface[] = [
  { id: "shell", label: "Shell layout", description: "三栏 / 紧凑双栏 / 顶栏式", demos: SHELL_DEMOS },
  { id: "space-wordmark", label: "Space wordmark", description: "顶部艺术字 5 种变体", demos: SPACE_WORDMARK_DEMOS },
  { id: "middle-nav", label: "Middle navigation", description: "中间栏的 4 种设计", demos: MIDDLE_NAV_DEMOS },
  { id: "channel", label: "Channel", description: "行式 / 气泡 / 沉浸式", demos: CHANNEL_DEMOS },
  { id: "messages", label: "Message & Task", description: "Task 标识位置与浮控按钮", demos: MESSAGES_DEMOS },
  { id: "task-detail", label: "Task detail", description: "工作线主导 / 分区 / 双栏", demos: TASK_DETAIL_DEMOS },
  { id: "composer", label: "Composer", description: "外框 / 悬浮 / 内嵌", demos: COMPOSER_DEMOS },
  { id: "thread", label: "Thread", description: "独立栏 / 浮层 / 紧凑", demos: THREAD_DEMOS },
  { id: "inbox", label: "Inbox", description: "聚合行 / 卡片 / 密集", demos: INBOX_DEMOS },
  { id: "members", label: "Members & Agents", description: "扁平行 / 分组 / 密集", demos: MEMBERS_DEMOS },
  { id: "member-management", label: "Member management", description: "权限、邀请、生命周期管理", demos: MEMBER_MANAGEMENT_DEMOS },
  { id: "computers", label: "Computers", description: "列表 / 卡片 / 密集", demos: COMPUTERS_DEMOS },
  { id: "onboarding", label: "Onboarding", description: "横幅 / 居中 / 分栏", demos: ONBOARDING_DEMOS },
  { id: "ax", label: "AX signals", description: "Review 双主体 / 焦点 / 像素", demos: AX_DEMOS },
];

function useDesignLabVariables() {
  const initial = new URLSearchParams(window.location.search);
  const [paletteId, setPaletteId] = useState(
    () => initial.get("palette") ?? PALETTE_CANDIDATES[0].id,
  );
  const [fontId, setFontId] = useState(() => initial.get("font") ?? FONT_CANDIDATES[0].id);
  const [accentSetId, setAccentSetId] = useState(
    () => initial.get("accents") ?? SPACE_ACCENT_SETS[0].id,
  );
  function choose(key: "palette" | "font" | "accents", value: string) {
    const url = new URL(window.location.href);
    url.searchParams.set(key, value);
    window.history.replaceState(null, "", url);
  }
  const palette = PALETTE_CANDIDATES.find((candidate) => candidate.id === paletteId)!;
  const font = FONT_CANDIDATES.find((candidate) => candidate.id === fontId)!;
  const accentSet = SPACE_ACCENT_SETS.find((candidate) => candidate.id === accentSetId)!;
  const variables = {
    ...palette.tokens,
    "--space-accent": accentSet.accents[0],
    "--font-sans": font.sans,
    "--font-mono": font.mono,
  } as CSSProperties;
  return {
    variables,
    palette,
    paletteId,
    setPaletteId,
    font,
    fontId,
    setFontId,
    accentSetId,
    setAccentSetId,
    choose,
  };
}

function Controls({ ctx }: { ctx: ReturnType<typeof useDesignLabVariables> }) {
  return (
    <div className="dlp-controls">
      <div className="dlp-segment" role="group" aria-label="Palette candidates">
        {PALETTE_CANDIDATES.map((candidate) => (
          <button
            key={candidate.id}
            type="button"
            aria-pressed={candidate.id === ctx.paletteId}
            onClick={() => {
              ctx.setPaletteId(candidate.id);
              ctx.choose("palette", candidate.id);
            }}
          >
            {candidate.label}
          </button>
        ))}
      </div>
      <div className="dlp-segment" role="group" aria-label="Typeface candidates">
        {FONT_CANDIDATES.map((candidate) => (
          <button
            key={candidate.id}
            type="button"
            aria-pressed={candidate.id === ctx.fontId}
            onClick={() => {
              ctx.setFontId(candidate.id);
              ctx.choose("font", candidate.id);
            }}
          >
            {candidate.label}
          </button>
        ))}
      </div>
      <div className="dlp-accents" role="group" aria-label="Space accent families">
        {SPACE_ACCENT_SETS.map((set) => (
          <button
            key={set.id}
            type="button"
            className={`dlp-accent${set.id === ctx.accentSetId ? " dlp-accent--active" : ""}`}
            aria-pressed={set.id === ctx.accentSetId}
            onClick={() => {
              ctx.setAccentSetId(set.id);
              ctx.choose("accents", set.id);
            }}
          >
            {set.accents.map((accent) => <span key={accent} style={{ background: accent }} />)}
          </button>
        ))}
      </div>
    </div>
  );
}

export function DesignLabPage() {
  const { spaceSlug } = useParams({ from: "/s/$spaceSlug/design-lab" });
  const ctx = useDesignLabVariables();
  return (
    <div className="dlx" style={ctx.variables}>
      <header className="dlx-header">
        <div>
          <Link
            className="dlx-exit"
            to="/s/$spaceSlug/channels/$channelSlug"
            params={{ spaceSlug, channelSlug: "general" }}
          >
            <ArrowLeft aria-hidden="true" /> Exit design lab
          </Link>
          <h1>Design lab</h1>
          <p>每个表面都是独立页面，方案之间自由切换；布局不受现有三栏限制。</p>
        </div>
        <Controls ctx={ctx} />
      </header>
      <div className="dlx-grid">
        {SURFACES.map((surface) => (
          <Link
            key={surface.id}
            className="dlx-card"
            to="/s/$spaceSlug/design-lab/$surface"
            params={{ spaceSlug, surface: surface.id }}
          >
            <span className="dlx-card-label">{surface.label}</span>
            <span className="dlx-card-note">{surface.description}</span>
            <span className="dlx-card-count">{surface.demos.length} 方案</span>
            <ExternalLink className="dlx-card-arrow" aria-hidden="true" />
          </Link>
        ))}
      </div>
    </div>
  );
}

export function DesignLabSurfacePage() {
  const { spaceSlug, surface } = useParams({ from: "/s/$spaceSlug/design-lab/$surface" });
  const surfaceInfo = SURFACES.find((candidate) => candidate.id === surface) ?? SURFACES[0];
  const [demoId, setDemoId] = useState(() => {
    const remembered = new URLSearchParams(window.location.search).get("demo");
    return surfaceInfo.demos.some((demo) => demo.id === remembered)
      ? remembered!
      : surfaceInfo.demos[0].id;
  });
  const ctx = useDesignLabVariables();
  const activeDemo = surfaceInfo.demos.find((demo) => demo.id === demoId) ?? surfaceInfo.demos[0];
  const Demo = activeDemo.Component as ComponentType;
  function chooseDemo(id: string) {
    setDemoId(id);
    const url = new URL(window.location.href);
    url.searchParams.set("demo", id);
    window.history.replaceState(null, "", url);
  }
  return (
    <div className="dlp" style={ctx.variables}>
      <header className="dlp-topbar">
        <Link className="dlp-back" to="/s/$spaceSlug/design-lab" params={{ spaceSlug }}>
          <ArrowLeft aria-hidden="true" /> Library
        </Link>
        <h1>{surfaceInfo.label}</h1>
        <div className="dlp-tabs" role="group" aria-label="Surface variants">
          {surfaceInfo.demos.map((demo) => (
            <button
              key={demo.id}
              type="button"
              aria-pressed={demo.id === activeDemo.id}
              onClick={() => chooseDemo(demo.id)}
            >
              {demo.label}
            </button>
          ))}
        </div>
        <Controls ctx={ctx} />
      </header>
      <p className="dlp-note">{activeDemo.note}</p>
      <div className="dlp-stage">
        <Demo />
      </div>
    </div>
  );
}
