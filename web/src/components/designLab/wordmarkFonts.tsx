import { Hash } from "lucide-react";

import { PixelWord } from "../PixelWord";
import type { SurfaceDemo } from "./demoTypes";
import { MainMock, Rail } from "./middleNav";

function FontPage({ variant }: { variant: "standard" | "bold" | "mini" }) {
  return (
    <div className="demo-page demo-page--dmn">
      <Rail />
      <nav className="dmn dmn--n1">
        <div className="dmn-brand dmn-brand--shadow">
          <PixelWord text="Sumi Dev" variant={variant} />
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

export const WORDMARK_FONT_DEMOS: SurfaceDemo[] = [
  { id: "f1", label: "F1 · 标准 5×7", note: "当前字形，笔画单像素。", Component: () => <FontPage variant="standard" /> },
  { id: "f2", label: "F2 · 粗体", note: "同网格膨胀加粗，笔画更实。", Component: () => <FontPage variant="bold" /> },
  { id: "f3", label: "F3 · 迷你 3×5", note: "更小的点阵字形，密而轻。", Component: () => <FontPage variant="mini" /> },
];
