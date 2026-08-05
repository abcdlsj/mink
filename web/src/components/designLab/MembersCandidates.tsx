import { PixelIdentity } from "../PixelIdentity";

function MemberRowMock({ card = false, dense = false }: { card?: boolean; dense?: boolean }) {
  return (
    <div className={`dl-m-row${card ? " dl-m-row--card" : ""}${dense ? " dl-m-row--dense" : ""}`}>
      <PixelIdentity name="Leo" kind="agent" seed="019c0000-0000-7000-8000-000000000020" />
      <div className="dl-m-body">
        <strong>Leo</strong>
        <span className="dl-m-status"><i /> working</span>
      </div>
      <span className="dl-m-permission">channel.create</span>
      <button type="button" aria-label="Message">✉</button>
    </div>
  );
}

export function MembersCandidates() {
  return (
    <div className="dl-region-grid">
      <article className="dl-candidate">
        <h3>M1 · 现状扁平行</h3>
        <p>身份 + 状态 + 权限 + 消息按钮，行间细分隔。基线。</p>
        <div className="dl-m-list">
          <MemberRowMock />
          <MemberRowMock />
        </div>
      </article>
      <article className="dl-candidate">
        <h3>M2 · 分组卡片</h3>
        <p>Agents / Humans 两组卡片，权限摘要一行展示。</p>
        <div className="dl-m-list">
          <div className="dl-m-group-label">Agents</div>
          <MemberRowMock card />
          <MemberRowMock card />
          <div className="dl-m-group-label">Humans</div>
          <MemberRowMock card />
        </div>
      </article>
      <article className="dl-candidate">
        <h3>M3 · 密集行</h3>
        <p>行高收紧，权限图标化，状态点更小。</p>
        <div className="dl-m-list">
          <MemberRowMock dense />
          <MemberRowMock dense />
          <MemberRowMock dense />
        </div>
      </article>
    </div>
  );
}
