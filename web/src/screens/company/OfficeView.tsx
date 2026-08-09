import { useQuery } from "@tanstack/react-query";
import { Link } from "@tanstack/react-router";
import { Building2, ListTodo, MessageCircle, Users } from "lucide-react";
import { useEffect, useMemo, useRef, useState } from "react";

import {
  listAgents,
  listChannelMembers,
  listMemberDirectMessages,
  listMessages,
  type Agent,
  type Channel,
  type DirectMessage,
  type Member,
} from "../../api/client";
import { activityLabel } from "../../agentActivity";
import { PixelAgent, type AgentPose } from "../../components/company/PixelAgent";
import { useSpaceEvents } from "../../hooks/useSpaceEvents";

const ROOM_WIDTH = 960;
const HOT_WINDOW_MS = 120_000;
const HOT_THRESHOLD = 5;
const VISIT_HOLD_MS = 45_000;

const WORKSTATIONS: readonly { x: number; y: number }[] = [
  { x: 120, y: 230 },
  { x: 320, y: 230 },
  { x: 520, y: 230 },
  { x: 720, y: 230 },
  { x: 110, y: 110 },
  { x: 210, y: 110 },
  { x: 750, y: 110 },
  { x: 850, y: 110 },
  { x: 120, y: 410 },
  { x: 320, y: 410 },
  { x: 520, y: 410 },
  { x: 720, y: 410 },
];

const DESKS: readonly { x: number; y: number }[] = WORKSTATIONS.map((station) => ({
  x: station.x,
  y: station.y + 24,
}));

const MEETING_SPOTS: readonly { x: number; y: number }[] = [
  { x: 480, y: 288 },
  { x: 384, y: 318 },
  { x: 576, y: 318 },
  { x: 480, y: 372 },
  { x: 390, y: 352 },
  { x: 570, y: 352 },
  { x: 480, y: 322 },
  { x: 480, y: 348 },
];

const VISITOR_SPOT = { x: 860, y: 430 };
const WANDER_DELAY_MS = 25_000;
const WANDER_HOLD_MS = 8_000;
const WANDER_SPOTS: readonly { x: number; y: number }[] = [
  { x: 200, y: 320 },
  { x: 320, y: 320 },
  { x: 440, y: 320 },
  { x: 560, y: 320 },
  { x: 680, y: 320 },
  { x: 800, y: 320 },
  { x: 200, y: 170 },
  { x: 320, y: 170 },
  { x: 560, y: 170 },
  { x: 680, y: 170 },
  { x: 200, y: 480 },
  { x: 440, y: 480 },
  { x: 680, y: 480 },
  { x: 60, y: 300 },
  { x: 900, y: 300 },
];

interface DmVisit {
  visitorId: string;
  hostId: string;
  until: number;
}

interface HotGroup {
  channelId: string;
  channelSlug: string;
  memberIds: string[];
  until: number;
}

