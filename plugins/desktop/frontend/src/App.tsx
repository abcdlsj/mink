import { useEffect, useRef, useState } from "react";
import type { ReactNode } from "react";
import { useStore } from "@/lib/store";
import { TopBar } from "@/panes/TopBar";
import { LeftPane } from "@/panes/LeftPane";
import { CenterPane } from "@/panes/CenterPane";
import { RightPane } from "@/panes/RightPane";
import { CommandPalette } from "@/components/CommandPalette";
import { QuickCreate } from "@/components/QuickCreate";
import { cn } from "@/lib/utils";

type MobileLayer = "spaces" | "details" | null;

export default function App() {
  const ready = useStore((s) => s.ready);
  const loadInitial = useStore((s) => s.loadInitial);
  const setPalette = useStore((s) => s.setPalette);
  const setQuickCreate = useStore((s) => s.setQuickCreate);
  const connectStream = useStore((s) => s.connectStream);
  const openCurrentRoute = useStore((s) => s.openCurrentRoute);
  const connectionStatus = useStore((s) => s.connectionStatus);
  const connectionMessage = useStore((s) => s.connectionMessage);
  const detail = useStore((s) => s.detail);
  const threadDetail = useStore((s) => s.threadDetail);
  const view = useStore((s) => s.view);
  const [mobileLayer, setMobileLayer] = useState<MobileLayer>(null);
  const openedScopeRef = useRef("");

  useEffect(() => {
    void loadInitial();
  }, [loadInitial]);

  useEffect(() => {
    if (!ready) return;
    return connectStream();
  }, [ready, connectStream]);

  useEffect(() => {
    const onPopState = () => {
      void openCurrentRoute().catch(() => undefined);
      setMobileLayer(null);
    };
    window.addEventListener("popstate", onPopState);
    return () => window.removeEventListener("popstate", onPopState);
  }, [openCurrentRoute]);

  const mobileScope = `${detail?.item?.id || ""}:${threadDetail?.parent_id || ""}`;

  useEffect(() => {
    if (mobileLayer === "spaces" && openedScopeRef.current && openedScopeRef.current !== mobileScope) {
      setMobileLayer(null);
    }
  }, [mobileScope, mobileLayer]);

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

      <div className="relative grid h-[100dvh] grid-rows-[40px_auto_1fr] overflow-hidden bg-bg text-text md:hidden">
        <TopBar />
        {offlineBanner}
        <MobileNav
          title={mobileTitle(view, detail?.item?.title, threadDetail?.parent?.content)}
          detailsEnabled={!!detail || !!threadDetail}
          onOpenSpaces={() => {
            openedScopeRef.current = mobileScope;
            setMobileLayer("spaces");
          }}
          onOpenDetails={() => setMobileLayer("details")}
        />
        <div className="min-h-0 overflow-hidden">
          <CenterPane />
        </div>
        {mobileLayer === "spaces" && (
          <MobileOverlay onClose={() => setMobileLayer(null)}>
            <div className="h-full w-[86vw] max-w-[340px] border-r-hard border-border bg-panel-2 shadow-hard">
              <LeftPane />
            </div>
          </MobileOverlay>
        )}
        {mobileLayer === "details" && (
          <MobileSheet onClose={() => setMobileLayer(null)}>
            <MobileDetailsContent />
          </MobileSheet>
        )}
        <CommandPalette />
        <QuickCreate />
      </div>
    </>
  );
}

function MobileNav({
  title,
  detailsEnabled,
  onOpenSpaces,
  onOpenDetails,
}: {
  title: string;
  detailsEnabled: boolean;
  onOpenSpaces: () => void;
  onOpenDetails: () => void;
}) {
  return (
    <nav className="grid grid-cols-[auto_1fr_auto] items-center gap-2 border-b-hard border-border bg-panel-2 px-2 py-2">
      <Crumb onClick={onOpenSpaces}>Spaces</Crumb>
      <div className="min-w-0 border-2 border-border bg-panel px-2 py-1 shadow-card">
        <div className="flex min-w-0 items-center gap-1 font-semibold text-[12px] text-text">
          <span className="shrink-0 font-mono text-[11px] text-text-faint">/</span>
          <span className="truncate">{title}</span>
        </div>
      </div>
      <Crumb
        disabled={!detailsEnabled}
        onClick={onOpenDetails}
      >
        Details
      </Crumb>
    </nav>
  );
}

