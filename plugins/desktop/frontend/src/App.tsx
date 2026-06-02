import { useEffect } from "react";
import { useStore } from "@/lib/store";
import { TopBar } from "@/panes/TopBar";
import { LeftPane } from "@/panes/LeftPane";
import { CenterPane } from "@/panes/CenterPane";
import { RightPane } from "@/panes/RightPane";
import { CommandPalette } from "@/components/CommandPalette";
import { QuickCreate } from "@/components/QuickCreate";

export default function App() {
  const ready = useStore((s) => s.ready);
  const loadInitial = useStore((s) => s.loadInitial);
  const setPalette = useStore((s) => s.setPalette);
  const setQuickCreate = useStore((s) => s.setQuickCreate);
  const connectStream = useStore((s) => s.connectStream);

  useEffect(() => {
    void loadInitial();
  }, [loadInitial]);

  useEffect(() => {
    if (!ready) return;
    return connectStream();
  }, [ready, connectStream]);

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

  return (
    <div
      className="grid h-screen bg-bg text-text"
      style={{
        gridTemplateColumns: "260px 1fr 320px",
        gridTemplateRows: "40px 1fr",
        gridTemplateAreas: '"topbar topbar topbar" "left center right"',
      }}
    >
      <div style={{ gridArea: "topbar" }}>
        <TopBar />
      </div>
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
  );
}
