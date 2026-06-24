import type { MessageView } from "@/lib/types";

export interface SumiTranscript {
  title: string;
  source: string;
  messages: SumiTranscriptMessage[];
}

export interface SumiTranscriptMessage {
  id: string;
  role: string;
  sender: string;
  time: string;
  source: string;
  link: string;
  content: string;
}

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
  const transcript = buildTranscript({
    title,
    sourceLabel: input.sourceLabel,
    messages: input.messages,
    hrefFor: input.hrefFor,
  });
  const lines: string[] = [
    "Sumi transcript excerpt",
    "Title: " + title,
    "Source: " + input.sourceLabel,
    "Messages: " + input.messages.length,
  ];
  transcript.messages.forEach((m) => {
    lines.push(
      "",
      `[${m.time}] ${m.sender} (msg:${m.id}, role:${m.role})`,
      `source: ${m.source}`,
      `link: ${m.link}`,
      "",
      m.content || "(empty)",
    );
  });
  return [
    lines.map((line) => "> " + line).join("\n"),
    "",
    "```sumi-transcript",
    JSON.stringify(transcript, null, 2),
    "```",
  ].join("\n");
}

export function buildTranscript(input: {
  title: string;
  sourceLabel: string;
  messages: MessageView[];
  hrefFor: (m: MessageView) => string;
}): SumiTranscript {
  return {
    title: input.title,
    source: input.sourceLabel,
    messages: input.messages.map((m) => ({
      id: m.id,
      role: m.role,
      sender: messageDisplayName(m),
      time: String(m.time),
      source: input.sourceLabel,
      link: input.hrefFor(m),
      content: messagePlainText(m) || "(empty)",
    })),
  };
}

export function parseSumiTranscripts(text: string): SumiTranscript[] {
  const out: SumiTranscript[] = [];
  const re = /```sumi-transcript\s*([\s\S]*?)```/g;
  let match: RegExpExecArray | null;
  while ((match = re.exec(text)) !== null) {
    try {
      const parsed = JSON.parse(match[1]);
      if (isTranscript(parsed)) out.push(parsed);
    } catch {
      // Ignore malformed external clipboard text.
    }
  }
  return out;
}

export function transcriptAttachmentData(t: SumiTranscript): string {
  return JSON.stringify(t);
}

export function parseTranscriptAttachment(data: string | undefined): SumiTranscript | null {
  if (!data) return null;
  try {
    const parsed = JSON.parse(data);
    return isTranscript(parsed) ? parsed : null;
  } catch {
    return null;
  }
}

function isTranscript(value: unknown): value is SumiTranscript {
  if (!value || typeof value !== "object") return false;
  const v = value as Partial<SumiTranscript>;
  if (typeof v.title !== "string" || typeof v.source !== "string" || !Array.isArray(v.messages)) return false;
  return v.messages.every((m) => {
    if (!m || typeof m !== "object") return false;
    const item = m as Partial<SumiTranscriptMessage>;
    return typeof item.id === "string" &&
      typeof item.role === "string" &&
      typeof item.sender === "string" &&
      typeof item.time === "string" &&
      typeof item.source === "string" &&
      typeof item.link === "string" &&
      typeof item.content === "string";
  });
}
