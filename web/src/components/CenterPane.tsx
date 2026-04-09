import { useEffect, useRef } from 'react'
import type { Message, Card } from '../lib/api'
import styles from './CenterPane.module.css'

interface CenterPaneProps {
  headerTitle: string
  headerSubtitle?: string
  headerMeta?: string[]
  messages?: Message[]
  cards?: Card[]
  emptyHint?: string
}

export function CenterPane({
  headerTitle,
  headerSubtitle,
  headerMeta,
  messages,
  cards,
  emptyHint,
}: CenterPaneProps) {
  const scrollRef = useRef<HTMLDivElement>(null)
  const stickToBottomRef = useRef(true)
  const seenMessageCountRef = useRef(0)

  const syncStickToBottom = () => {
    const el = scrollRef.current
    if (!el) return
    const distance = el.scrollHeight - el.scrollTop - el.clientHeight
    stickToBottomRef.current = distance <= 64
  }

  useEffect(() => {
    const el = scrollRef.current
    if (!el) return

    const nextCount = messages?.length ?? 0
    const shouldScroll = seenMessageCountRef.current === 0 || stickToBottomRef.current
    seenMessageCountRef.current = nextCount

    if (shouldScroll) {
      el.scrollTop = el.scrollHeight
    }
  }, [messages?.length])

  const hasMessages = messages && messages.length > 0
  const hasCards = cards && cards.length > 0

  return (
    <main className={styles.center}>
      <div className={styles.header}>
        <div className={styles.headerTitle}>{headerTitle}</div>
        {headerSubtitle && <div className={styles.headerSub}>{headerSubtitle}</div>}
        {headerMeta && headerMeta.length > 0 && (
          <div className={styles.headerMeta}>
            {headerMeta.map((m, i) => (
              <span key={i} className={styles.metaTag}>{m}</span>
            ))}
          </div>
        )}
      </div>

      {hasMessages ? (
        <div className={styles.messages} ref={scrollRef} onScroll={syncStickToBottom}>
          {messages.map((msg, i) => (
            <MessageBubble key={i} msg={msg} />
          ))}
        </div>
      ) : hasCards ? (
        <div className={styles.cards}>
          {cards.map((card, i) => (
            <div key={i} className={styles.card}>
              <div className={styles.cardTitle}>{card.title}</div>
              {card.subtitle && <div className={styles.cardSub}>{card.subtitle}</div>}
              {card.meta && <div className={styles.cardMeta}>{card.meta}</div>}
            </div>
          ))}
        </div>
      ) : (
        <div className={styles.emptyHint}>{emptyHint || 'No messages yet'}</div>
      )}
    </main>
  )
}

function MessageBubble({ msg }: { msg: Message }) {
  const roleClass =
    msg.role === 'user' ? styles.msgUser :
    msg.role === 'assistant' ? styles.msgAssistant :
    styles.msgSystem

  return (
    <div className={`${styles.msg} ${roleClass}`}>
      <div className={styles.msgHeader}>
        <span className={styles.msgSender}>{msg.sender}</span>
        {msg.descriptor && <span className={styles.msgDescriptor}>{msg.descriptor}</span>}
        {msg.time && <span className={styles.msgTime}>{msg.time}</span>}
      </div>
      {msg.reasoning && (
        <div className={styles.reasoning}>{msg.reasoning}</div>
      )}
      {msg.content && (
        <div className={styles.msgContent}>{msg.content}</div>
      )}
    </div>
  )
}
