import { useEffect, useRef, useState } from "react";
import { Hash, AtSign, MessageCircle } from "lucide-react";
import { useStore } from "@/lib/store";
import { Button } from "@/components/ui/button";
import { cn } from "@/lib/utils";

type Mode = "menu" | "channel" | "agent" | "direct";

export function QuickCreate() {
  const open = useStore((s) => s.quickCreateOpen);
  const setOpen = useStore((s) => s.setQuickCreate);
  const agents = useStore((s) => s.agents);
  const createChannel = useStore((s) => s.createChannel);
  const newAgentChat = useStore((s) => s.newAgentChat);
  const newDirectChat = useStore((s) => s.newDirectChat);

  const [mode, setMode] = useState<Mode>("menu");
  const [name, setName] = useState("");
  const [query, setQuery] = useState("");
  const [idx, setIdx] = useState(0);
  const [busy, setBusy] = useState(false);
  const [err, setErr] = useState<string | null>(null);
  const [selectedAgentID, setSelectedAgentID] = useState<string | null>(null);
  const inputRef = useRef<HTMLInputElement | null>(null);

  useEffect(() => {
    if (open) {
      setMode("menu");
      setName("");
      setQuery("");
      setIdx(0);
      setErr(null);
      setBusy(false);
      setSelectedAgentID(null);
      const t = setTimeout(() => inputRef.current?.focus(), 30);
      return () => clearTimeout(t);
    }
  }, [open]);

  useEffect(() => {
    setIdx(0);
  }, [mode, query]);

  const close = () => setOpen(false);

  if (!open) return null;

  const filteredAgents = agents.filter((a) => {
    const q = query.toLowerCase();
    return q === "" || a.id.toLowerCase().includes(q) || a.display.toLowerCase().includes(q);
  });

  const menuItems = [
    { id: "channel" as Mode, icon: <Hash className="size-4 text-text" />, label: "Channel", sub: "A shared room" },
    { id: "agent" as Mode, icon: <AtSign className="size-4 text-text" />, label: "Agent Chat", sub: "Talk to an agent" },
    { id: "direct" as Mode, icon: <MessageCircle className="size-4 text-text" />, label: "Direct Message", sub: "A standalone conversation" },
  ];

  const submitChannel = async () => {
    const trimmed = name.trim();
    if (!trimmed || busy) return;
    setBusy(true);
    setErr(null);
    try {
      await createChannel(trimmed);
      close();
    } catch (e) {
      setErr(e instanceof Error ? e.message : String(e));
      setBusy(false);
    }
  };

  const submitAgent = async (id: string) => {
    if (busy) return;
    setBusy(true);
    setErr(null);
    try {
      await newAgentChat(id, name.trim());
      close();
    } catch (e) {
      setErr(e instanceof Error ? e.message : String(e));
      setBusy(false);
    }
  };

  const submitDirect = async () => {
    if (busy) return;
    setBusy(true);
    setErr(null);
    try {
      await newDirectChat(name.trim() || undefined, selectedAgentID || undefined);
      close();
    } catch (e) {
      setErr(e instanceof Error ? e.message : String(e));
      setBusy(false);
    }
  };

  const onMenuKey = (e: React.KeyboardEvent) => {
    if (e.key === "ArrowDown") {
      e.preventDefault();
      setIdx((i) => (i + 1) % menuItems.length);
    } else if (e.key === "ArrowUp") {
      e.preventDefault();
      setIdx((i) => (i - 1 + menuItems.length) % menuItems.length);
    } else if (e.key === "Enter") {
      e.preventDefault();
      setMode(menuItems[idx].id);
    }
  };

  const onAgentKey = (e: React.KeyboardEvent) => {
    if (filteredAgents.length === 0) return;
    if (e.key === "ArrowDown") {
      e.preventDefault();
      setIdx((i) => (i + 1) % filteredAgents.length);
    } else if (e.key === "ArrowUp") {
      e.preventDefault();
      setIdx((i) => (i - 1 + filteredAgents.length) % filteredAgents.length);
    } else if (e.key === "Enter") {
      e.preventDefault();
      void submitAgent(filteredAgents[idx].id);
    }
  };

  const onDirectAgentKey = (e: React.KeyboardEvent) => {
    if (filteredAgents.length === 0) return;
    if (e.key === "ArrowDown") {
      e.preventDefault();
      setIdx((i) => (i + 1) % filteredAgents.length);
    } else if (e.key === "ArrowUp") {
      e.preventDefault();
      setIdx((i) => (i - 1 + filteredAgents.length) % filteredAgents.length);
    } else if (e.key === "Enter") {
      e.preventDefault();
      const a = filteredAgents[idx];
      if (a) setSelectedAgentID((prev) => (prev === a.id ? null : a.id));
    }
  };

  return (
    <div
      className="fixed inset-0 z-50 flex items-start justify-center bg-black/35 pt-[18vh]"
      onMouseDown={(e) => {
        if (e.target === e.currentTarget) close();
      }}
      onKeyDown={(e) => {
        if (e.key === "Escape") close();
      }}
    >
      <div className="w-[480px] overflow-hidden border-hard border-border bg-panel shadow-hard">
        <div className="border-b-hard border-border bg-accent px-4 pb-2 pt-3.5">
          <div className="font-display text-[13px] font-extrabold uppercase text-text">Create</div>
          <div className="mt-0.5 font-mono text-[11.5px] text-text-muted">
            {mode === "menu" && "Pick what you want to start."}
            {mode === "channel" && "Letters, numbers, dashes."}
            {mode === "agent" && "Pick an agent; title is optional."}
            {mode === "direct" && "Title and agent are optional."}
          </div>
        </div>

        {mode === "menu" && (
          <ul className="py-1" onKeyDown={onMenuKey}>
            <input
              ref={inputRef}
              className="absolute opacity-0 pointer-events-none"
              autoFocus
              onKeyDown={onMenuKey}
            />
            {menuItems.map((it, i) => (
              <li key={it.id}>
                <button
                  className={cn(
                    "flex w-full cursor-pointer items-center gap-3 border-y border-transparent px-4 py-2 text-left",
                    i === idx ? "border-border bg-panel-2" : "hover:border-border hover:bg-panel-2",
                  )}
                  onMouseEnter={() => setIdx(i)}
                  onClick={() => setMode(it.id)}
                >
                  {it.icon}
                  <span className="flex-1">
                    <span className="block text-[13px] font-semibold text-text">{it.label}</span>
                    <span className="text-[11.5px] text-text-faint">{it.sub}</span>
                  </span>
                </button>
              </li>
            ))}
          </ul>
        )}

        {mode === "channel" && (
          <form
            className="px-4 py-3.5"
            onSubmit={(e) => {
              e.preventDefault();
              void submitChannel();
            }}
          >
            <div className="relative">
              <span className="absolute left-2.5 top-1/2 -translate-y-1/2 text-text-faint text-[13px]">#</span>
              <input
                ref={inputRef}
                value={name}
                onChange={(e) => {
                  setName(e.target.value);
                  if (err) setErr(null);
                }}
                placeholder="research"
                disabled={busy}
                autoFocus
                className="w-full border-hard border-border bg-bg py-2 pl-6 pr-3 text-[13.5px] text-text outline-none transition-[box-shadow] hover:shadow-card focus:shadow-card disabled:opacity-70"
              />
            </div>
            {err && <div className="mt-2 text-[12px] text-error">{err}</div>}
            <div className="mt-3.5 flex justify-end gap-2">
              <Button variant="default" type="button" onClick={() => setMode("menu")} disabled={busy}>
                Back
              </Button>
              <Button variant="primary" type="submit" disabled={busy || !name.trim()}>
                {busy ? "Creating…" : "Create"}
              </Button>
            </div>
          </form>
        )}

        {mode === "agent" && (
          <div onKeyDown={onAgentKey}>
            <input
              ref={inputRef}
              value={query}
              onChange={(e) => setQuery(e.target.value)}
              placeholder="Search agents…"
              autoFocus
              className="w-full border-b-hard border-border bg-bg px-4 py-2.5 text-[13.5px] text-text outline-none"
            />
            <input
              value={name}
              onChange={(e) => setName(e.target.value)}
              placeholder="Chat title (optional)"
              className="w-full border-b-hard border-border bg-panel px-4 py-2 text-[13px] text-text outline-none"
            />
            {err && <div className="px-4 py-2 text-[12px] text-error">{err}</div>}
            <ul className="py-1 max-h-[300px] overflow-y-auto">
              {filteredAgents.length === 0 ? (
                <li className="px-4 py-2.5 text-[12px] text-text-faint">No matching agent.</li>
              ) : (
                filteredAgents.map((a, i) => (
                  <li key={a.id}>
                    <button
                      className={cn(
                        "flex w-full cursor-pointer items-center gap-2 border-y border-transparent px-4 py-2 text-left",
                        i === idx ? "border-border bg-panel-2" : "hover:border-border hover:bg-panel-2",
                      )}
                      onMouseEnter={() => setIdx(i)}
                      onClick={() => void submitAgent(a.id)}
                    >
                      <AtSign className="size-3 text-text-faint" />
                      <span className="text-[13px] text-text">{a.display}</span>
                      {a.role && <span className="ml-auto text-[11.5px] text-text-faint truncate max-w-[60%]">{a.role}</span>}
                    </button>
                  </li>
                ))
              )}
            </ul>
            <div className="flex justify-end gap-2 border-t-hard border-border px-4 py-2">
              <Button variant="default" type="button" onClick={() => setMode("menu")} disabled={busy}>
                Back
              </Button>
            </div>
          </div>
        )}

        {mode === "direct" && (
          <div>
            <input
              ref={inputRef}
              value={name}
              onChange={(e) => setName(e.target.value)}
              placeholder="Chat title (optional)"
              autoFocus
              className="w-full border-b-hard border-border bg-bg px-4 py-2.5 text-[13.5px] text-text outline-none"
            />
            <div onKeyDown={onDirectAgentKey}>
              <input
                value={query}
                onChange={(e) => setQuery(e.target.value)}
                placeholder="Search agents… (optional)"
                className="w-full border-b-hard border-border bg-panel px-4 py-2 text-[13px] text-text outline-none"
              />
              <ul className="py-1 max-h-[220px] overflow-y-auto">
                {filteredAgents.length === 0 ? (
                  <li className="px-4 py-2.5 text-[12px] text-text-faint">No matching agent.</li>
                ) : (
                  filteredAgents.map((a, i) => (
                    <li key={a.id}>
                      <button
                        className={cn(
                          "flex w-full cursor-pointer items-center gap-2 border-y border-transparent px-4 py-2 text-left",
                          selectedAgentID === a.id
                            ? "border-border bg-panel-2 text-text"
                            : i === idx
                              ? "border-border bg-panel-2"
                              : "hover:border-border hover:bg-panel-2",
                        )}
                        onMouseEnter={() => setIdx(i)}
                        onClick={() => setSelectedAgentID((prev) => (prev === a.id ? null : a.id))}
                      >
                        <AtSign className="size-3 text-text-faint" />
                        <span className="text-[13px] text-text">{a.display}</span>
                        {a.role && <span className="ml-auto text-[11.5px] text-text-faint truncate max-w-[60%]">{a.role}</span>}
                        {selectedAgentID === a.id && (
                          <span className="ml-1 shrink-0 font-mono text-[10.5px] text-text-muted">selected</span>
                        )}
                      </button>
                    </li>
                  ))
                )}
              </ul>
            </div>
            {err && <div className="px-4 py-2 text-[12px] text-error">{err}</div>}
            <div className="flex justify-end gap-2 border-t-hard border-border px-4 py-2">
              <Button variant="default" type="button" onClick={() => setMode("menu")} disabled={busy}>
                Back
              </Button>
              <Button variant="primary" disabled={busy} onClick={() => void submitDirect()}>
                {busy ? "Creating…" : "Create"}
              </Button>
            </div>
          </div>
        )}
      </div>
    </div>
  );
}

