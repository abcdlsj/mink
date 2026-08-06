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
  { id: "w3", label: "W3 · 硬影牌", note: "白底 + ink 边框 + 硬偏移阴影；产品已采用。", Component: () => <WordmarkPage variant="shadow" /> },
];
