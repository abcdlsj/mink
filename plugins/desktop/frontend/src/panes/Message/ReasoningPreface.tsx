import { useState } from "react";
import { Markdown } from "@/components/Markdown";

export function ReasoningPreface({ text }: { text: string }) {
  const [open, setOpen] = useState(false);
  const flat = text.replace(/\s+/g, " ").trim();
  const isLong = flat.length > 280;
  const collapsed = isLong ? flat.slice(0, 280) + "…" : flat;
  return (
    <div className="mb-2 max-w-[66ch] rounded-[2px] bg-panel/50 px-2 py-1 text-[11.5px] leading-[1.5] text-text-faint">
      <span className="mr-1 font-mono text-[10px] uppercase text-text-whisper">thinking</span>
      {open ? (
        <Markdown variant="lite" className="whitespace-pre-wrap">
          {text}
        </Markdown>
      ) : (
        <span className="whitespace-pre-wrap">{collapsed}</span>
      )}
      {isLong && (
        <>
          {" "}
          <button
            onClick={() => setOpen((v) => !v)}
            className="cursor-pointer text-[11px] text-text-muted underline underline-offset-2 hover:text-text"
          >
            {open ? "Show less" : "Show full thinking"}
          </button>
        </>
      )}
    </div>
  );
}