export function CompanyOfficeView({
  spaceSlug,
  spaceId,
  channels,
  directMessages,
  members,
  currentMember,
}: {
  spaceSlug: string;
  spaceId: string;
  channels: Channel[];
  directMessages: DirectMessage[];
  members: Member[];
  currentMember: Member;
}) {
  const reduced = useReducedMotion();
  const agents = useQuery({
    queryKey: ["agents", spaceId],
    queryFn: () => listAgents(spaceId),
    enabled: true,
  });
  const activeAgents = useMemo(
    () => (agents.data ?? []).filter((agent) => agent.desired_lifecycle === "active"),
    [agents.data],
  );
  const [dmVisits, setDmVisits] = useState<ReadonlyMap<string, DmVisit>>(new Map());
  const [hotGroups, setHotGroups] = useState<ReadonlyMap<string, HotGroup>>(new Map());
  const [settled, setSettled] = useState<ReadonlySet<string> | null>(null);
  const [wanderByMember, setWanderByMember] = useState<
    ReadonlyMap<string, { x: number; y: number; at: number }>
  >(new Map());
  const [governorDms, setGovernorDms] = useState<ReadonlyMap<string, { a: string; b: string }>>(
    () => new Map(),
  );
  const channelActivity = useRef<Map<string, number[]>>(new Map());
  const lastIdleAt = useRef<Map<string, number>>(new Map());
  const stageRef = useRef<HTMLDivElement>(null);
  const [scale, setScale] = useState(1);

  const isGovernor = ["owner", "admin"].includes(currentMember.access_level);
  const viewerDmDirectory = useMemo(() => {
    const directory = new Map<string, { a: string; b: string }>();
    for (const dm of directMessages) {
      directory.set(dm.channel_id, { a: currentMember.id, b: dm.other_member.id });
    }
    return directory;
  }, [currentMember.id, directMessages]);
  useEffect(() => {
    if (!isGovernor || activeAgents.length === 0) return;
    let cancelled = false;
    void Promise.all(activeAgents.map((agent) => listMemberDirectMessages(agent.member_id)))
      .then((results) => {
        if (cancelled) return;
        const directory = new Map<string, { a: string; b: string }>();
        for (let index = 0; index < activeAgents.length; index += 1) {
          const subject = activeAgents[index].member_id;
          for (const dm of results[index] ?? []) {
            if (dm.other_member.kind !== "agent") continue;
            directory.set(dm.channel_id, { a: subject, b: dm.other_member.id });
          }
        }
        setGovernorDms(directory);
      })
      .catch(() => {
        if (!cancelled) setGovernorDms(new Map());
      });
    return () => {
      cancelled = true;
    };
  }, [activeAgents, isGovernor]);
  const dmDirectory = useMemo(() => {
    const merged = new Map(viewerDmDirectory);
    for (const [channelId, pair] of governorDms) merged.set(channelId, pair);
    return merged;
  }, [governorDms, viewerDmDirectory]);
  const settledSet = settled ?? new Set(activeAgents.map((agent) => agent.member_id));
  useEffect(() => {
    const now = Date.now();
    const ids = new Set(activeAgents.map((agent) => agent.member_id));
    for (const agent of activeAgents) {
      if (!lastIdleAt.current.has(agent.member_id)) lastIdleAt.current.set(agent.member_id, now);
    }
    for (const memberId of [...lastIdleAt.current.keys()]) {
      if (!ids.has(memberId)) lastIdleAt.current.delete(memberId);
    }
  }, [activeAgents]);

  async function handleDmMessage(pair: { a: string; b: string }, channelId: string) {
    let authorId: string | undefined;
    try {
      const page = await listMessages(channelId);
      authorId = page.messages[page.messages.length - 1]?.author.id;
    } catch {
      // Directory still lets us visualise the discussion without the author.
    }
    const visitor =
      typeof authorId === "string" && (authorId === pair.a || authorId === pair.b)
        ? authorId
        : pair.a;
    const agentIds = new Set(activeAgents.map((agent) => agent.member_id));
    const agentVisitor = agentIds.has(visitor) ? visitor : (visitor === pair.a ? pair.b : pair.a);
    const host = agentVisitor === pair.a ? pair.b : pair.a;
    const now = Date.now();
    setDmVisits((current) =>
      new Map(current).set(channelId, {
        visitorId: agentVisitor,
        hostId: host,
        until: now + VISIT_HOLD_MS,
      }),
    );
    setSettled((current) => {
      const next = new Set(current ?? activeAgents.map((agent) => agent.member_id));
      next.delete(agentVisitor);
      return next;
    });
    setWanderByMember((current) => {
      const next = new Map(current);
      next.delete(agentVisitor);
      return next;
    });
  }

  function recordChannelMessage(channelId: string, now: number) {
    const cutoff = now - HOT_WINDOW_MS;
    const times = [...(channelActivity.current.get(channelId) ?? []), now].filter(
      (timestamp) => timestamp >= cutoff,
    );
    channelActivity.current.set(channelId, times);
    if (times.length >= HOT_THRESHOLD) void markChannelHot(channelId, now);
  }

  async function markChannelHot(channelId: string, now: number) {
    const slug = channels.find((channel) => channel.id === channelId)?.slug ?? channelId;
    setHotGroups((current) => {
      const existing = current.get(channelId);
      if (!existing) return current;
      return new Map(current).set(channelId, { ...existing, until: now + HOT_WINDOW_MS });
    });
    if (hotGroups.has(channelId)) return;
    try {
      const channelMembers = await listChannelMembers(channelId);
      const memberIds = channelMembers.members
        .filter((member) => member.kind === "agent")
        .map((member) => member.id);
      setHotGroups((current) =>
        current.has(channelId)
          ? new Map(current).set(channelId, {
              ...(current.get(channelId) as HotGroup),
              until: now + HOT_WINDOW_MS,
            })
          : new Map(current).set(channelId, {
              channelId,
              channelSlug: slug,
              memberIds,
              until: now + HOT_WINDOW_MS,
            }),
      );
      setSettled((current) => {
        const next = new Set(current);
        for (const memberId of memberIds) next.delete(memberId);
        return next;
      });
      setWanderByMember((current) => {
        const next = new Map(current);
        for (const memberId of memberIds) next.delete(memberId);
        return next;
      });
    } catch {
      // A channel that became unreadable between event and fetch is ignored.
    }
  }

  useSpaceEvents(spaceId, ({ channelId }) => {
    const now = Date.now();
    const pair = dmDirectory.get(channelId);
    if (pair) {
      void handleDmMessage(pair, channelId);
      return;
    }
    recordChannelMessage(channelId, now);
  });

  useEffect(() => {
    const timer = window.setInterval(() => {
      const now = Date.now();
      const expiredVisitors = [...dmVisits.values()]
        .filter((visit) => visit.until <= now)
        .map((visit) => visit.visitorId);
      const expiredGroupIds = [...hotGroups.values()]
        .filter((group) => group.until <= now)
        .flatMap((group) => group.memberIds);
      setDmVisits((current) => pruneByUntil(current, now));
      setHotGroups((current) => pruneByUntil(current, now));
      const currentSettled = settled ?? new Set(activeAgents.map((agent) => agent.member_id));
      const nextSettled = new Set(currentSettled);
      let settledChanged = false;
      if (expiredVisitors.length || expiredGroupIds.length) {
        for (const memberId of [...expiredVisitors, ...expiredGroupIds]) {
          if (nextSettled.delete(memberId)) settledChanged = true;
        }
      }
      const nextWander = new Map(wanderByMember);
      const activeVisits = [...dmVisits.values()];
      const activeGroups = [...hotGroups.values()];
      for (const agent of activeAgents) {
        const memberId = agent.member_id;
        const inVisit = activeVisits.some(
          (visit) => visit.visitorId === memberId || visit.hostId === memberId,
        );
        const inGroup = activeGroups.some((group) => group.memberIds.includes(memberId));
        const working = agent.activity_status === "working";
        if (working || inVisit || inGroup) {
          if (nextWander.delete(memberId)) {
            nextSettled.delete(memberId);
            settledChanged = true;
          }
          lastIdleAt.current.set(memberId, now);
        } else {
          const wander = nextWander.get(memberId);
          if (!wander) {
            const idleFor = now - (lastIdleAt.current.get(memberId) ?? now);
            if (idleFor >= WANDER_DELAY_MS) {
              nextWander.set(memberId, { ...pickWanderSpot(), at: now });
              nextSettled.delete(memberId);
              settledChanged = true;
            }
          } else if (currentSettled.has(memberId) && now - wander.at >= WANDER_HOLD_MS) {
            nextWander.set(memberId, { ...pickWanderSpot(), at: now });
            nextSettled.delete(memberId);
            settledChanged = true;
          }
        }
      }
      setWanderByMember((current) => {
        const currentEntries = [...current.entries()];
        const nextEntries = [...nextWander.entries()];
        if (
          currentEntries.length === nextEntries.length &&
          currentEntries.every(
            ([key, value], index) =>
              nextEntries[index]?.[0] === key && nextEntries[index]?.[1] === value,
          )
        ) {
          return current;
        }
        return nextWander;
      });
      if (settledChanged) setSettled(nextSettled);
      const cutoff = now - HOT_WINDOW_MS;
      for (const [channelId, times] of channelActivity.current) {
        const kept = times.filter((timestamp) => timestamp >= cutoff);
        if (kept.length) channelActivity.current.set(channelId, kept);
        else channelActivity.current.delete(channelId);
      }
    }, 1000);
    return () => window.clearInterval(timer);
  }, [activeAgents, dmVisits, hotGroups, settled, wanderByMember]);

  useEffect(() => {
    const element = stageRef.current;
    if (!element) return;
    const update = () => setScale(Math.min(1, element.clientWidth / ROOM_WIDTH));
    update();
    const observer = typeof ResizeObserver === "undefined" ? undefined : new ResizeObserver(update);
    observer?.observe(element);
    window.addEventListener("resize", update);
    return () => {
      observer?.disconnect();
      window.removeEventListener("resize", update);
    };
  }, []);

  const deskByMember = useMemo(
    () =>
      new Map(
        activeAgents.map((agent, index) => [
          agent.member_id,
          DESKS[index % DESKS.length],
        ]),
      ),
    [activeAgents],
  );
  const memberById = useMemo(
    () => new Map(members.map((member) => [member.id, member])),
    [members],
  );
  const counts = useMemo(() => officeCounts(agents.data ?? []), [agents.data]);

  return (
    <section className="office-workspace" aria-labelledby="office-heading">
      <header className="office-header">
        <span className="office-header-glyph" aria-hidden="true"><Building2 /></span>
        <div className="page-title">
          <h1 id="office-heading" tabIndex={-1}>Office</h1>
          <p>Live view of every Agent at work.</p>
        </div>
        <Link className="command-button office-tasks-link" to="/s/$spaceSlug/tasks" params={{ spaceSlug }}>
          <ListTodo aria-hidden="true" />Tasks
        </Link>
      </header>

      <div className="office-status" aria-label="Agent activity">
        <div className="office-status-counts">
          {counts.length === 0 ? <span className="office-status-empty">No Agents paired</span> : counts.map((group) => (
            <span className="office-status-item" key={group.status}>
              <i className={`office-status-dot office-status-dot--${group.status}`} aria-hidden="true" />
              {group.label} <strong>{group.count}</strong>
            </span>
          ))}
        </div>
        {hotGroups.size ? (
          <div className="office-hot-list" aria-label="Active group conversations">
            {[...hotGroups.values()].map((group) => (
              <Link
                key={group.channelId}
                className="office-hot-chip"
                to="/s/$spaceSlug/channels/$channelSlug"
                params={{ spaceSlug, channelSlug: group.channelSlug }}
                title={`#${group.channelSlug} is busy`}
              >
                <MessageCircle aria-hidden="true" />#{group.channelSlug} gathering
              </Link>
            ))}
          </div>
        ) : null}
      </div>

      <div className="office-stage-wrap" ref={stageRef}>
        {activeAgents.length === 0 ? (
          <div className="office-empty">
            <Users aria-hidden="true" />
            <h2>No Agents are paired yet</h2>
            <p>Pair a Computer and create an Agent to see the office come alive.</p>
            <Link className="command-button command-button--accent" to="/s/$spaceSlug/members" params={{ spaceSlug }}>
              Open Members
            </Link>
          </div>
        ) : (
          <div
            className="office-room"
            style={{ transform: `scale(${scale})` }}
          >
            <OfficeFurniture activeAgents={activeAgents} />
            {activeAgents.map((agent) => {
              const desk = deskByMember.get(agent.member_id) ?? DESKS[0];
              const visit = [...dmVisits.values()].find((candidate) => candidate.visitorId === agent.member_id);
              const hosting = [...dmVisits.values()].find((candidate) => candidate.hostId === agent.member_id);
              const group = [...hotGroups.values()].find((candidate) => candidate.memberIds.includes(agent.member_id));
              const status = agent.activity_status;
              const working = status === "working";
              const talk = Boolean(visit || hosting);
              const wander = wanderByMember.get(agent.member_id);
              const target = visit
                ? visitHostTarget(visit.hostId, deskByMember)
                : group
                  ? meetingSpot(group, agent.member_id)
                  : working
                    ? desk
                    : wander ?? desk;
              const moving = !reduced && !settledSet.has(agent.member_id);
              const pose: AgentPose = moving
                ? "walk"
                : visit || hosting
                  ? "stand"
                  : group
                    ? "stand"
                    : working
                      ? "typing"
                      : wander
                        ? "stand"
                        : "sit";
              const duration = reduced
                ? 0
                : Math.min(2600, Math.max(350, 140 + distance(desk, target) * 2.1));
              const member = memberById.get(agent.member_id);
              const name = member?.display_name ?? agent.name;
              const offline = agent.computer_reachable === false;
              return (
                <Link
                  key={agent.member_id}
                  className={`office-agent${offline ? " office-agent--offline" : ""}`}
                  to="/s/$spaceSlug/agents/$agentId"
                  params={{ spaceSlug, agentId: agent.member_id }}
                  style={{
                    transform: `translate(${target.x - 16}px, ${target.y - 32}px)`,
                    transitionDuration: `${duration}ms`,
                  }}
                  onTransitionEnd={(event) => {
                    if (event.propertyName !== "transform") return;
                    setSettled((current) =>
                      new Set(current ?? activeAgents.map((agent) => agent.member_id)).add(agent.member_id),
                    );
                  }}
                  title={`${name} · ${activityLabel(status)}`}
                >
                  <PixelAgent
                    variant={agentVariant(agent.member_id)}
                    pose={pose}
                    working={working}
                    talking={talk}
                    flip={target.x < desk.x}
                  />
                  <span className="office-agent-label">
                    <i className={`office-status-dot office-status-dot--${status}`} aria-hidden="true" />
                    <span>{name}</span>
                  </span>
                </Link>
              );
            })}
          </div>
        )}
      </div>
      {isGovernor ? (
        <p className="office-privacy-note">Agent-to-Agent DMs are visible because you govern this Space.</p>
      ) : null}
      <p className="office-attribution">
        Office scene &amp; sprites by{" "}
        <a href="https://arlantr.itch.io/free-office-pixel-art" target="_blank" rel="noreferrer">
          Arlan_TR
        </a>{" "}
        · Free office pixel art
      </p>
    </section>
  );
}