function Crumb({
  disabled,
  onClick,
  children,
}: {
  disabled?: boolean;
  onClick: () => void;
  children: ReactNode;
}) {
  return (
    <button
      type="button"
      disabled={disabled}
      onClick={onClick}
      className={cn(
        "min-w-0 border-2 border-border bg-panel px-2 py-1 text-left text-[12px] font-semibold text-text-muted shadow-card transition-colors",
        !disabled && "hover:border-border hover:bg-panel hover:text-text",
        disabled && "cursor-not-allowed text-text-whisper",
      )}
    >
      <span className="block truncate">{children}</span>
    </button>
  );
}

function MobileOverlay({ onClose, children }: { onClose: () => void; children: ReactNode }) {
  return (
    <div className="fixed inset-x-0 bottom-0 top-10 z-40 flex bg-border/35" onClick={onClose}>
      <div onClick={(e) => e.stopPropagation()}>{children}</div>
    </div>
  );
}

function MobileSheet({ onClose, children }: { onClose: () => void; children: ReactNode }) {
  return (
    <div className="fixed inset-0 z-40 flex items-end bg-border/35" onClick={onClose}>
      <div
        className="max-h-[78dvh] w-full overflow-hidden border-t-hard border-border bg-panel-3 shadow-hard"
        onClick={(e) => e.stopPropagation()}
      >
        <div className="flex items-center justify-between border-b border-border bg-panel px-3 py-2">
          <div className="font-display text-[12px] font-black uppercase tracking-[0.6px] text-text">Details</div>
          <button
            type="button"
            className="border border-border bg-panel-2 px-2 py-0.5 text-[12px] text-text-muted"
            onClick={onClose}
          >
            Close
          </button>
        </div>
        <div className="h-[calc(78dvh-39px)] overflow-y-auto">{children}</div>
      </div>
    </div>
  );
}

