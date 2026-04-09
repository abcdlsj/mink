import type { ContextBlock } from '../lib/api'
import styles from './ContextPane.module.css'

interface ContextPaneProps {
  title?: string
  blocks: ContextBlock[]
}

export function ContextPane({ title, blocks }: ContextPaneProps) {
  if (blocks.length === 0) return null

  return (
    <aside className={styles.context}>
      <div className={styles.header}>
        <span className={styles.title}>{title || 'Context'}</span>
      </div>
      <div className={styles.blocks}>
        {blocks.map((block, i) => (
          <div key={i} className={styles.block}>
            <div className={styles.blockTitle}>{block.title}</div>
            <div className={styles.blockBody}>{block.body}</div>
          </div>
        ))}
      </div>
    </aside>
  )
}
