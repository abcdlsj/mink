import type { NavItem } from '../lib/api'
import styles from './Rail.module.css'

const NAV_ICONS: Record<string, string> = {
  inbox: '📥',
  main: '💬',
  teams: '👥',
  threads: '🧵',
}

interface RailProps {
  nav: NavItem[]
  onSelect: (id: string) => void
}

export function Rail({ nav, onSelect }: RailProps) {
  return (
    <nav className={styles.rail}>
      <div className={styles.logo}>M</div>
      {nav.map((item) => (
        <button
          key={item.id}
          className={`${styles.navBtn} ${item.active ? styles.navBtnActive : ''}`}
          onClick={() => onSelect(item.id)}
          title={item.label}
        >
          {NAV_ICONS[item.id] ?? item.label.charAt(0)}
        </button>
      ))}
      <div className={styles.spacer} />
    </nav>
  )
}
