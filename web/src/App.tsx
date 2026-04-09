import { startTransition, useEffect, useState } from 'react'
import { QueryClient, QueryClientProvider, useQuery } from '@tanstack/react-query'
import { fetchState, runAction, selectItem, sendMessage, subscribeStateEvents } from './lib/api'
import { Rail } from './components/Rail'
import { IndexPane } from './components/IndexPane'
import { CenterPane } from './components/CenterPane'
import { ContextPane } from './components/ContextPane'
import { Composer } from './components/Composer'
import styles from './App.module.css'

const queryClient = new QueryClient({
  defaultOptions: {
    queries: {
      staleTime: 1000,
      retry: 1,
    },
  },
})

function Shell() {
  const [actionError, setActionError] = useState('')
  const [connectionMode, setConnectionMode] = useState<'live' | 'polling'>('polling')
  const [pendingAction, setPendingAction] = useState('')

  const { data: state, isLoading, error } = useQuery({
    queryKey: ['state'],
    queryFn: fetchState,
  })

  useEffect(() => {
    const refetchState = () => {
      startTransition(() => {
        void queryClient.invalidateQueries({ queryKey: ['state'] })
      })
    }

    const unsubscribe = subscribeStateEvents(
      () => {
        setConnectionMode('live')
        refetchState()
      },
      () => {
        setConnectionMode('polling')
      },
    )

    return unsubscribe
  }, [])

  useEffect(() => {
    if (connectionMode === 'live') {
      return
    }
    const timer = window.setInterval(() => {
      void queryClient.invalidateQueries({ queryKey: ['state'] })
    }, 5000)
    return () => window.clearInterval(timer)
  }, [connectionMode])

  const refreshState = async () => {
    await queryClient.refetchQueries({ queryKey: ['state'], type: 'active' })
  }

  const runWithFeedback = async (label: string, fn: () => Promise<void>) => {
    setActionError('')
    setPendingAction(label)
    try {
      await fn()
      await refreshState()
    } catch (err) {
      setActionError(err instanceof Error ? err.message : `${label} failed`)
    } finally {
      setPendingAction('')
    }
  }

  if (isLoading && !state) {
    return <div className={styles.loading}>Loading...</div>
  }

  if (error && !state) {
    return (
      <div className={styles.error}>
        <span>Failed to connect to Mink backend</span>
        <button
          className={styles.retryBtn}
          onClick={() => {
            void queryClient.invalidateQueries({ queryKey: ['state'] })
          }}
        >
          Retry
        </button>
      </div>
    )
  }

  if (!state) {
    return <div className={styles.loading}>Loading...</div>
  }

  const handleNavSelect = async (section: string) => {
    await runWithFeedback(`open ${section}`, async () => {
      await selectItem(section, '')
    })
  }

  const handleIndexSelect = async (section: string, id: string) => {
    await runWithFeedback(`open ${id || section}`, async () => {
      await selectItem(section, id)
    })
  }

  const handleAction = async () => {
    if (state.indexAction) {
      await runWithFeedback(state.indexAction, async () => {
        await runAction(state.indexAction!)
      })
    }
  }

  const handleSend = async (text: string) => {
    await runWithFeedback('send message', async () => {
      await sendMessage(text)
    })
  }

  const hasContext = !!(state.contextBlocks && state.contextBlocks.length > 0)

  return (
    <div className={styles.shell}>
      <Rail nav={state.nav} onSelect={handleNavSelect} />
      <div className={styles.main}>
        <div className={styles.topbar}>
          <span className={styles.workspace}>{state.workspace}</span>
          <span className={styles.section}>
            {state.section}
            <span className={styles.connectionTag}>{connectionMode}</span>
          </span>
        </div>
        {actionError && <div className={styles.noticeError}>{actionError}</div>}
        {pendingAction && <div className={styles.noticeInfo}>{pendingAction}...</div>}
        <div className={styles.body}>
          <IndexPane
            title={state.indexTitle}
            groups={state.indexGroups}
            actionLabel={state.indexActionLabel}
            onAction={handleAction}
            onSelect={handleIndexSelect}
          />
          <CenterPane
            headerTitle={state.headerTitle}
            headerSubtitle={state.headerSubtitle}
            headerMeta={state.headerMeta}
            messages={state.messages}
            cards={state.cards}
            emptyHint={state.emptyHint}
          />
          {hasContext && <ContextPane title={state.contextTitle} blocks={state.contextBlocks!} />}
        </div>
        <Composer
          label={state.composerLabel}
          placeholder={state.composerPlaceholder}
          disabled={state.composerDisabled || pendingAction !== ''}
          onSend={handleSend}
        />
      </div>
    </div>
  )
}

export default function App() {
  return (
    <QueryClientProvider client={queryClient}>
      <Shell />
    </QueryClientProvider>
  )
}
