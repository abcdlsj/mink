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
  const inputRef = useRef<HTMLInputElement | null>(null);

  useEffect(() => {
    if (open) {
      setMode("menu");
      setName("");
      setQuery("");
      setIdx(0);
      setErr(null);
      setBusy(false);
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
    { id: "channel" as Mode, icon: <Hash className="size-4 text-text-faint" />, label: "Channel", sub: "A shared room" },
    { id: "agent" as Mode, icon: <AtSign className="size-4 text-text-faint" />, label: "Agent Chat", sub: "Talk to an agent" },
    { id: "direct" as Mode, icon: <MessageCircle className="size-4 text-text-faint" />, label: "Direct Message", sub: "A standalone conversation" },
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
      await newAgentChat(id);
      close();
    } catch (e) {
      setErr(e instanceof Error ? e.message : String(e));
      setBusy(false);
    }
  };

  const submitDirect = async () => {
    if (busy) return;
    setBusy(true);
    try {
      await newDirectChat();
      close();
    } finally {
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
      const next = menuItems[idx].id;
      if (next === "direct") void submitDirect();
      else setMode(next);
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

  return (
    <div
      className="fixed inset-0 z-50 flex items-start justify-center bg-black/30 pt-[18vh]"
      onMouseDown={(e) => {
        if (e.target === e.currentTarget) close();
      }}
      onKeyDown={(e) => {
        if (e.key === "Escape") close();
      }}
    >
      <div className="w-[480px] rounded-lg border border-border bg-panel shadow-[0_24px_48px_-12px_rgba(31,41,51,0.18)] overflow-hidden">
        <div className="px-4 pt-3.5 pb-2 border-b border-border-soft">
          <div className="text-[13px] font-display font-semibold text-text">Create</div>
          <div className="text-[11.5px] text-text-faint mt-0.5">
            {mode === "menu" && "Pick what you want to start."}
            {mode === "channel" && "Letters, numbers, dashes."}
            {mode === "agent" && "Type to filter agents."}
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
                    "w-full text-left px-4 py-2 flex items-center gap-3 cursor-pointer",
                    i === idx ? "bg-panel-2" : "hover:bg-panel-2",
                  )}
                  onMouseEnter={() => setIdx(i)}
                  onClick={() => {
                    if (it.id === "direct") void submitDirect();
                    else setMode(it.id);
                  }}
                >
                  {it.icon}
                  <span className="flex-1">
                    <span className="text-[13px] text-text block">{it.label}</span>
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
                className="w-full rounded-md border border-border bg-bg pl-6 pr-3 py-2 text-[13.5px] text-text outline-none transition-[border,box-shadow] hover:border-border-strong focus:border-accent focus:ring-[3px] focus:ring-accent-bg disabled:opacity-70"
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
              className="w-full px-4 py-2.5 text-[13.5px] text-text outline-none border-b border-border-soft bg-transparent"
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
                        "w-full text-left px-4 py-2 flex items-center gap-2 cursor-pointer",
                        i === idx ? "bg-panel-2" : "hover:bg-panel-2",
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
            <div className="px-4 py-2 border-t border-border-soft flex justify-end gap-2">
              <Button variant="default" type="button" onClick={() => setMode("menu")} disabled={busy}>
                Back
              </Button>
            </div>
          </div>
        )}
      </div>
    </div>
  );
}
