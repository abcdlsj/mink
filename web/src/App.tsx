import { useEffect, useState } from "react";
import { ArrowLeft, PanelRightClose } from "lucide-react";
import { AgentWorkspace } from "./components/AgentWorkspace";
import { ComputerWorkspace } from "./components/ComputerWorkspace";
import { ConversationNavigation } from "./components/ConversationNavigation";
import { ConversationWorkspace } from "./components/ConversationWorkspace";
import {
  AgentsNavigation,
  ComputersNavigation,
} from "./components/DirectoryNavigation";
import { PrimaryRail, type WorkspaceModule } from "./components/PrimaryRail";
import { useBootstrap } from "./hooks/useBootstrap";
import { useFacts } from "./hooks/useFacts";
import "./styles.css";

export default function App() {
  const bootstrap = useBootstrap();
  const [module, setModule] = useState<WorkspaceModule>("conversation");
  const [navigationOpen, setNavigationOpen] = useState(
    () => window.innerWidth >= 1280,
  );
  const [contextOpen, setContextOpen] = useState(false);
  const [compactContextOpen, setCompactContextOpen] = useState(false);
  const [selectedAgent, setSelectedAgent] = useState<string>();
  const [selectedComputer, setSelectedComputer] = useState<string>();
  const facts = useFacts(
    bootstrap.status === "ready" && module !== "conversation",
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
            onClose={() => setNavigationOpen(false)}
          />
        ) : module === "agents" ? (
          <AgentsNavigation
            state={facts}
            selected={selectedAgent}
            onSelect={showAgent}
            onCreate={() => showAgent("create")}
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
          navigationOpen={navigationOpen}
          contextOpen={contextOpen}
          onOpenNavigation={() => setNavigationOpen(true)}
          onOpenContext={() => {
            if (window.innerWidth < 1024) setCompactContextOpen(true);
            else setContextOpen(true);
          }}
        />
      ) : module === "agents" ? (
        <AgentWorkspace
          selected={selectedAgent}
          facts={facts}
          bootstrap={bootstrap}
          navigationOpen={navigationOpen}
          onOpenNavigation={() => setNavigationOpen(true)}
          onSelectAgent={showAgent}
          onCancelCreate={() => {
            const first = facts.data?.agents[0];
            if (first) showAgent(first.id);
            else {
              setSelectedAgent(undefined);
              setNavigationOpen(true);
            }
          }}
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
              <span className="eyebrow">Current space</span>
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
            <p>No context selected</p>
          </div>
        </aside>
      )}
    </main>
  );
}

function capitalize(value: string) {
  return value.charAt(0).toUpperCase() + value.slice(1);
}
