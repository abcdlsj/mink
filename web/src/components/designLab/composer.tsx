import { Hash, ImagePlus, MessageCircle, Send } from "lucide-react";

import type { SurfaceDemo } from "./demoTypes";

function ComposerPage({ variant }: { variant: "f1" | "f2" | "f3" }) {
  return (
    <div className={`demo-page demo-page--composer demo-page--composer-${variant}`}>
      <aside className="demo-rail">
        <span className="demo-rail-space">S</span>
        <span className="demo-rail-tool demo-rail-tool--active"><MessageCircle aria-hidden="true" /></span>
        <span className="demo-rail-tool"><Hash aria-hidden="true" /></span>
      </aside>
      <main className="demo-main">
        <header className="demo-channel-header"><span>#general</span></header>
        <div className="demo-composer-stage">
          <div className="demo-cp-messages">
            <div className="demo-cp-line" />
            <div className="demo-cp-line" />
            <div className="demo-cp-line" />
          </div>
          <footer className={`demo-composer demo-composer--${variant} demo-composer--large`}>
            <span className="demo-composer-attach"><ImagePlus aria-hidden="true" /></span>
            <span className="demo-composer-input">Message #general...</span>
            <button type="button" aria-label="Send"><Send aria-hidden="true" /></button>
          </footer>
        </div>
      </main>
    </div>
  );
}

export const COMPOSER_DEMOS: SurfaceDemo[] = [
  { id: "f1", label: "F1 · 外框", note: "2px ink 外框 + 硬阴影，附件左下、发送右下；现状基线。", Component: () => <ComposerPage variant="f1" /> },
  { id: "f2", label: "F2 · 悬浮", note: "圆角卡片悬浮于消息区底部，柔和阴影；输入区更大。", Component: () => <ComposerPage variant="f2" /> },
  { id: "f3", label: "F3 · 内嵌", note: "无外框，顶部细线分隔；视觉最轻，贴近正文。", Component: () => <ComposerPage variant="f3" /> },
];
