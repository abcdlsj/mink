import { ListTodo, MessageCircle } from "lucide-react";
import { useState } from "react";

import { PixelIdentity } from "../PixelIdentity";
import type { SurfaceDemo } from "./demoTypes";

const BADGE_VARIANTS = ["header", "bottom-left", "top-right"] as const;
const ACTION_VARIANTS = ["square", "pill", "text"] as const;

function BadgeDemo({ variant }: { variant: typeof BADGE_VARIANTS[number] }) {
  return (
    <div className={`demo-badge-message demo-badge-message--${variant}`}>
      <PixelIdentity name="Mara" kind="human" seed="019c0000-0000-7000-8000-000000000002" />
      <div>
        <header>
          <strong>Mara</strong>
          {variant === "header" && <span className="demo-task-meta">!7 · IN REVIEW · Nora</span>}
          <time>14:32</time>
        </header>
        <p>This is the message body the badge must not compete with.</p>
        {variant === "bottom-left" && <span className="demo-task-meta">!7 · IN REVIEW · Nora</span>}
      </div>
      {variant === "top-right" && <span className="demo-task-meta demo-task-meta--corner">!7 · IN REVIEW</span>}
      {variant === "top-right" && <time className="demo-badge-time">14:32</time>}
    </div>
  );
}

function ActionDemo({ variant }: { variant: typeof ACTION_VARIANTS[number] }) {
  return (
    <div className="demo-actions-message">
      <PixelIdentity name="Mara" kind="human" seed="019c0000-0000-7000-8000-000000000002" />
      <div>
        <header><strong>Mara</strong><time>14:32</time></header>
        <p>Message body that the floating actions must not cover.</p>
      </div>
      <div className={`demo-actions demo-actions--${variant}`}>
        {variant === "text" ? (
          <span>Reply · Task</span>
        ) : (
          <>
            <button type="button" aria-label="Reply to Thread"><MessageCircle aria-hidden="true" /></button>
            <button type="button" aria-label="Create Task"><ListTodo aria-hidden="true" /></button>
          </>
        )}
      </div>
    </div>
  );
}

function MessagesDemo() {
  const [badge, setBadge] = useState<typeof BADGE_VARIANTS[number]>("header");
  const [actions, setActions] = useState<typeof ACTION_VARIANTS[number]>("square");
  return (
    <div className="demo-page demo-messages-page">
      <header className="demo-channel-header">
        <span>Message &amp; Task</span>
        <div className="demo-segment">
          {BADGE_VARIANTS.map((variant) => (
            <button key={variant} type="button" aria-pressed={badge === variant} onClick={() => setBadge(variant)}>{variant}</button>
          ))}
        </div>
        <div className="demo-segment">
          {ACTION_VARIANTS.map((variant) => (
            <button key={variant} type="button" aria-pressed={actions === variant} onClick={() => setActions(variant)}>{variant} actions</button>
          ))}
        </div>
      </header>
      <div className="demo-messages-stage">
        <BadgeDemo variant={badge} />
        <ActionDemo variant={actions} />
      </div>
    </div>
  );
}

export const MESSAGES_DEMOS: SurfaceDemo[] = [
  { id: "m1", label: "M1 · 组件对比", note: "同一页面切换 Task 标识位置（header / 左下 / 右上）与动作按钮形态（方角 / 胶囊 / 文字）。", Component: MessagesDemo },
];
