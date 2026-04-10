import { useEffect, useRef } from 'react'
import type { Message, Card, ToolCall, ToolResult } from '../lib/api'
import styles from './CenterPane.module.css'

interface CenterPaneProps {
  headerTitle: string
  headerSubtitle?: string
  headerMeta?: string[]
  messages?: Message[]
  cards?: Card[]
  emptyHint?: string
  section?: string
}

export function CenterPane({
  headerTitle,
  headerSubtitle,
  headerMeta,
  messages,
  cards,
  emptyHint,
  section,
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
  const showTools = section === 'main' || !section

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
          {messages.map((msg, i) => {
            const prev = i > 0 ? messages[i - 1] : null
            const isMinkRole = (r: string) => r === 'assistant' || r === 'tool'
            const continuation = prev !== null &&
              ((prev.sender === msg.sender && prev.role === msg.role) ||
               (isMinkRole(prev.role) && isMinkRole(msg.role)))
            return (
              <MessageBubble
                key={i}
                msg={msg}
                continuation={continuation}
                showTools={showTools}
              />
            )
          })}
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

function MessageBubble({ msg, continuation, showTools }: {
  msg: Message
  continuation: boolean
  showTools: boolean
}) {
  const roleClass =
    msg.role === 'user' ? styles.msgUser :
    msg.role === 'tool' ? styles.msgAssistant :
    msg.role === 'assistant' ? styles.msgAssistant :
    styles.msgSystem

  if (!showTools && msg.role === 'tool') return null
  if (!showTools && !msg.content && !msg.reasoning &&
    ((msg.toolCalls && msg.toolCalls.length > 0) || (msg.toolResults && msg.toolResults.length > 0))) {
    return null
  }

  return (
    <div className={`${styles.msg} ${roleClass} ${continuation ? styles.msgContinuation : ''}`}>
      {!continuation && (
        <div className={styles.msgHeader}>
          <span className={styles.msgSender}>{msg.sender}</span>
          {msg.descriptor && <span className={styles.msgDescriptor}>{msg.descriptor}</span>}
          {msg.time && <span className={styles.msgTime}>{msg.time}</span>}
        </div>
      )}
      {msg.reasoning && (
        <div className={styles.reasoning}>{msg.reasoning}</div>
      )}
      {showTools && msg.toolCalls && msg.toolCalls.length > 0 && (
        <div className={styles.toolStack}>
          {msg.toolCalls.map((call, i) => (
            <ToolCallBlock key={`${call.name}-${i}`} call={call} />
          ))}
        </div>
      )}
      {showTools && msg.toolResults && msg.toolResults.length > 0 && (
        <div className={styles.toolStack}>
          {msg.toolResults.map((result, i) => (
            <ToolResultBlock key={i} result={result} />
          ))}
        </div>
      )}
      {msg.content && (
        <div className={styles.msgContent}>{msg.content}</div>
      )}
    </div>
  )
}

function ToolCallBlock({ call }: { call: ToolCall }) {
  return (
    <div className={styles.toolLine}>tool call: {call.name}{call.args ? ` ${call.args}` : ''}</div>
  )
}

function ToolResultBlock({ result }: { result: ToolResult }) {
  const body = result.error || result.content || '(no output)'
  return (
    <div className={`${styles.toolLine} ${result.error ? styles.toolLineError : ''}`}>
      tool result: {body}
    </div>
  )
}
