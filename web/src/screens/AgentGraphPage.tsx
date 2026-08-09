import { useQuery } from "@tanstack/react-query";
import { Link, useParams } from "@tanstack/react-router";
import { Minus, Plus, RotateCcw, X } from "lucide-react";
import {
  useEffect,
  useMemo,
  useRef,
  useState,
  type KeyboardEvent as ReactKeyboardEvent,
  type PointerEvent as ReactPointerEvent,
} from "react";

import {
  getAgentGraph,
  type AgentGraphEdge,
  type AgentGraphMessage,
  type AgentGraphNode,
} from "../api/client";
import { PixelIdentity } from "../components/PixelIdentity";
import { buildAgentIdenticon, identityPalette } from "../components/agentIdenticon";
import { SpaceShell } from "../components/SpaceShell";
import { useExperimentalFeatures } from "../featureFlags";
import { layoutGraph, type GraphLayoutNode } from "./agentGraphLayout";
import "./agentGraph.css";

const VIEW_WIDTH = 900;
const VIEW_HEIGHT = 560;
const LAYOUT_ITERATIONS = 220;

export interface GraphView {
  x: number;
  y: number;
  k: number;
}

/** The graph world is already centered on the 900x560 canvas; the default view must not offset it. */
export const INITIAL_GRAPH_VIEW: GraphView = { x: 0, y: 0, k: 1 };

export function AgentGraphPage() {
  const { spaceSlug } = useParams({ from: "/s/$spaceSlug/graph" });
  const experimentalEnabled = useExperimentalFeatures();
  if (!experimentalEnabled) {
    return (
      <SpaceShell spaceSlug={spaceSlug} active="graph">
        {() => (
          <div className="route-status">
            <section className="route-status-panel">
              <p className="section-kicker">EXPERIMENTAL</p>
              <h1>Agent graph is disabled.</h1>
              <p>Enable experimental features in Settings to show this entry in the rail.</p>
              <Link
                className="command-button command-button--accent"
                to="/s/$spaceSlug/settings"
                params={{ spaceSlug }}
              >
                Open Settings
              </Link>
            </section>
          </div>
        )}
      </SpaceShell>
    );
  }
  return (
    <SpaceShell spaceSlug={spaceSlug} active="graph">
      {({ space }) => <AgentGraphWorkspace spaceId={space.id} spaceSlug={space.slug} />}
    </SpaceShell>
  );
}

