import type { AgentDMItem, AgentItem, MessageView } from "@/lib/types";

export function personaForActiveAgent(
  agents: AgentItem[],
  agentDMs: AgentDMItem[],
  activeAgentSpace: string | null,
  detailPersonaID?: string,
): AgentItem | undefined {
  if (detailPersonaID) {
    const fromDetail = agents.find((a) => a.id === detailPersonaID);
    if (fromDetail) return fromDetail;
  }
  if (!activeAgentSpace) return undefined;
  const direct = agents.find((a) => a.id === activeAgentSpace);
  if (direct) return direct;
  const dm = agentDMs.find((d) => d.id === activeAgentSpace);
  return dm && agents.find((a) => a.id === dm.persona_id);
}

export function renderableMessage(m: MessageView): boolean {
  if (m.is_thread_reply) return false;
  if (m.status === "pending" || m.status === "failed") return true;
  if (m.content && m.content.trim() !== "") return true;
  if (m.reasoning && m.reasoning.trim() !== "") return true;
  if (m.events && m.events.length > 0) return true;
  if (m.thread_id && m.thread_summary) return true;
  if (m.thread_info) return true;
  return false;
}

export function shortRole(role: string): string {
  const trimmed = role.trim().replace(/[.。!?！？]$/, "");
  if (!trimmed) return "";
  const firstWord = trimmed.split(/[\s—·,、/]+/)[0] || trimmed;
  const word = firstWord.length <= 14 ? firstWord : firstWord.slice(0, 14) + "…";
  return titleCase(word);
}

export function stripCollabLeak(text: string): string {
  let out = text;
  out = out.replace(/[（(]\s*task_id\s*=\s*[A-Za-z0-9_-]+\s*[）)]/g, "");
  out = out.replace(/[,，]?\s*task_id\s*=\s*[A-Za-z0-9_-]+/g, "");
  out = out.replace(/\bscheduled\s+next\s+team\s+turn\s+for\s+\S+.*$/gim, "");
  return out.replace(/[ \t]+\n/g, "\n").replace(/\n{3,}/g, "\n\n").trim();
}

function titleCase(s: string): string {
  if (!/[a-zA-Z]/.test(s)) return s;
  return s
    .toLowerCase()
    .replace(/(?:^|[\s-])(\p{L})/gu, (m) => m.toUpperCase());
}