function OfficeFurniture({ activeAgents }: { activeAgents: Agent[] }) {
  return (
    <>
      {Array.from({ length: 15 }, (_, index) => (
        <OfficeSprite
          key={`wall-${index}`}
          src="partition-wall.png"
          x={index * 64}
          y={0}
          width={64}
          height={64}
        />
      ))}
      <OfficeSprite src="water-cooler.png" x={20} y={240} width={16} height={32} />
      <OfficeSprite src="coffee-maker.png" x={896} y={240} width={64} height={64} />
      <OfficeSprite src="printer.png" x={20} y={120} width={64} height={32} />
      <OfficeSprite src="sink.png" x={896} y={120} width={64} height={64} />
      <OfficeSprite src="trash.png" x={24} y={80} width={16} height={16} />
      <OfficeSprite src="cabinet.png" x={896} y={80} width={64} height={64} />
      <OfficeSprite src="plant.png" x={32} y={470} width={32} height={32} />
      <OfficeSprite src="plant.png" x={896} y={470} width={32} height={32} />
      <OfficeSprite src="writing-table.png" x={448} y={288} width={64} height={64} />
      <OfficeSprite src="chair.png" x={472} y={268} width={16} height={16} />
      <OfficeSprite src="chair.png" x={472} y={356} width={16} height={16} />
      <OfficeSprite src="chair.png" x={428} y={312} width={16} height={16} />
      <OfficeSprite src="chair.png" x={516} y={312} width={16} height={16} />
      <OfficeSprite src="stamping-table.png" x={448} y={500} width={64} height={32} />
      {WORKSTATIONS.map((station, index) => {
        const working = activeAgents[index]?.activity_status === "working";
        return (
          <span
            key={`${station.x}-${station.y}`}
            className="office-workstation"
            aria-hidden="true"
            style={{ transform: `translate(${station.x - 64}px, ${station.y - 64}px)` }}
          >
            <OfficeSprite src="desk.png" x={32} y={48} width={64} height={32} />
            <OfficeSprite
              src={working ? "pc-on.png" : "pc-off.png"}
              x={48}
              y={32}
              width={32}
              height={32}
            />
            <OfficeSprite src="chair.png" x={56} y={76} width={16} height={16} />
          </span>
        );
      })}
    </>
  );
}

