import { Inbox as InboxIcon } from "lucide-react";

import { PixelIdentity } from "../PixelIdentity";
import type { SurfaceDemo } from "./demoTypes";

const GROUPS = [
  { name: "Mara", kind: "human" as const, seed: "019c0000-0000-7000-8000-000000000002", title: "Mara · DM", preview: "Can you review the design thread?", count: 3 },
  { name: "Iris", kind: "agent" as const, seed: "019c0000-0000-7000-8000-000000000021", title: "Iris · DM", preview: "Status from @Iris on !3 Timeline density.", count: 1 },
  { name: "Leo", kind: "agent" as const, seed: "019c0000-0000-7000-8000-000000000020", title: "Thread reply", preview: "Review submission for Task !3.", count: 2 },
];

function InboxPage({ variant }: { variant: "rows" | "cards" | "dense" }) {
  return (
    <div className="demo-page demo-page--inbox">
      <main className="demo-main">
        <header className="demo-channel-header"><InboxIcon aria-hidden="true" /> Inbox <span className="demo-channel-members">6 pending</span></header>
        <div className={`demo-inbox demo-inbox--${variant}`}>
          {GROUPS.map((group) => (
            <article key={group.title} className="demo-inbox-item">
              <PixelIdentity name={group.name} kind={group.kind} seed={group.seed} />
              <div className="demo-inbox-body">
                <header><strong>{group.title}</strong><time>2m</time><span className="demo-inbox-count">{group.count}</span></header>
                <p>{group.preview}</p>
              </div>
            </article>
          ))}
        </div>
      </main>
    </div>
  );
}

export const INBOX_DEMOS: SurfaceDemo[] = [
  { id: "i1", label: "I1 · 聚合行", note: "扁平行 + 分隔线；发送者、预览、时间、未读数。", Component: () => <InboxPage variant="rows" /> },
  { id: "i2", label: "I2 · 卡片", note: "每组一张浅色卡片，圆角 + 细边框；组间留白更清晰。", Component: () => <InboxPage variant="cards" /> },
  { id: "i3", label: "I3 · 密集", note: "行高收紧、预览单行省略；一屏看更多组。", Component: () => <InboxPage variant="dense" /> },
];
