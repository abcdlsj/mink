import { act, renderHook } from "@testing-library/react";
import { afterEach, describe, expect, it } from "vitest";

import {
  activityForAgent,
  clearAgentActivity,
  recordAgentActivity,
  useAgentActivity,
  type AgentActivityEvent,
} from "./agentActivity";

function activityEvent(
  eventId: string,
  data: Partial<AgentActivityEvent["data"]> = {},
): AgentActivityEvent {
  return {
    event_id: eventId,
    occurred_at: "2026-08-03T00:00:00Z",
    data: {
      member_id: "agent-1",
      kind: "message.send",
      ...data,
    },
  };
}

afterEach(() => clearAgentActivity());

describe("agent activity store", () => {
  it("records the newest item first and updates the hook", () => {
    const { result } = renderHook(() => useAgentActivity("agent-1"));
    expect(result.current).toHaveLength(0);
    act(() => {
      recordAgentActivity(activityEvent("e1", { message_id: "m1" }));
      recordAgentActivity(activityEvent("e2", { kind: "task.create", task_id: "t1" }));
    });
    expect(result.current.map((item) => item.eventId)).toEqual(["e2", "e1"]);
    expect(result.current[0].kind).toBe("task.create");
  });

  it("dedupes replays of the same action by kind and resource", () => {
    act(() => {
      recordAgentActivity(activityEvent("e1", { message_id: "m1" }));
      recordAgentActivity(activityEvent("e2", { message_id: "m1" }));
    });
    expect(activityForAgent("agent-1")).toHaveLength(1);
  });

  it("keeps distinct interactions by the same agent", () => {
    act(() => {
      recordAgentActivity(activityEvent("e1", { message_id: "m1" }));
      recordAgentActivity(activityEvent("e2", { message_id: "m2" }));
    });
    expect(activityForAgent("agent-1")).toHaveLength(2);
  });

  it("caps the per-agent feed at 50 rows", () => {
    act(() => {
      for (let index = 0; index < 60; index += 1) {
        recordAgentActivity(activityEvent(`e${index}`, { message_id: `m${index}` }));
      }
    });
    const items = activityForAgent("agent-1");
    expect(items).toHaveLength(50);
    expect(items[0].messageId).toBe("m59");
    expect(items[49].messageId).toBe("m10");
  });

  it("keeps feeds of different agents separate", () => {
    act(() => {
      recordAgentActivity(activityEvent("e1", { member_id: "agent-2", message_id: "m1" }));
    });
    expect(activityForAgent("agent-1")).toHaveLength(0);
    expect(activityForAgent("agent-2")).toHaveLength(1);
  });
});