export function AgentGraphWorkspace({
  spaceId,
  spaceSlug,
}: {
  spaceId: string;
  spaceSlug: string;
}) {
  const graph = useQuery({
    queryKey: ["agent-graph", spaceId],
    queryFn: () => getAgentGraph(spaceId),
  });
  const [selectedNodeId, setSelectedNodeId] = useState<string>();
  const [selectedEdgeKey, setSelectedEdgeKey] = useState<string>();
  const [view, setView] = useState<GraphView>(INITIAL_GRAPH_VIEW);
  const [dragOverrides, setDragOverrides] = useState<Map<string, { x: number; y: number }>>(
    () => new Map(),
  );
  const container = useRef<HTMLDivElement>(null);
  const dragging = useRef<{
    kind: "node" | "view";
    nodeId?: string;
    pointerId: number;
    lastX: number;
    lastY: number;
  } | undefined>(undefined);

  const nodes = useMemo(() => graph.data?.nodes ?? [], [graph.data]);
  const edges = useMemo(() => graph.data?.edges ?? [], [graph.data]);
  const edgeKey = (edge: AgentGraphEdge) => [edge.member_a_id, edge.member_b_id].sort().join(":");
  const baseLayout = useMemo(
    () => layoutGraph(nodes, edges, VIEW_WIDTH, VIEW_HEIGHT, LAYOUT_ITERATIONS),
    [nodes, edges],
  );
  const positions = useMemo(() => {
    const map = new Map<string, GraphLayoutNode>(
      baseLayout.map((node) => [node.member_id, { ...node }]),
    );
    for (const [memberId, point] of dragOverrides) {
      const node = map.get(memberId);
      if (node) map.set(memberId, { ...node, x: point.x, y: point.y });
    }
    return map;
  }, [baseLayout, dragOverrides]);

  const selectedNode = selectedNodeId ? positions.get(selectedNodeId) : undefined;
  const selectedEdge = selectedEdgeKey
    ? edges.find((edge) => edgeKey(edge) === selectedEdgeKey)
    : undefined;
  const neighborEdges = useMemo(
    () =>
      selectedNode
        ? edges.filter(
            (edge) => edge.member_a_id === selectedNode.member_id || edge.member_b_id === selectedNode.member_id,
          )
        : [],
    [edges, selectedNode],
  );

  function handlePointerDown(event: ReactPointerEvent<SVGSVGElement>) {
    const target = event.target as Element;
    const nodeGroup = target.closest("[data-node-id]") as SVGGElement | null;
    if (nodeGroup?.dataset.nodeId) {
      const node = positions.get(nodeGroup.dataset.nodeId);
      if (!node) return;
      event.preventDefault();
      dragging.current = {
        kind: "node",
        nodeId: node.member_id,
        pointerId: event.pointerId,
        lastX: event.clientX,
        lastY: event.clientY,
      };
      (event.currentTarget as SVGSVGElement).setPointerCapture(event.pointerId);
      return;
    }
    dragging.current = {
      kind: "view",
      pointerId: event.pointerId,
      lastX: event.clientX,
      lastY: event.clientY,
    };
    (event.currentTarget as SVGSVGElement).setPointerCapture(event.pointerId);
  }

  function handlePointerMove(event: ReactPointerEvent<SVGSVGElement>) {
    const drag = dragging.current;
    if (!drag || drag.pointerId !== event.pointerId) return;
    const dx = event.clientX - drag.lastX;
    const dy = event.clientY - drag.lastY;
    drag.lastX = event.clientX;
    drag.lastY = event.clientY;
    if (drag.kind === "view") {
      setView((current) => ({ ...current, x: current.x + dx, y: current.y + dy }));
      return;
    }
    const node = drag.nodeId ? positions.get(drag.nodeId) : undefined;
    if (!node) return;
    setDragOverrides((current) => {
      const next = new Map(current);
      const previous = next.get(node.member_id) ?? { x: node.x, y: node.y };
      next.set(node.member_id, {
        x: previous.x + dx / view.k,
        y: previous.y + dy / view.k,
      });
      return next;
    });
  }

  function handlePointerUp(event: ReactPointerEvent<SVGSVGElement>) {
    if (dragging.current?.pointerId === event.pointerId) {
      dragging.current = undefined;
      (event.currentTarget as SVGSVGElement).releasePointerCapture(event.pointerId);
    }
  }

  function handleWheel(event: React.WheelEvent<SVGSVGElement>) {
    event.preventDefault();
    const factor = event.deltaY < 0 ? 1.12 : 0.89;
    const rect = container.current?.getBoundingClientRect();
    if (!rect) return;
    const cursorX = event.clientX - rect.left;
    const cursorY = event.clientY - rect.top;
    setView((current) => {
      const nextK = Math.min(3, Math.max(0.35, current.k * factor));
      const worldX = (cursorX - current.x) / current.k;
      const worldY = (cursorY - current.y) / current.k;
      return {
        k: nextK,
        x: cursorX - worldX * nextK,
        y: cursorY - worldY * nextK,
      };
    });
  }

  function handleNodeKeyDown(event: ReactKeyboardEvent, nodeId: string) {
    if (event.key === "Enter" || event.key === " ") {
      event.preventDefault();
      setSelectedNodeId((current) => (current === nodeId ? undefined : nodeId));
      setSelectedEdgeKey(undefined);
    }
  }

  function clearSelection() {
    setSelectedNodeId(undefined);
    setSelectedEdgeKey(undefined);
  }

  useEffect(() => {
    const handler = (event: globalThis.KeyboardEvent) => {
      if (event.key === "Escape") clearSelection();
    };
    window.addEventListener("keydown", handler);
    return () => window.removeEventListener("keydown", handler);
  }, []);

  if (graph.isPending) return <div className="route-status">Opening Agent graph...</div>;
  if (graph.error) {
    return (
      <div className="route-status route-status--error">
        <section className="route-status-panel" role="alert">
          <p className="section-kicker">AGENT GRAPH</p>
          <h1>Could not load the graph.</h1>
          <p>{graph.error instanceof Error ? graph.error.message : "The Server returned no usable data."}</p>
          <button className="command-button command-button--accent" type="button" onClick={() => void graph.refetch()}>
            Retry
          </button>
        </section>
      </div>
    );
  }

  if (!nodes.length) {
    return (
      <div className="route-status">
        <section className="route-status-panel">
          <p className="section-kicker">AGENT GRAPH</p>
          <h1>No Agents yet.</h1>
          <p>Create an Agent and start a conversation; relationships appear here.</p>
        </section>
      </div>
    );
  }

  const totalInteractions = edges.reduce((sum, edge) => sum + edge.total_interactions, 0);

  return (
    <div className="agent-graph-page">
      <header className="agent-graph-header">
        <div>
          <h1>Agent graph</h1>
          <p>
            {nodes.length} Agents · {edges.length} relationships · {totalInteractions} interactions
          </p>
        </div>
        <div className="agent-graph-zoom" aria-label="Graph zoom controls">
          <button type="button" aria-label="Zoom in" title="Zoom in" onClick={() => setView((current) => ({ ...current, k: Math.min(3, current.k * 1.25) }))}>
            <Plus aria-hidden="true" />
          </button>
          <button type="button" aria-label="Zoom out" title="Zoom out" onClick={() => setView((current) => ({ ...current, k: Math.max(0.35, current.k * 0.8) }))}>
            <Minus aria-hidden="true" />
          </button>
          <button type="button" aria-label="Reset view" title="Reset view" onClick={() => setView(INITIAL_GRAPH_VIEW)}>
            <RotateCcw aria-hidden="true" />
          </button>
        </div>
      </header>
      <div className="agent-graph-layout">
        <div ref={container} className="agent-graph-canvas">
          <svg
            viewBox={`0 0 ${VIEW_WIDTH} ${VIEW_HEIGHT}`}
            role="application"
            aria-label="Agent coordination graph. Drag nodes or the background, and use the mouse wheel or buttons to zoom."
            onPointerDown={handlePointerDown}
            onPointerMove={handlePointerMove}
            onPointerUp={handlePointerUp}
            onPointerCancel={handlePointerUp}
            onWheel={handleWheel}
          >
            <g transform={`translate(${view.x} ${view.y}) scale(${view.k})`}>
              {edges.map((edge) => {
                const a = positions.get(edge.member_a_id);
                const b = positions.get(edge.member_b_id);
                if (!a || !b) return null;
                const key = edgeKey(edge);
                const active = selectedEdgeKey === key || selectedNodeId === a.member_id || selectedNodeId === b.member_id;
                const dimmed = (selectedNodeId || selectedEdgeKey) && !active;
                return (
                  <g
                    key={key}
                    className={`graph-edge${active ? " graph-edge--active" : ""}${dimmed ? " graph-edge--dimmed" : ""}`}
                    role="button"
                    tabIndex={0}
                    aria-label={`${edge.total_interactions} interactions between ${a.display_name} and ${b.display_name}`}
                    onClick={() => {
                      setSelectedEdgeKey((current) => (current === key ? undefined : key));
                      setSelectedNodeId(undefined);
                    }}
                    onKeyDown={(event) => {
                      if (event.key === "Enter" || event.key === " ") {
                        event.preventDefault();
                        setSelectedEdgeKey((current) => (current === key ? undefined : key));
                        setSelectedNodeId(undefined);
                      }
                    }}
                  >
                    <title>
                      {a.display_name} ↔ {b.display_name}: {edge.total_interactions} interactions
                      (DM {edge.dm_message_count}, mentions {edge.mention_a_to_b + edge.mention_b_to_a}, replies {edge.reply_a_to_b + edge.reply_b_to_a})
                    </title>
                    <line
                      className="graph-edge-line"
                      x1={a.x}
                      y1={a.y}
                      x2={b.x}
                      y2={b.y}
                    />
                    <line className="graph-edge-hit" x1={a.x} y1={a.y} x2={b.x} y2={b.y} />
                  </g>
                );
              })}
              {[...positions.values()].map((node) => {
                const active = selectedNodeId === node.member_id;
                const avatar = buildAgentIdenticon(node.member_id);
                const palette = identityPalette(node.member_id);
                const connected = selectedNodeId
                  ? edges.some(
                      (edge) =>
                        edge.member_a_id === selectedNodeId || edge.member_b_id === selectedNodeId,
                    )
                  : true;
                const dimmed = (selectedNodeId || selectedEdgeKey) && !active && !connected;
                return (
                  <g
                    key={node.member_id}
                    data-node-id={node.member_id}
                    transform={`translate(${node.x} ${node.y})`}
                    className={`graph-node${active ? " graph-node--active" : ""}${dimmed ? " graph-node--dimmed" : ""}`}
                    role="button"
                    tabIndex={0}
                    aria-label={`${node.display_name}, ${node.role_text}`}
                    onClick={() => {
                      setSelectedNodeId((current) => (current === node.member_id ? undefined : node.member_id));
                      setSelectedEdgeKey(undefined);
                    }}
                    onKeyDown={(event) => handleNodeKeyDown(event, node.member_id)}
                  >
                    <rect
                      className="graph-node-avatar"
                      x={-18}
                      y={-18}
                      width={36}
                      height={36}
                      fill={palette.background}
                    />
                    <g
                      transform="translate(-16 -16) scale(4)"
                      shapeRendering="crispEdges"
                      aria-hidden="true"
                    >
                      <rect width="8" height="8" fill={avatar.background} />
                      {avatar.cells.map(([x, y]) => (
                        <rect key={`${x}-${y}`} x={x} y={y} width="1" height="1" fill={avatar.foreground} />
                      ))}
                    </g>
                    <text className="graph-node-label" textAnchor="middle" y={27}>
                      {node.display_name}
                    </text>
                  </g>
                );
              })}
            </g>
          </svg>
        </div>
        <aside className="agent-graph-panel" aria-label="Agent graph details">
          {selectedNode ? (
            <NodeDetails
              node={selectedNode}
              edges={neighborEdges}
              positions={positions}
              onSelectEdge={(edge) => {
                setSelectedEdgeKey(edgeKey(edge));
                setSelectedNodeId(undefined);
              }}
              onClose={clearSelection}
            />
          ) : selectedEdge ? (
            <EdgeDetails edge={selectedEdge} positions={positions} onClose={clearSelection} />
          ) : (
            <GraphOverview
              nodes={nodes}
              edges={edges}
              totalInteractions={totalInteractions}
              spaceSlug={spaceSlug}
            />
          )}
        </aside>
      </div>
    </div>
  );
}

