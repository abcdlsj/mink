import { ImagePlus, Send } from "lucide-react";

export function ComposerCandidates() {
  return (
    <div className="dl-region-grid">
      <article className="dl-candidate">
        <h3>F1 · 现状外框</h3>
        <p>2px ink 外框 + 硬阴影；附件左下、发送右下。基线。</p>
        <div className="dl-composer dl-composer--f1">
          <textarea readOnly value="Type a message..." aria-label="Message" />
          <span className="dl-composer-attach"><ImagePlus aria-hidden="true" /></span>
          <button type="button" aria-label="Send"><Send aria-hidden="true" /></button>
        </div>
      </article>
      <article className="dl-candidate">
        <h3>F2 · 悬浮卡片</h3>
        <p>圆角卡片悬浮于消息区底部，柔和阴影；外框变细。</p>
        <div className="dl-composer dl-composer--f2">
          <textarea readOnly value="Type a message..." aria-label="Message" />
          <span className="dl-composer-attach"><ImagePlus aria-hidden="true" /></span>
          <button type="button" aria-label="Send"><Send aria-hidden="true" /></button>
        </div>
      </article>
      <article className="dl-candidate">
        <h3>F3 · 底线内嵌</h3>
        <p>无外框，只有顶部细线；发送按钮在右下，视觉最轻。</p>
        <div className="dl-composer dl-composer--f3">
          <textarea readOnly value="Type a message..." aria-label="Message" />
          <span className="dl-composer-attach"><ImagePlus aria-hidden="true" /></span>
          <button type="button" aria-label="Send"><Send aria-hidden="true" /></button>
        </div>
      </article>
    </div>
  );
}
