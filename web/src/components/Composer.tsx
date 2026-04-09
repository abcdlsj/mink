import { useState, useRef, useCallback, type KeyboardEvent, type ChangeEvent } from 'react'
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

  const autoResize = useCallback(() => {
    const el = inputRef.current
    if (!el) return
    el.style.height = 'auto'
    el.style.height = Math.min(el.scrollHeight, 120) + 'px'
  }, [])

  const handleChange = (e: ChangeEvent<HTMLTextAreaElement>) => {
    setText(e.target.value)
    autoResize()
  }

  const handleSubmit = async () => {
    const trimmed = text.trim()
    if (!trimmed || disabled || sending) return
    setSending(true)
    try {
      await onSend(trimmed)
      setText('')
      if (inputRef.current) {
        inputRef.current.style.height = 'auto'
      }
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
          onChange={handleChange}
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