function GraphOverview({
  nodes,
  edges,
  totalInteractions,
  spaceSlug,
}: {
  nodes: AgentGraphNode[];
  edges: AgentGraphEdge[];
  totalInteractions: number;
  spaceSlug: string;
}) {
  const mostConnected = [...edges].sort((a, b) => b.total_interactions - a.total_interactions)[0];
  const nameOf = (id: string) => nodes.find((node) => node.member_id === id)?.display_name ?? "Agent";
  return (
    <div className="agent-graph-overview">
      <h2>Overview</h2>
      <dl>
        <div><dt>Agents</dt><dd>{nodes.length}</dd></div>
        <div><dt>Relationships</dt><dd>{edges.length}</dd></div>
        <div><dt>Interactions</dt><dd>{totalInteractions}</dd></div>
      </dl>
      {mostConnected ? (
        <p className="agent-graph-hint">
          Busiest pair: {nameOf(mostConnected.member_a_id)} ↔ {nameOf(mostConnected.member_b_id)} with{" "}
          {mostConnected.total_interactions} interactions.
        </p>
      ) : (
        <p className="agent-graph-hint">No interactions between Agents yet. DMs, mentions, and replies will connect them.</p>
      )}
      <p className="agent-graph-hint agent-graph-hint--muted">
        Select an Agent or an edge to inspect the communication chain. {spaceSlug}
      </p>
    </div>
  );
}