function OfficeSprite({
  src,
  x,
  y,
  width,
  height,
}: {
  src: string;
  x: number;
  y: number;
  width: number;
  height: number;
}) {
  return (
    <span
      className="office-sprite"
      style={{
        width,
        height,
        transform: `translate(${x}px, ${y}px)`,
        backgroundImage: `url("/office/${src}")`,
      }}
      aria-hidden="true"
    />
  );
}

function visitHostTarget(hostId: string, deskByMember: Map<string, { x: number; y: number }>): { x: number; y: number } {
  const desk = deskByMember.get(hostId);
  return desk ? { x: desk.x + 54, y: desk.y + 4 } : VISITOR_SPOT;
}

function meetingSpot(group: HotGroup, memberId: string): { x: number; y: number } {
  const index = group.memberIds.indexOf(memberId);
  if (index < 0) return MEETING_SPOTS[0];
  return MEETING_SPOTS[index % MEETING_SPOTS.length];
}

function distance(from: { x: number; y: number }, to: { x: number; y: number }): number {
  return Math.hypot(to.x - from.x, to.y - from.y);
}

function pickWanderSpot(): { x: number; y: number } {
  return WANDER_SPOTS[Math.floor(Math.random() * WANDER_SPOTS.length)];
}

function agentVariant(seed: string): number {
  let hash = 0;
  for (let index = 0; index < seed.length; index += 1) {
    hash = (hash * 31 + seed.charCodeAt(index)) >>> 0;
  }
  return hash % 4;
}

