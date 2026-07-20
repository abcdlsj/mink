import { useEffect, useState } from "react";
import { ArrowLeft, PanelRightClose } from "lucide-react";
import { AgentWorkspace } from "./components/AgentWorkspace";
import { ComputerWorkspace } from "./components/ComputerWorkspace";
import { ConversationNavigation } from "./components/ConversationNavigation";
import { ConversationWorkspace } from "./components/ConversationWorkspace";
import { SpaceContext } from "./components/SpaceContext";
import { ThreadPane } from "./components/ThreadPane";
import {
  AgentsNavigation,
  ComputersNavigation,
} from "./components/DirectoryNavigation";
import { PrimaryRail, type WorkspaceModule } from "./components/PrimaryRail";
import { useBootstrap } from "./hooks/useBootstrap";
import { useFacts } from "./hooks/useFacts";
import { useConversation } from "./hooks/useConversation";
import { useSession } from "./hooks/useSession";
import { useSpaces } from "./hooks/useSpaces";
import type { Message, Space } from "./gen/sumi/space/v1/space_pb";
import "./styles.css";

export default function App() {
  const bootstrap = useBootstrap();
  const session = useSession();
  const [module, setModule] = useState<WorkspaceModule>("conversation");
  const [navigationOpen, setNavigationOpen] = useState(
    () => window.innerWidth >= 1280,
  );
  const [contextOpen, setContextOpen] = useState(false);
  const [compactContextOpen, setCompactContextOpen] = useState(false);
  const [selectedAgent, setSelectedAgent] = useState<string>();
  const [selectedComputer, setSelectedComputer] = useState<string>();
  const [selectedSpace, setSelectedSpace] = useState<Space>();
  const [threadRoot, setThreadRoot] = useState<Message>();
  const facts = useFacts(
    bootstrap.status === "ready" && module !== "conversation",
  );
  const human =
    session.status === "authenticated" || session.status === "logging-out"
      ? session.human
      : undefined;
  const collaborationHuman =
    module === "conversation" && bootstrap.status === "ready"
      ? human
      : undefined;
  const spaces = useSpaces(collaborationHuman?.id, !!collaborationHuman);
  const conversation = useConversation(
    collaborationHuman?.id,
    selectedSpace?.id,
    threadRoot,
  );

  useEffect(() => {
    if (!facts.data) return;
    if (!selectedAgent && facts.data.agents[0]) {
      setSelectedAgent(facts.data.agents[0].id);
    }
    if (!selectedComputer && facts.data.computers[0]) {
      setSelectedComputer(facts.data.computers[0].id);
    }
  }, [facts.data, selectedAgent, selectedComputer]);

  useEffect(() => {
    if (!spaces.data || !selectedSpace) return;
    const visible = spaces.data.spaces.find(
      (space) => space.id === selectedSpace.id,
    );
    if (!visible) {
      setSelectedSpace(undefined);
      setThreadRoot(undefined);
      setContextOpen(false);
      setCompactContextOpen(false);
    } else if (visible !== selectedSpace) {
      setSelectedSpace(visible);
    }
  }, [selectedSpace, spaces.data]);

  useEffect(() => {
    if (
      conversation.conversation.inaccessibleTargetId &&
      conversation.conversation.inaccessibleTargetId === selectedSpace?.id
    ) {
      setSelectedSpace(undefined);
      setThreadRoot(undefined);
      setContextOpen(false);
      setCompactContextOpen(false);
      return;
    }
    if (
      conversation.thread.inaccessibleTargetId &&
      conversation.thread.inaccessibleTargetId === threadRoot?.id
    ) {
      setThreadRoot(undefined);
      setContextOpen(false);
      setCompactContextOpen(false);
    }
  }, [
    conversation.conversation.inaccessibleTargetId,
    conversation.thread.inaccessibleTargetId,
    selectedSpace?.id,
    threadRoot?.id,
  ]);

  const selectModule = (next: WorkspaceModule) => {
    setModule(next);
    setContextOpen(false);
    setCompactContextOpen(false);
    if (next === "conversation") {
      setNavigationOpen(window.innerWidth >= 1280);
    } else {
      setNavigationOpen(true);
    }
  };

  const showAgent = (id: string) => {
    setSelectedAgent(id);
    if (window.innerWidth < 1024) setNavigationOpen(false);
  };

  const showComputer = (id: string) => {
    setSelectedComputer(id);
    if (window.innerWidth < 1024) setNavigationOpen(false);
  };

  const showSpace = (space: Space) => {
    setSelectedSpace(space);
    setThreadRoot(undefined);
    setContextOpen(false);
    setCompactContextOpen(false);
    if (window.innerWidth < 1280) setNavigationOpen(false);
  };

  const showThread = (message: Message) => {
    setThreadRoot(message);
    if (window.innerWidth < 1024) setCompactContextOpen(true);
    else setContextOpen(true);
  };

  const closeContext = () => {
    setThreadRoot(undefined);
    setContextOpen(false);
    setCompactContextOpen(false);
  };

  const management = module !== "conversation";
  const shellClasses = [
    "app-shell",
    navigationOpen ? "" : "navigation-collapsed",
    contextOpen && !management ? "" : "context-collapsed",
    compactContextOpen && !management ? "compact-context-open" : "",
  ]
    .filter(Boolean)
    .join(" ");

  return (
    <main className={shellClasses}>
      <PrimaryRail
        active={module}
        factsAvailable={bootstrap.status === "ready"}
        onSelect={selectModule}
      />

      <aside
        className="secondary-nav"
        aria-label={
          module === "conversation"
            ? "Conversation navigation"
            : `${capitalize(module)} navigation`
        }
      >
        {module === "conversation" ? (
          <ConversationNavigation
            bootstrap={bootstrap}
            spaces={spaces}
            currentHumanId={human?.id}
            selected={selectedSpace?.id}
            onSelect={showSpace}
            onClose={() => setNavigationOpen(false)}
          />
        ) : module === "agents" ? (
          <AgentsNavigation
            state={facts}
            selected={selectedAgent}
            onSelect={showAgent}
            onRefresh={facts.refresh}
            onClose={() => setNavigationOpen(false)}
          />
        ) : (
          <ComputersNavigation
            state={facts}
            selected={selectedComputer}
            onSelect={showComputer}
            onRefresh={facts.refresh}
            onClose={() => setNavigationOpen(false)}
          />
        )}
      </aside>

      {module === "conversation" ? (
        <ConversationWorkspace
          bootstrap={bootstrap}
          session={session}
          spaces={spaces}
          selectedSpace={selectedSpace}
          conversation={conversation}
          navigationOpen={navigationOpen}
          contextOpen={contextOpen}
          onOpenNavigation={() => setNavigationOpen(true)}
          onOpenContext={() => {
            if (window.innerWidth < 1024) setCompactContextOpen(true);
            else setContextOpen(true);
          }}
          onOpenThread={showThread}
        />
      ) : module === "agents" ? (
        <AgentWorkspace
          selected={selectedAgent}
          facts={facts}
          bootstrap={bootstrap}
          navigationOpen={navigationOpen}
          onOpenNavigation={() => setNavigationOpen(true)}
          onSelectAgent={showAgent}
        />
      ) : (
        <ComputerWorkspace
          selected={selectedComputer}
          facts={facts}
          bootstrap={bootstrap}
          navigationOpen={navigationOpen}
          onOpenNavigation={() => setNavigationOpen(true)}
        />
      )}

      {module === "conversation" && (
        <aside className="context-pane" aria-label="Context">
          {threadRoot && spaces.data ? (
            <ThreadPane
              snapshot={conversation.thread.data}
              directory={spaces.data}
              status={conversation.thread.status}
              error={conversation.thread.error}
              compact={compactContextOpen}
              sendDisabledReason={
                conversation.conversation.data?.space.archivedAt
                  ? "Archived Spaces are read-only"
                  : conversation.conversation.data?.permissions.send.status ===
                      "allowed"
                    ? undefined
                    : "Thread reply permission is unavailable"
              }
              onClose={closeContext}
              onRefresh={() => void conversation.refreshThread()}
              onLoadMore={() => void conversation.loadMoreThread()}
              onSend={conversation.sendReply}
            />
          ) : conversation.conversation.data && spaces.data && human ? (
            <SpaceContext
              conversation={conversation.conversation.data}
              directory={spaces.data}
              currentHumanId={human.id}
              onClose={closeContext}
              onAdd={conversation.addMember}
              onRemove={conversation.removeMember}
              onSetArchived={async (requestId, archived) => {
                await conversation.setArchived(requestId, archived);
                await spaces.refresh();
              }}
            />
          ) : (
            <>
              <header className="context-header">
                <button
                  className="icon-button compact-context-back"
                  type="button"
                  aria-label="Back to conversation"
                  title="Back to conversation"
                  onClick={() => setCompactContextOpen(false)}
                >
                  <ArrowLeft size={18} />
                </button>
                <div>
                  <span className="eyebrow">Current Space</span>
                  <strong>Context</strong>
                </div>
                <button
                  className="icon-button desktop-context-close"
                  type="button"
                  aria-label="Close context"
                  title="Close context"
                  onClick={() => setContextOpen(false)}
                >
                  <PanelRightClose size={18} />
                </button>
              </header>
              <div className="context-content context-empty">
                <p>Select a Space to inspect members and state.</p>
              </div>
            </>
          )}
        </aside>
      )}
    </main>
  );
}

function capitalize(value: string) {
  return value.charAt(0).toUpperCase() + value.slice(1);
}
