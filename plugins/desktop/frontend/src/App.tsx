import { useEffect, useState } from "react";
import type { ReactNode } from "react";
import { useStore } from "@/lib/store";
import { TopBar } from "@/panes/TopBar";
import { LeftPane } from "@/panes/LeftPane";
import { CenterPane } from "@/panes/CenterPane";
import { RightPane } from "@/panes/RightPane";
import { CommandPalette } from "@/components/CommandPalette";
import { QuickCreate } from "@/components/QuickCreate";
import { cn } from "@/lib/utils";

type MobilePane = "left" | "center" | "right";

export default function App() {
  const ready = useStore((s) => s.ready);
  const loadInitial = useStore((s) => s.loadInitial);
  const setPalette = useStore((s) => s.setPalette);
  const setQuickCreate = useStore((s) => s.setQuickCreate);
  const connectStream = useStore((s) => s.connectStream);
  const connectionStatus = useStore((s) => s.connectionStatus);
  const connectionMessage = useStore((s) => s.connectionMessage);
  const detail = useStore((s) => s.detail);
  const threadDetail = useStore((s) => s.threadDetail);
  const view = useStore((s) => s.view);
  const [mobilePane, setMobilePane] = useState<MobilePane>("center");

  useEffect(() => {
    void loadInitial();
  }, [loadInitial]);

  useEffect(() => {
    if (!ready) return;
    return connectStream();
  }, [ready, connectStream]);

  useEffect(() => {
    if (mobilePane === "left" && (detail || threadDetail)) {
      setMobilePane("center");
    }
  }, [detail?.item?.id, threadDetail?.parent_id, mobilePane]);

  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      const mod = e.metaKey || e.ctrlKey;
      const k = e.key.toLowerCase();
      if (mod && k === "k") {
        e.preventDefault();
        setPalette(true);
        return;
      }
      if (mod && (k === "t" || k === "n") && !e.shiftKey) {
        e.preventDefault();
        setQuickCreate(true);
        return;
      }
      if (mod && e.shiftKey && k === "t") {
        e.preventDefault();
        setQuickCreate(true);
        return;
      }
      if (e.key === "Escape") {
        setPalette(false);
        setQuickCreate(false);
      }
    };
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, [setPalette, setQuickCreate]);

  if (!ready) {
    return (
      <div className="grid h-screen place-items-center bg-bg text-text-muted text-[12.5px]">
        Loading…
      </div>
    );
  }

  const offlineBanner = connectionStatus !== "online" && (
    <div className="fixed left-1/2 top-12 z-50 max-w-[calc(100vw-24px)] -translate-x-1/2 border-hard border-border bg-panel px-3 py-1.5 font-mono text-[11.5px] text-text shadow-hard">
      {connectionStatus === "connecting" ? "Connecting to desktop backend..." : connectionMessage || "Desktop backend offline."}
    </div>
  );

  return (
    <>
      <div
        className="hidden h-screen bg-bg text-text md:grid"
        style={{
          gridTemplateColumns: "260px 1fr 320px",
          gridTemplateRows: "40px 1fr",
          gridTemplateAreas: '"topbar topbar topbar" "left center right"',
        }}
      >
        <div style={{ gridArea: "topbar" }}>
          <TopBar />
        </div>
        {offlineBanner}
        <div style={{ gridArea: "left" }} className="min-h-0">
          <LeftPane />
        </div>
        <div style={{ gridArea: "center" }} className="min-h-0">
          <CenterPane />
        </div>
        <div style={{ gridArea: "right" }} className="min-h-0">
          <RightPane />
        </div>
        <CommandPalette />
        <QuickCreate />
      </div>

      <div className="grid h-[100dvh] grid-rows-[40px_auto_1fr] bg-bg text-text md:hidden">
        <TopBar />
        {offlineBanner}
        <MobileBreadcrumbs
          active={mobilePane}
          title={mobileTitle(view, detail?.item?.title, threadDetail?.parent?.content)}
          detailsEnabled={!!detail || !!threadDetail}
          onChange={setMobilePane}
        />
        <div className="min-h-0 overflow-hidden">
          <div className={cn("h-full", mobilePane !== "left" && "hidden")}>
            <LeftPane />
          </div>
          <div className={cn("h-full", mobilePane !== "center" && "hidden")}>
            <CenterPane />
          </div>
          <div className={cn("h-full", mobilePane !== "right" && "hidden")}>
            <RightPane />
          </div>
        </div>
        <CommandPalette />
        <QuickCreate />
      </div>
    </>
  );
}

function MobileBreadcrumbs({
  active,
  title,
  detailsEnabled,
  onChange,
}: {
  active: MobilePane;
  title: string;
  detailsEnabled: boolean;
  onChange: (pane: MobilePane) => void;
}) {
  return (
    <nav className="flex items-center gap-1 border-b-hard border-border bg-panel-2 px-2 py-2">
      <Crumb active={active === "left"} onClick={() => onChange("left")}>Spaces</Crumb>
      <span className="font-mono text-[11px] text-text-faint">/</span>
      <Crumb active={active === "center"} onClick={() => onChange("center")} grow>{title}</Crumb>
      <span className="font-mono text-[11px] text-text-faint">/</span>
      <Crumb
        active={active === "right"}
        disabled={!detailsEnabled}
        onClick={() => onChange("right")}
      >
        Details
      </Crumb>
    </nav>
  );
}

function Crumb({
  active,
  disabled,
  grow,
  onClick,
  children,
}: {
  active: boolean;
  disabled?: boolean;
  grow?: boolean;
  onClick: () => void;
  children: ReactNode;
}) {
  return (
    <button
      type="button"
      disabled={disabled}
      onClick={onClick}
      className={cn(
        "min-w-0 border-2 px-2 py-1 text-left text-[12px] font-semibold transition-colors",
        grow && "flex-1",
        active ? "border-border bg-panel text-text shadow-card" : "border-transparent text-text-muted",
        !disabled && "hover:border-border hover:bg-panel hover:text-text",
        disabled && "cursor-not-allowed text-text-whisper",
      )}
    >
      <span className="block truncate">{children}</span>
    </button>
  );
}

function mobileTitle(view: string, title?: string, parent?: string): string {
  const raw = title || parent || (view === "home" ? "Home" : view);
  const compact = raw.replace(/\s+/g, " ").trim();
  if (compact.length <= 32) return compact || "Conversation";
  return compact.slice(0, 31) + "...";
}
