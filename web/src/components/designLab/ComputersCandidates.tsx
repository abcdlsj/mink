import { Monitor } from "lucide-react";

function ComputerMock({ card = false, dense = false }: { card?: boolean; dense?: boolean }) {
  return (
    <div className={`dl-cp-item${card ? " dl-cp-item--card" : ""}${dense ? " dl-cp-item--dense" : ""}`}>
      <span className="dl-cp-icon"><Monitor aria-hidden="true" /></span>
      <div className="dl-cp-body">
        <strong>Dev Computer</strong>
        <span><i className="dl-cp-online" /> online · v0.1.0</span>
      </div>
      <span className="dl-cp-agents">3 agents</span>
    </div>
  );
}

export function ComputersCandidates() {
  return (
    <div className="dl-region-grid">
      <article className="dl-candidate">
        <h3>CP1 · 现状列表</h3>
        <p>左列表 + 右 onboarding/详情；扁平行。基线。</p>
        <div className="dl-cp-list">
          <ComputerMock />
          <ComputerMock />
        </div>
      </article>
      <article className="dl-candidate">
        <h3>CP2 · 卡片网格</h3>
        <p>Computer 卡片网格：状态、版本、Agent 数一眼可见。</p>
        <div className="dl-cp-grid">
          <ComputerMock card />
          <ComputerMock card />
        </div>
      </article>
      <article className="dl-candidate">
        <h3>CP3 · 密集列表</h3>
        <p>更小的行高，状态用色点，信息靠右排列。</p>
        <div className="dl-cp-list">
          <ComputerMock dense />
          <ComputerMock dense />
          <ComputerMock dense />
        </div>
      </article>
    </div>
  );
}
