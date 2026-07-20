import { ArrowLeft, PanelLeftOpen, Search } from "lucide-react";
import type { ReactNode } from "react";
import type { useBootstrap } from "../hooks/useBootstrap";
import { ServerIndicator } from "./ConversationWorkspace";

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
            <button
              className="icon-button"
              type="button"
              aria-label="Open navigation"
              title="Open navigation"
              onClick={onOpenNavigation}
            >
              <PanelLeftOpen size={18} />
            </button>
          )}
          <label className="search-field">
            <Search size={17} />
            <input type="search" placeholder={`Search ${label}`} disabled />
          </label>
        </div>
        <ServerIndicator bootstrap={bootstrap} />
      </header>
      <header className="space-header management-header">
        <button
          className="icon-button management-back"
          type="button"
          aria-label={`Back to ${label} list`}
          title={`Back to ${label} list`}
          onClick={onOpenNavigation}
        >
          <ArrowLeft size={18} />
        </button>
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
