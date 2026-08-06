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
  { id: "f2", label: "F2 · 粗体", note: "同网格膨胀加粗；产品已采用，长名字自适应换行。", Component: () => <FontPage variant="bold" /> },
];
