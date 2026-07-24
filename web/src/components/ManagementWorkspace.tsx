import { PanelLeftOpen } from "lucide-react";
import type { ReactNode } from "react";
import type { useBootstrap } from "../hooks/useBootstrap";
import { ServerIndicator } from "./ConversationWorkspace";
import { IconButton } from "./IconButton";

type Bootstrap = ReturnType<typeof useBootstrap>;

export function ManagementWorkspace({
  label,
  title,
  summary,
  bootstrap,
  navigationOpen,
  children,
  onOpenNavigation,
}: {
  label: string;
  title: string;
  summary: string;
  bootstrap: Bootstrap;
  navigationOpen: boolean;
  children: ReactNode;
  onOpenNavigation: () => void;
}) {
  return (
    <section
      className="conversation workspace-main management"
      aria-label={label}
    >
      <header className="workspace-header management-workspace-header">
        <div className="space-header management-header workspace-heading">
          <div className="topbar-leading">
            {!navigationOpen && (
              <IconButton
                label="Open navigation"
                tooltipPlacement="right"
                onClick={onOpenNavigation}
              >
                <PanelLeftOpen size={18} />
              </IconButton>
            )}
            <h1>{title}</h1>
          </div>
          <p>{summary}</p>
          <ServerIndicator bootstrap={bootstrap} />
        </div>
      </header>
      <div className="management-content">{children}</div>
    </section>
  );
}
