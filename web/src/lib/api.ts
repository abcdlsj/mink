export interface NavItem {
  id: string
  label: string
  active: boolean
}

export interface IndexItem {
  id: string
  section: string
  label: string
  meta?: string
  active: boolean
}

export interface IndexGroup {
  title: string
  items: IndexItem[]
}

export interface Message {
  role: string
  sender: string
  descriptor?: string
  time?: string
  content?: string
  reasoning?: string
  toolCalls?: ToolCall[]
  toolResults?: ToolResult[]
}

export interface ToolCall {
  name: string
  args?: string
}

export interface ToolResult {
  content?: string
  error?: string
}

export interface Card {
  title: string
  subtitle?: string
  meta?: string
}

export interface ContextBlock {
  title: string
  body: string
}

export interface WebState {
  workspace: string
  section: string
  nav: NavItem[]
  indexTitle: string
  indexGroups: IndexGroup[]
  indexAction?: string
  indexActionLabel?: string
  headerTitle: string
  headerSubtitle?: string
  headerMeta?: string[]
  messages?: Message[]
  cards?: Card[]
  contextTitle?: string
  contextBlocks?: ContextBlock[]
  composerLabel: string
  composerPlaceholder: string
  composerDisabled: boolean
  emptyHint?: string
}

const BASE = ''

async function request<T>(path: string, init?: RequestInit): Promise<T> {
  const res = await fetch(`${BASE}${path}`, {
    headers: { 'Content-Type': 'application/json' },
    ...init,
  })
  if (!res.ok) throw new Error(`${res.status} ${res.statusText}`)
  return res.json()
}

export async function fetchState(): Promise<WebState> {
  return request('/api/state')
}

export function subscribeStateEvents(onStateChange: () => void, onError?: () => void): () => void {
  const es = new EventSource(`${BASE}/api/events`)
  es.addEventListener('state', () => onStateChange())
  es.onerror = () => {
    onError?.()
  }
  return () => {
    es.close()
  }
}

export async function selectItem(section: string, id: string): Promise<void> {
  await request('/api/select', {
    method: 'POST',
    body: JSON.stringify({ section, id }),
  })
}

export async function sendMessage(text: string): Promise<void> {
  await request('/api/message', {
    method: 'POST',
    body: JSON.stringify({ text }),
  })
}

export async function runAction(name: string): Promise<void> {
  await request('/api/action', {
    method: 'POST',
    body: JSON.stringify({ name }),
  })
}
