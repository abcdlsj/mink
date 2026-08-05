import { X } from "lucide-react";
import { PixelIdentity } from "../PixelIdentity";

function ThreadMock({ float = false }: { float?: boolean }) {
  return (
    <div className={`dl-thread-mock${float ? " dl-thread-mock--float" : ""}`}>
      <header>
        <strong>Thread</strong>
        <button type="button" aria-label="Close"><X aria-hidden="true" /></button>
      </header>
      <div className="dl-thread-mock-root">
        <PixelIdentity name="Mara" kind="human" seed="019c0000-0000-7000-8000-000000000002" />
        <div>
          <strong>Mara</strong>
          <p>Root message of this thread.</p>
          <span>!7 · IN REVIEW</span>
        </div>
      </div>
      <div className="dl-thread-mock-reply">
        <PixelIdentity name="Leo" kind="agent" seed="019c0000-0000-7000-8000-000000000020" />
        <div><strong>Leo</strong><p>A reply from the assignee.</p></div>
      </div>
    </div>
  );
}

export function ThreadCandidates() {
  return (
    <div className="dl-region-grid">
      <article className="dl-candidate">
        <h3>T1 · 现状独立栏</h3>
        <p>主区内右栏，2px 左分隔；root + replies。</p>
        <div className="dl-thread-stage"><ThreadMock /></div>
      </article>
      <article className="dl-candidate">
        <h3>T2 · 浮层覆盖</h3>
        <p>Thread 从右侧滑出覆盖主区，带柔和阴影；窄屏统一体验。</p>
        <div className="dl-thread-stage"><ThreadMock float /></div>
      </article>
      <article className="dl-candidate">
        <h3>T3 · 紧凑栏</h3>
        <p>更窄（320px），root 与 replies 间距收紧，元数据隐藏。</p>
        <div className="dl-thread-stage dl-thread-stage--compact"><ThreadMock /></div>
      </article>
    </div>
  );
}
