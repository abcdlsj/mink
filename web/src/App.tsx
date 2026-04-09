import { QueryClient, QueryClientProvider, useQuery } from '@tanstack/react-query'
import { fetchState, selectItem, sendMessage, runAction } from './lib/api'
import { Rail } from './components/Rail'
import { IndexPane } from './components/IndexPane'
import { CenterPane } from './components/CenterPane'
import { ContextPane } from './components/ContextPane'
import { Composer } from './components/Composer'
import styles from './App.module.css'

const queryClient = new QueryClient({
  defaultOptions: {
    queries: {
      refetchInterval: 2000,
      staleTime: 1000,
    },
  },
})

function Shell() {
  const { data: state, isLoading, error } = useQuery({
    queryKey: ['state'],
    queryFn: fetchState,
  })

  if (isLoading || !state) {
    return <div className={styles.loading}>Loading...</div>
  }

  if (error) {
    return <div className={styles.error}>Failed to connect to Mink backend</div>
  }

  const handleNavSelect = async (id: string) => {
    await selectItem(id, '')
    queryClient.invalidateQueries({ queryKey: ['state'] })
  }

  const handleIndexSelect = async (section: string, id: string) => {
    await selectItem(section, id)
    queryClient.invalidateQueries({ queryKey: ['state'] })
  }

  const handleAction = async () => {
    if (state.indexAction) {
      await runAction(state.indexAction)
      queryClient.invalidateQueries({ queryKey: ['state'] })
    }
  }

  const handleSend = async (text: string) => {
    await sendMessage(text)
    queryClient.invalidateQueries({ queryKey: ['state'] })
  }

  const hasContext = state.contextBlocks && state.contextBlocks.length > 0

  return (
    <div className={styles.shell}>
      <Rail nav={state.nav} onSelect={handleNavSelect} />
      <div className={styles.main}>
        <div className={styles.topbar}>
          <span className={styles.workspace}>{state.workspace}</span>
          <span className={styles.section}>{state.section}</span>
        </div>
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
          {hasContext && (
            <ContextPane
              title={state.contextTitle}
              blocks={state.contextBlocks!}
            />
          )}
        </div>
        <Composer
          label={state.composerLabel}
          placeholder={state.composerPlaceholder}
          disabled={state.composerDisabled}
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
