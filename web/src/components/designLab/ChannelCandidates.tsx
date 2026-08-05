import { PixelIdentity } from "../PixelIdentity";

function MessageMock({ bubble = false, dense = false }: { bubble?: boolean; dense?: boolean }) {
  return (
    <div className={`dl-c-message${bubble ? " dl-c-message--bubble" : ""}${dense ? " dl-c-message--dense" : ""}`}>
      <PixelIdentity name="Mara" kind="human" seed="019c0000-0000-7000-8000-000000000002" />
      <div className="dl-c-message-body">
        <header><strong>Mara</strong><time>14:32</time></header>
        <p>This is the message body. Layout should keep it readable at 13px with a calm rhythm.</p>
        <span className="dl-c-task">!7 · IN REVIEW</span>
      </div>
    </div>
  );
}

export function ChannelCandidates() {
  return (
    <div className="dl-region-grid">
      <article className="dl-candidate">
        <h3>C1 · 现状消息行</h3>
        <p>头像 + 正文行 + 细分隔；动作 hover 出现。现有基线。</p>
        <div className="dl-c-timeline">
          <MessageMock />
          <MessageMock />
        </div>
      </article>
      <article className="dl-candidate">
        <h3>C2 · 气泡式</h3>
        <p>正文在浅色圆角气泡内，头像保留；Human 与 Agent 同色。</p>
        <div className="dl-c-timeline">
          <MessageMock bubble />
          <MessageMock bubble />
        </div>
      </article>
      <article className="dl-candidate">
        <h3>C3 · 紧凑</h3>
        <p>行距收紧、时间隐藏（hover 显示）、任务标识贴正文。</p>
        <div className="dl-c-timeline">
          <MessageMock dense />
          <MessageMock dense />
        </div>
      </article>
    </div>
  );
}
