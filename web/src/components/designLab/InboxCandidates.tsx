import { PixelIdentity } from "../PixelIdentity";

function InboxItemMock({ card = false, dense = false }: { card?: boolean; dense?: boolean }) {
  return (
    <div className={`dl-i-item${card ? " dl-i-item--card" : ""}${dense ? " dl-i-item--dense" : ""}`}>
      <PixelIdentity name="Mara" kind="human" seed="019c0000-0000-7000-8000-000000000002" />
      <div className="dl-i-body">
        <header><strong>Mara · DM</strong><time>2m</time><span className="dl-i-count">3</span></header>
        <p>Preview of the latest message in this group...</p>
      </div>
    </div>
  );
}

export function InboxCandidates() {
  return (
    <div className="dl-region-grid">
      <article className="dl-candidate">
        <h3>I1 · 现状聚合行</h3>
        <p>扁平行 + 分隔线，发送者 + 预览 + 时间 + 未读数。基线。</p>
        <div className="dl-i-list">
          <InboxItemMock />
          <InboxItemMock />
          <InboxItemMock />
        </div>
      </article>
      <article className="dl-candidate">
        <h3>I2 · 卡片聚合</h3>
        <p>每组一张浅色卡片，圆角 + 细边框；组间留白更清晰。</p>
        <div className="dl-i-list">
          <InboxItemMock card />
          <InboxItemMock card />
        </div>
      </article>
      <article className="dl-candidate">
        <h3>I3 · 密集行</h3>
        <p>行高收紧，单行预览省略；一屏看更多组。</p>
        <div className="dl-i-list">
          <InboxItemMock dense />
          <InboxItemMock dense />
          <InboxItemMock dense />
          <InboxItemMock dense />
        </div>
      </article>
    </div>
  );
}
