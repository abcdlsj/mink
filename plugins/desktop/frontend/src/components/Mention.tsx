import { Fragment } from "react";

const RE = /(^|\s)@([A-Za-z0-9_-]{1,32})/g;

export function renderMentions(
  text: string,
  known: Set<string>,
): React.ReactNode[] {
  const out: React.ReactNode[] = [];
  let last = 0;
  let i = 0;
  RE.lastIndex = 0;
  let m: RegExpExecArray | null;
  while ((m = RE.exec(text)) !== null) {
    const lead = m[1];
    const id = m[2];
    const start = m.index + lead.length;
    if (start > last) {
      out.push(<Fragment key={`t${i}`}>{text.slice(last, start)}</Fragment>);
    }
    if (known.has(id)) {
      out.push(
        <span
          key={`m${i}`}
          className="border border-border bg-accent px-1 py-px font-semibold text-text"
        >
          @{id}
        </span>,
      );
    } else {
      out.push(<Fragment key={`m${i}`}>@{id}</Fragment>);
    }
    last = m.index + m[0].length;
    i++;
  }
  if (last < text.length) {
    out.push(<Fragment key="tail">{text.slice(last)}</Fragment>);
  }
  return out;
}
