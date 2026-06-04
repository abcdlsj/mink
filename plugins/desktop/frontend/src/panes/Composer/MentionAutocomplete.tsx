import { cn } from "@/lib/utils";
import type { MentionCandidate, MentionState } from "./composer-helpers";

export function MentionAutocomplete({
  state,
  candidates,
  index,
  onAccept,
  onHover,
}: {
  state: MentionState;
  candidates: MentionCandidate[];
  index: number;
  onAccept: (idx: number) => void;
  onHover: (idx: number) => void;
}) {
  if (candidates.length === 0) {
    return (
      <div className="absolute bottom-full left-0 z-30 mb-1.5 w-[280px] border-hard border-border bg-panel px-3 py-1.5 text-[12px] text-text-muted shadow-hard">
        No agent matches "{state.query}"
      </div>
    );
  }
  return (
    <div className="absolute bottom-full left-0 z-30 mb-1.5 max-h-[260px] w-[280px] overflow-y-auto border-hard border-border bg-panel py-1 text-[13px] shadow-hard">
      {candidates.map((a, i) => (
        <button
          key={a.id}
          type="button"
          onMouseDown={(e) => {
            e.preventDefault();
            onAccept(i);
          }}
          onMouseEnter={() => onHover(i)}
          className={cn(
            "flex w-full items-center gap-2 px-3 py-1.5 text-left",
            i === index ? "bg-accent" : "hover:bg-accent",
          )}
        >
          <span className="text-text-faint">@</span>
          <span className="text-text">{a.id}</span>
          {a.display && a.display !== a.id && (
            <span className="ml-auto text-[11.5px] text-text-faint">{a.display}</span>
          )}
        </button>
      ))}
    </div>
  );
}
