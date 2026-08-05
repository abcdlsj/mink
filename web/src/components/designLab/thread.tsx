import { X } from "lucide-react";

import { PixelIdentity } from "../PixelIdentity";
import type { SurfaceDemo } from "./demoTypes";

function ThreadPage({ float = false, compact = false }: { float?: boolean; compact?: boolean }) {
  return (
    <div className="demo-page demo-page--thread">
      <main className="demo-main">
        <header className="demo-channel-header"><span>#general</span></header>
        <div className="demo-timeline">
          <div className="demo-msg">
            <PixelIdentity name="Mara" kind="human" seed="019c0000-0000-7000-8000-000000000002" />
            <div className="demo-msg-body">
              <header><strong>Mara</strong><time>14:32</time></header>
              <p>Root message of the thread.</p>
              <span className="demo-task-meta">!7 · IN REVIEW · Nora</span>
            </div>
          </div>
        </div>
        <footer className="demo-composer demo-composer--f1"><span>Message #general...</span></footer>
      </main>
      <aside className={`demo-thread${float ? " demo-thread--float" : ""}${compact ? " demo-thread--compact" : ""}`}>
        <header>
          <strong>Thread</strong>
          <button type="button" aria-label="Close Thread"><X aria-hidden="true" /></button>
        </header>
        <div className="demo-thread-root">
          <PixelIdentity name="Mara" kind="human" seed="019c0000-0000-7000-8000-000000000002" />
          <div><strong>Mara</strong><p>Root message of the thread.</p></div>
        </div>
        <div className="demo-thread-reply">
          <PixelIdentity name="Leo" kind="agent" seed="019c0000-0000-7000-8000-000000000020" />
          <div><strong>Leo</strong><p>A reply from the assignee.</p></div>
        </div>
      </aside>
    </div>
  );
}

export const THREAD_DEMOS: SurfaceDemo[] = [
  { id: "t1", label: "T1 · 独立栏", note: "Thread 常驻主区右栏，2px 左分隔；可拖拽调宽。", Component: () => <ThreadPage /> },
  { id: "t2", label: "T2 · 浮层", note: "Thread 覆盖在主区之上，带柔和阴影；窄屏天然适用。", Component: () => <ThreadPage float /> },
  { id: "t3", label: "T3 · 紧凑", note: "更窄的栏宽，root 与 replies 间距收紧，元数据隐藏。", Component: () => <ThreadPage compact /> },
];
