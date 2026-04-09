import type { IndexGroup } from '../lib/api'
import styles from './IndexPane.module.css'

interface IndexPaneProps {
  title: string
  groups: IndexGroup[]
  actionLabel?: string
  onAction?: () => void
  onSelect: (section: string, id: string) => void
}

export function IndexPane({ title, groups, actionLabel, onAction, onSelect }: IndexPaneProps) {
  return (
    <aside className={styles.index}>
      <div className={styles.header}>
        <span className={styles.title}>{title}</span>
        {actionLabel && (
          <button className={styles.actionBtn} onClick={onAction}>
            {actionLabel}
          </button>
        )}
      </div>
      <div className={styles.list}>
        {groups.map((group) => (
          <div key={group.title}>
            {group.title && <div className={styles.groupTitle}>{group.title}</div>}
            {group.items.map((item) => (
              <div
                key={item.id}
                className={`${styles.item} ${item.active ? styles.itemActive : ''}`}
                onClick={() => onSelect(item.section, item.id)}
              >
                <span className={styles.itemLabel}>{item.label}</span>
                {item.meta && <span className={styles.itemMeta}>{item.meta}</span>}
              </div>
            ))}
          </div>
        ))}
      </div>
    </aside>
  )
}
