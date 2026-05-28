import { useEffect } from "react";
import { useStore } from "@/lib/store";
import { TopBar } from "@/panes/TopBar";
import { LeftPane } from "@/panes/LeftPane";
import { CenterPane } from "@/panes/CenterPane";
import { RightPane } from "@/panes/RightPane";

export default function App() {
  const ready = useStore((s) => s.ready);
  const loadInitial = useStore((s) => s.loadInitial);
  const setPalette = useStore((s) => s.setPalette);
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
      if ((e.metaKey || e.ctrlKey) && e.key.toLowerCase() === "k") {
        e.preventDefault();
        setPalette(true);
      }
      if (e.key === "Escape") setPalette(false);
    };
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, [setPalette]);

  if (!ready) {
    return (
      <div className="grid h-screen place-items-center text-text-faint text-[12.5px]">
        Loading…
      </div>
    );
  }

  return (
    <div
      className="grid h-screen"
      style={{
        gridTemplateColumns: "248px 1fr 320px",
        gridTemplateRows: "36px 1fr",
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
    </div>
  );
}
