import type { AgentGraphEdge, AgentGraphNode } from "../api/client";

export interface GraphLayoutNode extends AgentGraphNode {
  x: number;
  y: number;
  vx: number;
  vy: number;
}

export function layoutGraph(
  nodes: AgentGraphNode[],
  edges: AgentGraphEdge[],
  width: number,
  height: number,
  iterations: number,
): GraphLayoutNode[] {
  if (!nodes.length) return [];
  const radius = Math.min(width, height) * 0.34;
  const laid: GraphLayoutNode[] = nodes.map((node, index) => {
    const angle = (2 * Math.PI * index) / nodes.length;
    return {
      ...node,
      x: width / 2 + Math.cos(angle) * radius,
      y: height / 2 + Math.sin(angle) * radius,
      vx: 0,
      vy: 0,
    };
  });
  const byId = new Map(laid.map((node) => [node.member_id, node]));
  const links = edges
    .map((edge) => ({
      a: byId.get(edge.member_a_id),
      b: byId.get(edge.member_b_id),
      weight: Math.max(0.35, Math.min(2.2, Math.log2(1 + edge.total_interactions))),
    }))
    .filter((link): link is { a: GraphLayoutNode; b: GraphLayoutNode; weight: number } =>
      Boolean(link.a && link.b),
    );
  const repulsion = 2400;
  const spring = 0.05;
  const restLength = 160;
  const gravity = 0.012;
  const damping = 0.72;

  for (let step = 0; step < iterations; step += 1) {
    for (let i = 0; i < laid.length; i += 1) {
      for (let j = i + 1; j < laid.length; j += 1) {
        const a = laid[i];
        const b = laid[j];
        let dx = a.x - b.x;
        let dy = a.y - b.y;
        let distanceSq = dx * dx + dy * dy;
        if (distanceSq < 1) {
          dx = (Math.random() - 0.5) * 2;
          dy = (Math.random() - 0.5) * 2;
          distanceSq = dx * dx + dy * dy || 1;
        }
        const force = Math.min(180, repulsion / distanceSq);
        const distance = Math.sqrt(distanceSq);
        const fx = (dx / distance) * force;
        const fy = (dy / distance) * force;
        a.vx += fx;
        a.vy += fy;
        b.vx -= fx;
        b.vy -= fy;
      }
    }
    for (const link of links) {
      const dx = link.b.x - link.a.x;
      const dy = link.b.y - link.a.y;
      const distance = Math.max(1, Math.sqrt(dx * dx + dy * dy));
      const force = (distance - restLength) * spring * link.weight;
      const fx = (dx / distance) * force;
      const fy = (dy / distance) * force;
      link.a.vx += fx;
      link.a.vy += fy;
      link.b.vx -= fx;
      link.b.vy -= fy;
    }
    for (const node of laid) {
      node.vx += (width / 2 - node.x) * gravity;
      node.vy += (height / 2 - node.y) * gravity;
      node.vx *= damping;
      node.vy *= damping;
      node.x += node.vx;
      node.y += node.vy;
      node.x = Math.max(30, Math.min(width - 30, node.x));
      node.y = Math.max(30, Math.min(height - 30, node.y));
    }
  }
  return laid;
}
