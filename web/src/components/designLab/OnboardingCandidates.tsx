import { ArrowRight } from "lucide-react";

export function OnboardingCandidates() {
  return (
    <div className="dl-region-grid">
      <article className="dl-candidate">
        <h3>O1 · 现状横幅</h3>
        <p>深色顶栏 + 大标题 + 左侧表单；品牌在顶部。基线。</p>
        <div className="dl-o-card">
          <header><span className="dl-o-brand">S</span><span>SUMI</span></header>
          <div className="dl-o-body">
            <h1>Sign in.</h1>
            <div className="dl-o-field" />
            <div className="dl-o-field" />
            <button type="button">Sign in <ArrowRight aria-hidden="true" /></button>
          </div>
        </div>
      </article>
      <article className="dl-candidate">
        <h3>O2 · 居中卡片</h3>
        <p>表单在页面中央卡片内，品牌以小标识呈现。</p>
        <div className="dl-o-card dl-o-card--center">
          <span className="dl-o-brand">S</span>
          <h1>Sign in.</h1>
          <div className="dl-o-field" />
          <div className="dl-o-field" />
          <button type="button">Sign in <ArrowRight aria-hidden="true" /></button>
        </div>
      </article>
      <article className="dl-candidate">
        <h3>O3 · 分栏</h3>
        <p>左侧品牌区（标识 + 一句话），右侧表单；信息层级更强。</p>
        <div className="dl-o-card dl-o-card--split">
          <aside><span className="dl-o-brand">S</span><p>Human and Agent, one Space.</p></aside>
          <div>
            <h1>Sign in.</h1>
            <div className="dl-o-field" />
            <div className="dl-o-field" />
            <button type="button">Sign in <ArrowRight aria-hidden="true" /></button>
          </div>
        </div>
      </article>
    </div>
  );
}
