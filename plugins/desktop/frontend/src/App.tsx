import { useEffect, useRef, useState } from "react";
import type { ReactNode } from "react";
import { useStore } from "@/lib/store";
import { TopBar } from "@/panes/TopBar";
import { LeftPane } from "@/panes/LeftPane";
import { CenterPane } from "@/panes/CenterPane";
import { RightPane } from "@/panes/RightPane";
import { ThreadView } from "@/panes/ThreadView";
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
  const syncNow = useStore((s) => s.syncNow);
  const connectionStatus = useStore((s) => s.connectionStatus);
  const connectionMessage = useStore((s) => s.connectionMessage);
  const routeNotice = useStore((s) => s.routeNotice);
  const detail = useStore((s) => s.detail);
  const threadDetail = useStore((s) => s.threadDetail);
  const view = useStore((s) => s.view);
  const [mobileLayer, setMobileLayer] = useState<MobileLayer>(null);
  const [detailsDrawerOpen, setDetailsDrawerOpen] = useState(false);
  const [noticeNow, setNoticeNow] = useState(() => Date.now());
  const openedScopeRef = useRef("");
  const syncInFlightRef = useRef(false);
  const lastSyncAtRef = useRef(0);

  useEffect(() => {
    void loadInitial();
  }, [loadInitial]);

  useEffect(() => {
    if (!ready) return;
    return connectStream();
  }, [ready, connectStream]);

  useEffect(() => {
    if (!ready) return;
    const runSync = (force = false) => {
      if (syncInFlightRef.current) return;
      if (!force && document.visibilityState === "hidden") return;
      const now = Date.now();
      if (!force && now - lastSyncAtRef.current < 2500) return;
      lastSyncAtRef.current = now;
      syncInFlightRef.current = true;
      void syncNow().finally(() => {
        syncInFlightRef.current = false;
      });
    };
    const timer = window.setInterval(() => runSync(), 10000);
    const onVisible = () => {
      if (document.visibilityState === "visible") runSync(true);
    };
    const onFocus = () => runSync(true);
    document.addEventListener("visibilitychange", onVisible);
    window.addEventListener("focus", onFocus);
    return () => {
      window.clearInterval(timer);
      document.removeEventListener("visibilitychange", onVisible);
      window.removeEventListener("focus", onFocus);
    };
  }, [ready, syncNow]);

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
        setDetailsDrawerOpen(false);
      }
    };
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, [setPalette, setQuickCreate]);

  useEffect(() => {
    if (!routeNotice) return;
    setNoticeNow(Date.now());
    const t = setTimeout(() => setNoticeNow(Date.now()), 8000);
    return () => clearTimeout(t);
  }, [routeNotice]);

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
  const routeNoticeBanner = routeNotice && noticeNow - routeNotice.at < 8000 && (
    <div className="fixed left-1/2 top-12 z-50 max-w-[calc(100vw-24px)] -translate-x-1/2 border-hard border-accent-border bg-action-bg px-3 py-1.5 font-mono text-[11.5px] text-text shadow-hard">
      {routeNotice.text}
    </div>
  );
  const statusBanner = offlineBanner || routeNoticeBanner;
  const desktopHasThread = !!threadDetail;
  const desktopDetailsEnabled = !!detail || !!threadDetail;

  return (
    <>
      <div
        className="hidden h-screen bg-bg text-text md:grid"
        style={{
          gridTemplateColumns: desktopHasThread ? "260px minmax(0,1fr) minmax(460px,540px)" : "260px minmax(0,1fr)",
          gridTemplateRows: "40px 1fr",
          gridTemplateAreas: desktopHasThread
            ? '"topbar topbar topbar" "left center right"'
            : '"topbar topbar" "left center"',
        }}
      >
        <div style={{ gridArea: "topbar" }}>
          <TopBar
            detailsEnabled={desktopDetailsEnabled}
            onOpenDetails={() => setDetailsDrawerOpen(true)}
          />
        </div>
        {statusBanner}
        <div style={{ gridArea: "left" }} className="min-h-0">
          <LeftPane />
        </div>
        <div style={{ gridArea: "center" }} className="min-h-0">
          <CenterPane />
        </div>
        {desktopHasThread && (
          <div style={{ gridArea: "right" }} className="min-h-0 border-l-hard border-border bg-panel">
            <ThreadSplitPanel />
          </div>
        )}
        {detailsDrawerOpen && (
          <DetailsDrawer onClose={() => setDetailsDrawerOpen(false)}>
            <RightPane />
          </DetailsDrawer>
        )}
        <CommandPalette />
        <QuickCreate />
      </div>

      <div className="relative grid h-[100dvh] grid-rows-[40px_auto_1fr] overflow-hidden bg-bg text-text md:hidden">
        <TopBar />
        {statusBanner}
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
          <CenterPane preferThreadView />
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
          <div className="font-display text-[12px] font-extrabold uppercase text-text">Details</div>
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

function ThreadSplitPanel() {
  return (
    <section className="h-full min-w-0 bg-panel shadow-[inset_10px_0_0_rgba(0,0,0,0.02)]">
      <ThreadView />
    </section>
  );
}

function DetailsDrawer({ onClose, children }: { onClose: () => void; children: ReactNode }) {
  return (
    <div className="fixed inset-0 z-40 flex justify-end bg-border/35" onClick={onClose}>
      <div
        className="h-full w-[380px] max-w-[calc(100vw-24px)] border-l-hard border-border bg-panel-3 shadow-hard"
        onClick={(e) => e.stopPropagation()}
      >
        <div className="flex h-10 items-center justify-between border-b-hard border-border bg-panel px-3">
          <div className="font-display text-[12px] font-extrabold uppercase text-text">Details</div>
          <button
            type="button"
            className="border border-border bg-panel-2 px-2 py-0.5 text-[12px] text-text-muted hover:bg-accent hover:text-text"
            onClick={onClose}
          >
            Close
          </button>
        </div>
        <div className="h-[calc(100%-40px)] overflow-y-auto">{children}</div>
      </div>
    </div>
  );
}

function MobileDetailsContent() {
  const state = useStore((s) => s.state);
  const detail = useStore((s) => s.detail);
  const view = useStore((s) => s.view);
  const participants = useStore((s) => s.participants);
  const personas = useStore((s) => s.personas);
  const agents = useStore((s) => s.agents);
  const agentDMs = useStore((s) => s.agentDMs);
  const activeAgentSpace = useStore((s) => s.activeAgentSpace);
  const capabilities = useStore((s) => s.capabilities);
  const [open, setOpen] = useState<"skills" | "tasks" | "approvals" | "agents" | null>(null);

  const skills = capabilities?.skills || [];
  const tasks = capabilities?.tasks || [];
  const approvals = capabilities?.action_proposals || [];
  const missingSkills = skills.filter((s) => !s.configured).length;
  const failures = mobileFailureCount(tasks);
  const activeRuns = participants?.active_runs || [];
  const activePersonaID = detail?.item?.persona_id || agentDMs.find((d) => d.id === activeAgentSpace)?.persona_id || activeAgentSpace || "";
  const activePersona = view === "agent" ? personas.find((p) => p.id === activePersonaID) : undefined;
  const visibleAgents = activePersona
    ? [{
        id: activePersona.id,
        display: activePersona.display || activePersona.id,
        role: activePersona.description,
        runtime: activePersona.runtime,
        model: activePersona.model,
        status: agents.find((a) => a.id === activePersona.id)?.status || "idle",
      }]
    : (participants?.agents || []).slice(0, 6);

  return (
    <div className="flex flex-col gap-5 px-4 py-4">
      <MobileDetailSection title="Agent Workbench">
        <div className="grid grid-cols-2 gap-2 text-[12px]">
          <MobileMetric label="Working" value={activeRuns.length ? String(activeRuns.length) : "none"} active={activeRuns.length > 0} />
          <MobileMetric label="Mode" value={mobileMode(view)} />
          <MobileMetric label="Skills" value={`${skills.length - missingSkills}/${skills.length} ready`} active={missingSkills === 0 && skills.length > 0} error={missingSkills > 0} />
          <MobileMetric label="Failures" value={failures ? String(failures) : "none"} error={failures > 0} />
        </div>
        <div className="mt-3 flex flex-col gap-1.5">
          {visibleAgents.length > 0 ? visibleAgents.map((a) => {
            const persona = personas.find((p) => p.id === a.id);
            const status = activeRuns.some((r) => r.agent_id === a.id) ? "working" : a.status || "idle";
            const runtime = persona?.runtime || a.runtime || state?.runtime || "default";
            const model = persona?.model || a.model;
            return (
              <div key={a.id} className="flex items-center justify-between gap-2 border border-border bg-panel px-2 py-1.5">
                <span className="truncate text-[12.5px] text-text">@{a.display}</span>
                <span className="shrink-0 font-mono text-[10.5px] text-text-faint">
                  {status} · {runtime}{model ? " / " + model : ""}
                </span>
              </div>
            );
          }) : (
            <div className="border border-border bg-panel px-2 py-1.5 text-[12px] text-text-faint">
              Default Sumi direct · {state?.runtime || "default"}{state?.model ? " / " + state.model : ""}
            </div>
          )}
        </div>
      </MobileDetailSection>
      <MobileDetailSection title="Capability Entries">
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
          <MobileCapabilityRow
            label="Skills"
            count={skills.length}
            active={open === "skills"}
            onClick={() => setOpen(open === "skills" ? null : "skills")}
          />
          {open === "skills" && (
            <MobileCapabilityList items={skills.slice(0, 6).map((s) => [s.name, mobileSkillLine(s)])} />
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
      </MobileDetailSection>
    </div>
  );
}

function MobileMetric({ label, value, active, error }: { label: string; value: string; active?: boolean; error?: boolean }) {
  return (
    <div className="border border-border bg-panel px-2 py-1.5">
      <div className="font-mono text-[10px] uppercase text-text-faint">{label}</div>
      <div className={cn("truncate text-[12px]", error ? "text-error" : active ? "text-running" : "text-text")}>{value}</div>
    </div>
  );
}

function mobileMode(view: string): string {
  if (view === "channel") return "routed";
  if (view === "direct") return "direct";
  if (view === "agent") return "direct agent";
  if (view === "tasks") return "task board";
  return "direct";
}

function mobileSkillLine(s: { configured: boolean; risk?: string; when?: string; description?: string; missing_env?: string[] }) {
  if (!s.configured) return `missing ${s.missing_env?.length || 1}`;
  return s.risk || s.when || s.description || "ready";
}

function mobileFailureCount(tasks: { status: string; run_status?: string }[]): number {
  return tasks.filter((t) => {
    const s = (t.run_status || t.status || "").toLowerCase();
    return s === "failed" || s === "error" || s === "canceled" || s === "no_output";
  }).length;
}

function MobileDetailSection({ title, children }: { title: string; children: ReactNode }) {
  return (
    <section>
      <div className="mb-2 font-display text-[11px] font-extrabold uppercase text-text-muted">
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
        active && "bg-accent",
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
  if (view === "tasks") return "Task Board";
  const raw = title || parent || (view === "home" ? "Home" : view);
  const compact = raw.replace(/\s+/g, " ").trim();
  if (compact.length <= 32) return compact || "Conversation";
  return compact.slice(0, 31) + "...";
}
