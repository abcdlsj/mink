import { ArrowLeft, PanelLeftOpen } from "lucide-react";
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
      <header className="topbar">
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
          <span className="topbar-product">Sumi</span>
        </div>
        <ServerIndicator bootstrap={bootstrap} />
      </header>
      <header className="space-header management-header">
        <IconButton
          className="management-back"
          label={`Back to ${label} list`}
          tooltipPlacement="right"
          onClick={onOpenNavigation}
        >
          <ArrowLeft size={18} />
        </IconButton>
        <div>
          <span className="eyebrow">{label}</span>
          <h1>{title}</h1>
        </div>
        <p>{summary}</p>
      </header>
      <div className="management-content">{children}</div>
    </section>
  );
}
