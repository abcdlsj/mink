import type { MouseEvent } from "react";
import type { TaskAccessoryInfo } from "@/lib/types";
import { useStore } from "@/lib/store";
import { Dot } from "../LeftPane";
import { cn } from "@/lib/utils";

export function TaskAccessoryRow({ info }: { info: TaskAccessoryInfo }) {
  const expandTaskInRail = useStore((s) => s.expandTaskInRail);
  const expandedTaskID = useStore((s) => s.expandedTaskID);
  const taskInScope = useTaskInActiveRail(info.task_id);
  if (isQuietTerminalStatus(info.status)) return null;
  const label = taskAccessoryLabel(info);
  const isRunning = info.status === "running" || info.status === "queued";
  const opened = expandedTaskID === info.task_id;
  const onClick = (e: MouseEvent) => {
    e.stopPropagation();
    if (!taskInScope) return;
    expandTaskInRail(info.task_id);
  };
  return (
    <button
      type="button"
      onClick={onClick}
      className={cn(
        "mt-1.5 inline-flex cursor-pointer items-center gap-1.5 border px-1.5 py-0.5 text-left font-mono text-[10.5px]",
        isRunning ? "border-running-border bg-running-bg text-running" : "border-border-soft bg-transparent text-text-faint",
        taskInScope ? "hover:text-text" : "cursor-default",
        opened && "text-text",
      )}
    >
      {isRunning && <Dot status="running" />}
      <span>{label}</span>
    </button>
  );
}

function isQuietTerminalStatus(status: string): boolean {
  return status === "finished" || status === "done" || status === "complete" || status === "completed" || status === "no_output";
}

function useTaskInActiveRail(taskID: string): boolean {
  const participants = useStore((s) => s.participants);
  const threadDetail = useStore((s) => s.threadDetail);
  if (threadDetail && !threadDetail.unsupported && !threadDetail.not_found) {
    return (threadDetail.recent_runs || []).some((r) => r.id === taskID);
  }
  return (participants?.recent_runs || []).some((r) => r.id === taskID);
}

function taskAccessoryLabel(info: TaskAccessoryInfo): string {
  const who = info.worker_display || info.worker_id || "worker";
  switch (info.status) {
    case "queued":
      return who + " · queued";
    case "running":
      return who + " · working...";
    case "finished":
      return who + " · finished";
    case "failed":
      return info.short_outcome
        ? who + " · failed: " + info.short_outcome
        : who + " · failed";
    case "canceled":
      return info.short_outcome
        ? who + " · canceled: " + info.short_outcome
        : who + " · canceled";
    case "no_output":
      return who + " · finished with no output";
    default:
      return who + " · " + info.status;
  }
}
