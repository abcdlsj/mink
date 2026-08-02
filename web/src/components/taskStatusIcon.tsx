import { CircleCheck, CircleDot, Eye, ListTodo, XCircle } from "lucide-react";

import type { TaskStatus } from "../api/client";

/** One icon per Task status, shared by the Tasks page and Message badges. */
export function TaskStatusIcon({ status }: { status: TaskStatus }) {
  if (status === "done") return <CircleCheck aria-hidden="true" />;
  if (status === "closed") return <XCircle aria-hidden="true" />;
  if (status === "in_review") return <Eye aria-hidden="true" />;
  if (status === "in_progress") return <CircleDot aria-hidden="true" />;
  return <ListTodo aria-hidden="true" />;
}
