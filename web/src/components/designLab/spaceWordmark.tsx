import { Hash } from "lucide-react";

import { PixelWord } from "../PixelWord";
import type { SurfaceDemo } from "./demoTypes";
import { MainMock, Rail } from "./middleNav";

function WordmarkPage({ variant }: { variant: string }) {
  return (
    <div className="demo-page demo-page--dmn">
      <Rail />
      <nav className="dmn dmn--n1">
        <div className={`dmn-brand dmn-brand--${variant}`}>
          <PixelWord text="Sumi Dev" />
        </div>
        <div className="dmn-scroll">
          <p className="dmn-label">CHANNELS <span>3</span></p>
          {["general", "design-lab", "empty-lab"].map((channel, index) => (
            <span key={channel} className={`dmn-item${index === 0 ? " dmn-item--active" : ""}`}><Hash aria-hidden="true" /> {channel}</span>
          ))}
        </div>
      </nav>
      <MainMock />
    </div>
  );
}

export const SPACE_WORDMARK_DEMOS: SurfaceDemo[] = [
  { id: "w1", label: "W1 · 裸字", note: "像素字直接呈现，space 主题色；现状基线。", Component: () => <WordmarkPage variant="bare" /> },
  { id: "w2", label: "W2 · 印章框", note: "像素字收进 2px ink 边框方框，像印章。", Component: () => <WordmarkPage variant="seal" /> },
  { id: "w3", label: "W3 · 硬影牌", note: "白底 + ink 边框 + 硬偏移阴影，物件感。", Component: () => <WordmarkPage variant="shadow" /> },
  { id: "w4", label: "W4 · 反色块", note: "space 主题色实色块 + 纸色像素字，最醒目。", Component: () => <WordmarkPage variant="block" /> },
  { id: "w5", label: "W5 · 工程线", note: "像素字上下各一条 ink 线，工程排布。", Component: () => <WordmarkPage variant="rule" /> },
];