function NodeDetails({
  node,
  edges,
  positions,
  onSelectEdge,
  onClose,
}: {
  node: GraphLayoutNode;
  edges: AgentGraphEdge[];
  positions: Map<string, GraphLayoutNode>;
  onSelectEdge: (edge: AgentGraphEdge) => void;
  onClose: () => void;
}) {
  const sorted = [...edges].sort((a, b) => b.total_interactions - a.total_interactions);
  return (
    <div className="agent-graph-details">
      <div className="agent-graph-details-head">
        <PixelIdentity name={node.display_name} kind="agent" seed={node.member_id} />
        <div>
          <h2>{node.display_name}</h2>
          <p>{node.role_text}</p>
        </div>
        <button className="icon-button" type="button" aria-label="Close details" title="Close details" onClick={onClose}>
          <X aria-hidden="true" />
        </button>
      </div>
      <h3>Neighbors</h3>
      {sorted.length ? (
        <ul className="agent-graph-neighbors">
          {sorted.map((edge) => {
            const otherId = edge.member_a_id === node.member_id ? edge.member_b_id : edge.member_a_id;
            const other = positions.get(otherId);
            return (
              <li key={[edge.member_a_id, edge.member_b_id].sort().join(":")}>
                <button type="button" onClick={() => onSelectEdge(edge)}>
                  <span>{other?.display_name ?? "Agent"}</span>
                  <strong>{edge.total_interactions}</strong>
                  <small>{edge.dm_message_count} DM · {edge.mention_a_to_b + edge.mention_b_to_a} mention · {edge.reply_a_to_b + edge.reply_b_to_a} reply</small>
                </button>
              </li>
            );
          })}
        </ul>
      ) : (
        <p className="agent-graph-hint">This Agent has no relationships yet.</p>
      )}
    </div>
  );
}

