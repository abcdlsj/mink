import { Eye } from "lucide-react";

import { PixelIdentity } from "../PixelIdentity";
import type { SurfaceDemo } from "./demoTypes";

function AxPage() {
  return (
    <div className="demo-page demo-page--ax">
      <main className="demo-main demo-main--ax">
        <header className="demo-channel-header"><Eye aria-hidden="true" /> AX signals</header>
        <div className="demo-ax-grid">
          <article className="demo-ax-card">
            <h3>Review shows reviewer</h3>
            <div className="demo-review-mock"><span>IN REVIEW · Nora</span></div>
            <p>reviewer can be Human or Agent — depends on assignment.</p>
          </article>
          <article className="demo-ax-card">
            <h3>Focus marker</h3>
            <div className="demo-focus-row">
              <span className="demo-focus-avatar">
                <PixelIdentity name="Leo" kind="agent" seed="019c0000-0000-7000-8000-000000000020" />
                <i className="demo-focus-dot" aria-label="active run" />
              </span>
              <span>Leo · focus #design-lab:7</span>
            </div>
            <p>quiet marker on the Agent seal while a Run is active.</p>
          </article>
          <article className="demo-ax-card">
            <h3>Pixel scale</h3>
            <div className="demo-pixel-row">
              {[20, 24, 32, 36, 48].map((size) => (
                <span key={size} className="demo-pixel-step">
                  <span style={{ width: size, height: size }}>
                    <PixelIdentity name="Lin" kind="agent" seed="019c0000-0000-7000-8000-000000000020" />
                  </span>
                  <small>{size}px</small>
                </span>
              ))}
            </div>
            <p>one 8×8 grid, integer pixel units.</p>
          </article>
        </div>
      </main>
    </div>
  );
}

export const AX_DEMOS: SurfaceDemo[] = [
  { id: "ax1", label: "AX · 信号集合", note: "Review 双主体、焦点标记、像素尺寸在一个页面统一展示。", Component: AxPage },
];