function MobileDetailsContent() {
  const state = useStore((s) => s.state);
  const participants = useStore((s) => s.participants);
  const tools = useStore((s) => s.tools);
  const personas = useStore((s) => s.personas);
  const agents = useStore((s) => s.agents);
  const agentDMs = useStore((s) => s.agentDMs);
  const capabilities = useStore((s) => s.capabilities);
  const [open, setOpen] = useState<"skills" | "tasks" | "approvals" | "agents" | null>(null);

  const skills = capabilities?.skills || [];
  const tasks = capabilities?.tasks || [];
  const approvals = capabilities?.action_proposals || [];

  return (
    <div className="flex flex-col gap-5 px-4 py-4">
      <MobileDetailSection title="Runtime">
        <div className="grid grid-cols-[72px_1fr] gap-y-1 font-mono text-[12px]">
          <span className="text-text-faint">Runtime</span>
          <span className="truncate text-text">{state?.runtime || "default"}</span>
          <span className="text-text-faint">Model</span>
          <span className="truncate text-text">{state?.model || "default"}</span>
        </div>
      </MobileDetailSection>
      <MobileDetailSection title="People">
        <div className="text-[12.5px] text-text-muted">
          {(participants?.agents.length || 0)} participants
          {(participants?.active_runs?.length || 0) > 0 && ` · ${participants?.active_runs?.length} running`}
        </div>
      </MobileDetailSection>
      <MobileDetailSection title="Agent Directory">
        <MobileCapabilityRow
          label="Defined agents"
          count={personas.length}
          active={open === "agents"}
          onClick={() => setOpen(open === "agents" ? null : "agents")}
        />
        {open === "agents" && (
          <MobileCapabilityList
            items={personas.slice(0, 8).map((p) => {
              const status = agents.find((a) => a.id === p.id)?.status || "idle";
              const hasDM = agentDMs.some((d) => d.persona_id === p.id);
              const runtime = p.runtime || state?.runtime || "default";
              const model = p.model;
              return [
                "@" + (p.display || p.id),
                `${status}${hasDM ? " · dm" : " · ready"} · ${runtime}${model ? " / " + model : ""}`,
              ];
            })}
          />
        )}
      </MobileDetailSection>
      <MobileDetailSection title="Capabilities">
        <div className="flex flex-col gap-2">
          <MobileCapabilityRow
            label="Skills"
            count={skills.length}
            active={open === "skills"}
            onClick={() => setOpen(open === "skills" ? null : "skills")}
          />
          {open === "skills" && (
            <MobileCapabilityList items={skills.slice(0, 6).map((s) => [s.name, s.risk || s.when || s.description || "skill"])} />
          )}
          <MobileCapabilityRow
            label="Tasks"
            count={tasks.length}
            active={open === "tasks"}
            onClick={() => setOpen(open === "tasks" ? null : "tasks")}
          />
          {open === "tasks" && (
            <MobileCapabilityList items={tasks.slice(0, 6).map((t) => [t.title, t.state?.checkpoint || t.status])} />
          )}
          <MobileCapabilityRow
            label="Approvals"
            count={approvals.length}
            active={open === "approvals"}
            onClick={() => setOpen(open === "approvals" ? null : "approvals")}
          />
          {open === "approvals" && (
            <MobileCapabilityList items={approvals.slice(0, 6).map((a) => [a.tool || "action", a.intent || a.risk || a.result || "proposal"])} />
          )}
        </div>
      </MobileDetailSection>
      <MobileDetailSection title="Tools">
        <div className="flex flex-wrap gap-1.5">
          {tools.length > 0 ? tools.slice(0, 12).map((t) => (
            <span key={t.name} className="border border-border bg-panel px-1.5 py-px font-mono text-[11px] text-text-muted">
              {t.name}
            </span>
          )) : (
            <span className="text-[12px] text-text-faint">No tools enabled.</span>
          )}
        </div>
      </MobileDetailSection>
    </div>
  );
}

function MobileDetailSection({ title, children }: { title: string; children: ReactNode }) {
  return (
    <section>
      <div className="mb-2 font-display text-[10px] font-black uppercase tracking-[1px] text-text-muted">
        {title}
      </div>
      {children}
    </section>
  );
}

function MobileCapabilityRow({
  label,
  count,
  active,
  onClick,
}: {
  label: string;
  count: number;
  active: boolean;
  onClick: () => void;
}) {
  return (
    <button
      type="button"
      onClick={onClick}
      className={cn(
        "flex w-full items-center justify-between border-2 border-border bg-panel px-2.5 py-2 text-left text-[12.5px] shadow-card",
        active && "bg-accent-bg",
      )}
    >
      <span className="font-semibold text-text">{label}</span>
      <span className="font-mono text-text-muted">{count}</span>
    </button>
  );
}

function MobileCapabilityList({ items }: { items: [string, string][] }) {
  if (items.length === 0) {
    return <div className="px-2 text-[12px] text-text-faint">No records.</div>;
  }
  return (
    <div className="flex flex-col gap-1 border-x border-b border-border bg-panel px-2 py-2">
      {items.map(([title, meta], i) => (
        <div key={`${title}-${i}`} className="min-w-0 text-[12px] leading-[1.45]">
          <div className="truncate font-semibold text-text">{title}</div>
          <div className="line-clamp-2 text-text-muted">{meta}</div>
        </div>
      ))}
    </div>
  );
}

function mobileTitle(view: string, title?: string, parent?: string): string {
  const raw = title || parent || (view === "home" ? "Home" : view);
  const compact = raw.replace(/\s+/g, " ").trim();
  if (compact.length <= 32) return compact || "Conversation";
  return compact.slice(0, 31) + "...";
}
