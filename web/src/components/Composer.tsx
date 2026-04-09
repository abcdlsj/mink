import { useState, useRef, type KeyboardEvent } from 'react'
import styles from './Composer.module.css'

interface ComposerProps {
  label: string
  placeholder: string
  disabled: boolean
  onSend: (text: string) => void
}

export function Composer({ label, placeholder, disabled, onSend }: ComposerProps) {
  const [text, setText] = useState('')
  const [sending, setSending] = useState(false)
  const inputRef = useRef<HTMLTextAreaElement>(null)

  const handleSubmit = async () => {
    const trimmed = text.trim()
    if (!trimmed || disabled || sending) return
    setSending(true)
    try {
      await onSend(trimmed)
      setText('')
    } finally {
      setSending(false)
      inputRef.current?.focus()
    }
  }

  const handleKeyDown = (e: KeyboardEvent) => {
    if (e.key === 'Enter' && !e.shiftKey) {
      e.preventDefault()
      handleSubmit()
    }
  }

  return (
    <div className={styles.composer}>
      {label && <div className={styles.label}>{label}</div>}
      <div className={styles.form}>
        <textarea
          ref={inputRef}
          className={styles.input}
          value={text}
          onChange={(e) => setText(e.target.value)}
          onKeyDown={handleKeyDown}
          placeholder={placeholder}
          disabled={disabled || sending}
          rows={1}
        />
        <button
          className={styles.sendBtn}
          onClick={handleSubmit}
          disabled={disabled || sending || !text.trim()}
        >
          Send
        </button>
      </div>
    </div>
  )
}
