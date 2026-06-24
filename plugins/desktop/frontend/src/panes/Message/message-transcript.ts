import type { MessageView } from "@/lib/types";

export function messageDisplayName(m: MessageView): string {
  if (m.role === "user") return "You";
  if (m.role === "system") return "System";
  return m.author_name || m.author_id || "Sumi";
}

export function messagePlainText(m: MessageView): string {
  const parts: string[] = [];
  if (m.reasoning) parts.push("[reasoning]\n" + m.reasoning.trim());
  if (m.content) parts.push(m.content.trim());
  for (const ev of m.events || []) {
    if (ev.kind === "service_notice" && ev.output) {
      parts.push("[notice] " + ev.output.trim());
    } else if (ev.kind === "tool_call") {
      const status = ev.status ? " " + ev.status : "";
      const summary = ev.output || ev.err || ev.args || "";
      parts.push(`[tool${status}] ${[ev.tool_name, summary].filter(Boolean).join(": ")}`.trim());
    } else if (ev.kind === "mention" || ev.kind === "delegate") {
      const label = ev.agent_display || ev.agent_id || ev.kind;
      const summary = ev.reply || ev.output || ev.err || ev.task || "";
      parts.push(`[${ev.kind}] ${[label, summary].filter(Boolean).join(": ")}`.trim());
    }
  }
  return parts.filter(Boolean).join("\n\n").trim();
}

export function serializeMessagesForCopy(input: {
  title?: string;
  sourceLabel: string;
  messages: MessageView[];
  hrefFor: (m: MessageView) => string;
}): string {
  const title = input.title?.trim() || "Sumi transcript";
  const lines: string[] = [
    "Sumi transcript excerpt",
    "Title: " + title,
    "Source: " + input.sourceLabel,
    "Messages: " + input.messages.length,
  ];
  input.messages.forEach((m) => {
    const text = messagePlainText(m) || "(empty)";
    lines.push(
      "",
      `[${m.time}] ${messageDisplayName(m)} (msg:${m.id}, role:${m.role})`,
      `source: ${input.sourceLabel}`,
      `link: ${input.hrefFor(m)}`,
      "",
      text,
    );
  });
  return lines.map((line) => "> " + line).join("\n");
}