function pruneByUntil<T extends { until: number }>(current: ReadonlyMap<string, T>, now: number): ReadonlyMap<string, T> {
  let changed = false;
  const next = new Map(current);
  for (const [key, value] of next) {
    if (value.until <= now) {
      next.delete(key);
      changed = true;
    }
  }
  return changed ? next : current;
}

function officeCounts(agents: Agent[]): Array<{ status: string; label: string; count: number }> {
  const counts = new Map<string, number>();
  for (const agent of agents) {
    if (agent.desired_lifecycle !== "active") continue;
    const status = agent.activity_status;
    counts.set(status, (counts.get(status) ?? 0) + 1);
  }
  return [...counts.entries()].map(([status, count]) => ({
    status,
    label: activityLabel(status as Agent["activity_status"]),
    count,
  }));
}

function useReducedMotion(): boolean {
  const [reduced, setReduced] = useState(
    () => window.matchMedia?.("(prefers-reduced-motion: reduce)").matches ?? false,
  );
  useEffect(() => {
    const query = window.matchMedia?.("(prefers-reduced-motion: reduce)");
    if (!query) return;
    const onChange = () => setReduced(query.matches);
    query.addEventListener("change", onChange);
    return () => query.removeEventListener("change", onChange);
  }, []);
  return reduced;
}