function EdgeDetails({
  edge,
  positions,
  onClose,
}: {
  edge: AgentGraphEdge;
  positions: Map<string, GraphLayoutNode>;
  onClose: () => void;
}) {
  const a = positions.get(edge.member_a_id);
  const b = positions.get(edge.member_b_id);
  const chain = [...edge.recent_messages].sort(
    (left, right) => new Date(right.created_at).getTime() - new Date(left.created_at).getTime(),
  );
  return (
    <div className="agent-graph-details">
      <div className="agent-graph-details-head">
        <div>
          <h2>{a?.display_name ?? "Agent"} ↔ {b?.display_name ?? "Agent"}</h2>
          <p>{edge.total_interactions} interactions total</p>
        </div>
        <button className="icon-button" type="button" aria-label="Close details" title="Close details" onClick={onClose}>
          <X aria-hidden="true" />
        </button>
      </div>
      <dl className="agent-graph-edge-stats">
        <div><dt>DM messages</dt><dd>{edge.dm_message_count}</dd></div>
        <div><dt>{a?.display_name ?? "A"} → mentions</dt><dd>{edge.mention_a_to_b}</dd></div>
        <div><dt>{b?.display_name ?? "B"} → mentions</dt><dd>{edge.mention_b_to_a}</dd></div>
        <div><dt>{a?.display_name ?? "A"} → replies</dt><dd>{edge.reply_a_to_b}</dd></div>
        <div><dt>{b?.display_name ?? "B"} → replies</dt><dd>{edge.reply_b_to_a}</dd></div>
      </dl>
      <h3>Communication chain</h3>
      {chain.length ? (
        <ol className="agent-graph-chain">
          {chain.map((message) => (
            <CommunicationStep key={message.id} message={message} positions={positions} />
          ))}
        </ol>
      ) : (
        <p className="agent-graph-hint">No readable recent Messages; the viewer is not a member of this conversation.</p>
      )}
    </div>
  );
}

function CommunicationStep({
  message,
  positions,
}: {
  message: AgentGraphMessage;
  positions: Map<string, GraphLayoutNode>;
}) {
  const author = positions.get(message.author_member_id);
  const target = positions.get(message.target_member_id);
  const kindLabel = message.kind === "dm" ? "DM" : message.kind === "mention" ? "Mention" : "Reply";
  return (
    <li className="agent-graph-chain-step">
      <div className="agent-graph-chain-meta">
        <span className={`agent-graph-kind agent-graph-kind--${message.kind}`}>{kindLabel}</span>
        <strong>{author?.display_name ?? "Agent"}</strong>
        <span>→</span>
        <strong>{target?.display_name ?? "Agent"}</strong>
        <time dateTime={message.created_at}>{formatGraphTime(message.created_at)}</time>
      </div>
      <p>{truncate(message.body_markdown, 180)}</p>
    </li>
  );
}

function truncate(value: string, max: number): string {
  if (value.length <= max) return value;
  return `${value.slice(0, max).trimEnd()}…`;
}

function formatGraphTime(value: string): string {
  const date = new Date(value);
  if (Number.isNaN(date.getTime())) return value;
  return date.toLocaleString(undefined, {
    month: "short",
    day: "numeric",
    hour: "2-digit",
    minute: "2-digit",
  });
}
