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

const HOT_WINDOW_MS = 120_000;
const HOT_THRESHOLD = 5;
const VISIT_HOLD_MS = 45_000;

const WANDER_DELAY_MS = 25_000;
const WANDER_HOLD_MS = 5_000;
const WANDER_ARRIVE_MS = 5_000;
const LEISURE_HOLD_MS = 12_000;

interface OfficeProp {
  src: string;
  x: number;
  y: number;
  width: number;
  height: number;
}

interface OfficeLayout {
  width: number;
  height: number;
  workstations: readonly { x: number; y: number }[];
  desks: readonly { x: number; y: number }[];
  wallTiles: readonly { x: number }[];
  props: readonly OfficeProp[];
  wanderSpots: readonly { x: number; y: number }[];
  leisureSpots: readonly { x: number; y: number }[];
  meetingSpots: readonly { x: number; y: number }[];
  visitorSpot: { x: number; y: number };
}

function buildOfficeLayout(count: number): OfficeLayout {
  const tier = count <= 0 ? 0 : count <= 3 ? 1 : count <= 6 ? 2 : count <= 9 ? 3 : 4;
  const sizes = [
    { width: 320, height: 180 },
    { width: 480, height: 270 },
    { width: 640, height: 360 },
    { width: 800, height: 450 },
    { width: 960, height: 540 },
  ];
  const { width, height } = sizes[tier];
  const wallTiles = Array.from({ length: Math.ceil(width / 64) }, (_, index) => ({
    x: Math.min(index * 64, width - 64),
  }));

  const workstations: { x: number; y: number }[] = [];
  if (tier === 1) {
    const n = Math.min(count, 3);
    const spacing = 80;
    const startX = (width - (n - 1) * spacing) / 2;
    const y = Math.round(height * 0.48);
    for (let index = 0; index < n; index += 1) {
      workstations.push({ x: Math.round(startX + index * spacing), y });
    }
  } else if (tier === 2) {
    const rows = 2;
    const cols = 3;
    const startX = (width - (cols - 1) * 80) / 2;
    const startY = Math.round(height * 0.38);
    for (let row = 0; row < rows && workstations.length < count; row += 1) {
      for (let col = 0; col < cols && workstations.length < count; col += 1) {
        workstations.push({ x: Math.round(startX + col * 80), y: startY + row * 90 });
      }
    }
  } else if (tier === 3) {
    const rows = 3;
    const cols = 3;
    const startX = (width - (cols - 1) * 80) / 2;
    const startY = Math.round(height * 0.3);
    for (let row = 0; row < rows && workstations.length < count; row += 1) {
      for (let col = 0; col < cols && workstations.length < count; col += 1) {
        workstations.push({ x: Math.round(startX + col * 80), y: startY + row * 80 });
      }
    }
  } else if (tier === 4) {
    const rows = 3;
    const cols = 4;
    const spacingX = 112;
    const startX = (width - (cols - 1) * spacingX) / 2;
    const startY = 160;
    for (let row = 0; row < rows && workstations.length < count; row += 1) {
      for (let col = 0; col < cols && workstations.length < count; col += 1) {
        workstations.push({ x: Math.round(startX + col * spacingX), y: startY + row * 120 });
      }
    }
  }

  const desks = workstations.map((station) => ({ x: station.x, y: station.y + 24 }));
  const props: OfficeProp[] = [];
  if (tier >= 1) {
    props.push({ src: "plant.png", x: 16, y: height - 48, width: 32, height: 32 });
    props.push({ src: "plant.png", x: width - 48, y: height - 48, width: 32, height: 32 });
    props.push({ src: "water-cooler.png", x: 16, y: height - 96, width: 16, height: 32 });
    props.push({ src: "trash.png", x: width - 32, y: height - 96, width: 16, height: 16 });
    const gameX = (width - 64) / 2;
    const gameY = height - 88;
    props.push({ src: "stamping-table.png", x: gameX, y: gameY, width: 64, height: 32 });
    props.push({ src: "chair.png", x: gameX + 24, y: gameY - 20, width: 16, height: 16 });
    props.push({ src: "chair.png", x: gameX + 24, y: gameY + 40, width: 16, height: 16 });
  }
  if (tier >= 2) {
    props.push({ src: "printer.png", x: 16, y: height - 160, width: 64, height: 32 });
    props.push({ src: "coffee-maker.png", x: width - 80, y: height - 160, width: 64, height: 64 });
  }
  if (tier >= 3) {
    props.push({ src: "sink.png", x: width - 80, y: height - 240, width: 64, height: 64 });
    props.push({ src: "cabinet.png", x: width - 80, y: 16, width: 64, height: 64 });
  }
  if (tier >= 4) {
    const tableX = (width - 64) / 2;
    const tableY = (height - 64) / 2;
    props.push({ src: "writing-table.png", x: tableX, y: tableY, width: 64, height: 64 });
    props.push({ src: "chair.png", x: tableX + 24, y: tableY - 20, width: 16, height: 16 });
    props.push({ src: "chair.png", x: tableX + 24, y: tableY + 68, width: 16, height: 16 });
    props.push({ src: "chair.png", x: tableX - 20, y: tableY + 24, width: 16, height: 16 });
    props.push({ src: "chair.png", x: tableX + 68, y: tableY + 24, width: 16, height: 16 });
  }

  const leisureSpots =
    tier >= 1
      ? [
          { x: 40, y: height - 72 },
          { x: width - 48, y: height - 96 },
          { x: (width - 64) / 2 + 32, y: height - 88 + 16 },
          { x: (width - 64) / 2 + 32, y: height - 88 + 48 },
        ]
      : [];

  const blocked = [
    ...workstations.map((station) => ({
      x0: station.x - 40,
      y0: station.y - 32,
      x1: station.x + 40,
      y1: station.y + 40,
    })),
    ...props.map((prop) => ({
      x0: prop.x - 8,
      y0: prop.y - 8,
      x1: prop.x + prop.width + 8,
      y1: prop.y + prop.height + 8,
    })),
  ];
  const wanderSpots: { x: number; y: number }[] = [];
  for (let y = 104; y < height - 16; y += 48) {
    for (let x = 40; x < width - 16; x += 64) {
      if (!blocked.some((box) => x >= box.x0 && x <= box.x1 && y >= box.y0 && y <= box.y1)) {
        wanderSpots.push({ x, y });
      }
    }
  }
  if (!wanderSpots.length) wanderSpots.push({ x: width / 2, y: height - 40 });

  const meetingSpots =
    tier >= 4
      ? [
          { x: (width - 64) / 2 + 32, y: (height - 64) / 2 + 32 },
          { x: (width - 64) / 2 + 8, y: (height - 64) / 2 + 56 },
          { x: (width - 64) / 2 + 56, y: (height - 64) / 2 + 56 },
          { x: (width - 64) / 2 + 32, y: (height - 64) / 2 + 80 },
        ]
      : tier === 3
        ? [{ x: width / 2, y: 175 }]
        : tier === 2
          ? [{ x: width / 2, y: 182 }]
          : [{ x: width / 2, y: height - 60 }];

  return {
    width,
    height,
    workstations,
    desks,
    wallTiles,
    props,
    wanderSpots,
    leisureSpots,
    meetingSpots,
    visitorSpot: { x: width - 80, y: height - 48 },
  };
}

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
  const layout = useMemo(() => buildOfficeLayout(activeAgents.length), [activeAgents.length]);
  const [dmVisits, setDmVisits] = useState<ReadonlyMap<string, DmVisit>>(new Map());
  const [hotGroups, setHotGroups] = useState<ReadonlyMap<string, HotGroup>>(new Map());
  const [settled, setSettled] = useState<ReadonlySet<string> | null>(null);
  const [wanderByMember, setWanderByMember] = useState<
    ReadonlyMap<string, { x: number; y: number; at: number; kind: "wander" | "leisure" | "desk" }>
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
          const agentIndex = activeAgents.findIndex((candidate) => candidate.member_id === memberId);
          const desk = layout.desks[agentIndex] ?? layout.desks[0];
          if (!wander) {
            const idleFor = now - (lastIdleAt.current.get(memberId) ?? now);
            if (idleFor >= WANDER_DELAY_MS) {
              nextWander.set(memberId, {
                ...pickIdleTarget(layout.wanderSpots, layout.leisureSpots, desk),
                at: now,
              });
              nextSettled.delete(memberId);
              settledChanged = true;
            }
          } else if (
            currentSettled.has(memberId) &&
            now - wander.at >= (wander.kind === "leisure" ? LEISURE_HOLD_MS : WANDER_HOLD_MS)
          ) {
            nextWander.set(memberId, {
              ...pickIdleTarget(layout.wanderSpots, layout.leisureSpots, desk, wander),
              at: now,
            });
            nextSettled.delete(memberId);
            settledChanged = true;
          }
        }
      }
      for (const [memberId, wander] of nextWander) {
        if (!currentSettled.has(memberId) && now - wander.at >= WANDER_ARRIVE_MS) {
          nextSettled.add(memberId);
          settledChanged = true;
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
  }, [activeAgents, dmVisits, hotGroups, layout, settled, wanderByMember]);

  useEffect(() => {
    const element = stageRef.current;
    if (!element) return;
    const update = () => setScale(Math.min(1, element.clientWidth / layout.width));
    update();
    const observer = typeof ResizeObserver === "undefined" ? undefined : new ResizeObserver(update);
    observer?.observe(element);
    window.addEventListener("resize", update);
    return () => {
      observer?.disconnect();
      window.removeEventListener("resize", update);
    };
  }, [layout.width]);

  const deskByMember = useMemo(
    () =>
      new Map(
        activeAgents.map((agent, index) => [
          agent.member_id,
          layout.desks[index % layout.desks.length],
        ]),
      ),
    [activeAgents, layout.desks],
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

      <div
        className="office-stage-wrap"
        ref={stageRef}
        style={{
          width: `min(calc(100% - 40px), ${layout.width}px)`,
          aspectRatio: `${layout.width} / ${layout.height}`,
        }}
      >
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
            style={{ width: layout.width, height: layout.height, transform: `scale(${scale})` }}
          >
            <OfficeFurniture activeAgents={activeAgents} layout={layout} />
            {activeAgents.map((agent) => {
              const desk = deskByMember.get(agent.member_id) ?? layout.desks[0];
              const visit = [...dmVisits.values()].find((candidate) => candidate.visitorId === agent.member_id);
              const hosting = [...dmVisits.values()].find((candidate) => candidate.hostId === agent.member_id);
              const group = [...hotGroups.values()].find((candidate) => candidate.memberIds.includes(agent.member_id));
              const status = agent.activity_status;
              const working = status === "working";
              const talk = Boolean(visit || hosting);
              const wander = wanderByMember.get(agent.member_id);
              const target = visit
                ? visitHostTarget(visit.hostId, deskByMember, layout.visitorSpot)
                : group
                  ? meetingSpot(group, agent.member_id, layout.meetingSpots)
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
                        ? wander.kind === "leisure"
                          ? "leisure"
                          : "stand"
                        : "sit";
              const duration = reduced
                ? 0
                : Math.min(6000, Math.max(1200, 500 + distance(desk, target) * 5));
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

function OfficeFurniture({ activeAgents, layout }: { activeAgents: Agent[]; layout: OfficeLayout }) {
  return (
    <>
      {layout.wallTiles.map((tile, index) => (
        <OfficeSprite
          key={`wall-${index}`}
          src="partition-wall.png"
          x={tile.x}
          y={0}
          width={64}
          height={64}
        />
      ))}
      {layout.props.map((prop, index) => (
        <OfficeSprite key={`${prop.src}-${index}`} {...prop} />
      ))}
      {layout.workstations.map((station, index) => {
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

function visitHostTarget(
  hostId: string,
  deskByMember: Map<string, { x: number; y: number }>,
  visitorSpot: { x: number; y: number },
): { x: number; y: number } {
  const desk = deskByMember.get(hostId);
  return desk ? { x: desk.x + 54, y: desk.y + 4 } : visitorSpot;
}

function meetingSpot(
  group: HotGroup,
  memberId: string,
  spots: readonly { x: number; y: number }[],
): { x: number; y: number } {
  const index = group.memberIds.indexOf(memberId);
  if (index < 0 || !spots.length) return { x: 0, y: 0 };
  return spots[index % spots.length];
}

function distance(from: { x: number; y: number }, to: { x: number; y: number }): number {
  return Math.hypot(to.x - from.x, to.y - from.y);
}

function pickIdleTarget(
  spots: readonly { x: number; y: number }[],
  leisureSpots: readonly { x: number; y: number }[],
  desk: { x: number; y: number },
  exclude?: { x: number; y: number },
): { x: number; y: number; kind: "wander" | "leisure" | "desk" } {
  const choices: { x: number; y: number; kind: "wander" | "leisure" | "desk" }[] = [
    ...spots.map((spot) => ({ ...spot, kind: "wander" as const })),
    ...leisureSpots.map((spot) => ({ ...spot, kind: "leisure" as const })),
    { ...desk, kind: "desk" as const },
  ];
  const candidates = exclude
    ? choices.filter((choice) => choice.x !== exclude.x || choice.y !== exclude.y)
    : choices;
  return candidates[Math.floor(Math.random() * candidates.length)] ?? choices[0];
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
