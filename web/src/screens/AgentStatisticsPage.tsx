import { useMemo, useState } from "react";
import { useQueries, useQuery } from "@tanstack/react-query";
import { useParams } from "@tanstack/react-router";
import { Activity, ArrowDownToLine, ArrowUpFromLine, Gauge } from "lucide-react";

import {
  getComputerLlmUsage,
  getSpaceBySlug,
  listAgents,
  listComputers,
  type LlmUsageAgentSeries,
  type LlmUsageBucket,
} from "../api/client";
import { PixelIdentity } from "../components/PixelIdentity";
import { BreakdownTable, StatCard, TokenUsageChart, formatCount } from "../components/TokenUsageChart";
import "./agentStatistics.css";

type Range = "24h" | "7d" | "30d";

const RANGES: Array<{ value: Range; label: string }> = [
  { value: "24h", label: "24h" },
  { value: "7d", label: "7d" },
  { value: "30d", label: "30d" },
];

interface MergedAgent {
  member_id: string;
  requests: number;
  input_tokens: number;
  output_tokens: number;
  cached_input_tokens: number;
  series: LlmUsageBucket[];
}

interface MergedModel {
  key: string;
  requests: number;
  input_tokens: number;
  output_tokens: number;
  cached_input_tokens: number;
}

export function AgentStatisticsPage() {
  const { spaceSlug } = useParams({ from: "/s/$spaceSlug/insights/stats" });
  return <AgentStatisticsWorkspace spaceSlug={spaceSlug} />;
}

export function AgentStatisticsWorkspace({ spaceSlug }: { spaceSlug: string }) {
  const [range, setRange] = useState<Range>("24h");
  const [selectedMemberId, setSelectedMemberId] = useState<string>();
  const space = useQuery({
    queryKey: ["space", spaceSlug],
    queryFn: () => getSpaceBySlug(spaceSlug),
  });
  const agents = useQuery({
    queryKey: ["agents-stats", space.data?.id],
    queryFn: () => listAgents(space.data!.id),
    enabled: Boolean(space.data),
  });
  const computers = useQuery({
    queryKey: ["computers-stats", space.data?.id],
    queryFn: () => listComputers(space.data!.id),
    enabled: Boolean(space.data),
  });
  const usageResults = useQueries({
    queries: (computers.data ?? []).map((computer) => ({
      queryKey: ["llm-usage", computer.id, range],
      queryFn: () => getComputerLlmUsage(computer.id, range),
      enabled: computer.status === "online",
      retry: false,
    })),
  });

  const merged = useMemo(() => {
    const byAgent = new Map<string, MergedAgent>();
    const byModel = new Map<string, MergedModel>();
    for (const result of usageResults) {
      const data = result.data;
      if (!data) continue;
      for (const entry of data.by_agent_series) {
        byAgent.set(entry.agent_id, mergeAgent(byAgent.get(entry.agent_id), entry));
      }
      for (const row of data.by_model) {
        const previous = byModel.get(row.key);
        byModel.set(row.key, {
          key: row.key,
          requests: (previous?.requests ?? 0) + row.requests,
          input_tokens: (previous?.input_tokens ?? 0) + row.input_tokens,
          output_tokens: (previous?.output_tokens ?? 0) + row.output_tokens,
          cached_input_tokens: (previous?.cached_input_tokens ?? 0) + row.cached_input_tokens,
        });
      }
    }
    return {
      agents: [...byAgent.values()].sort((a, b) => b.input_tokens - a.input_tokens),
      models: [...byModel.values()].sort((a, b) => b.input_tokens - a.input_tokens),
    };
  }, [usageResults]);

  const agentName = (memberId: string) =>
    agents.data?.find((agent) => agent.member_id === memberId)?.name ?? memberId.slice(0, 8);
  const selectedAgent =
    merged.agents.find((agent) => agent.member_id === selectedMemberId) ??
    merged.agents[0] ??
    null;

  return (
    <div className="agent-statistics">
      <header className="agent-statistics-header">
        <div>
          <h1>Agent statistics</h1>
          <p>LLM token usage per Agent, aggregated from this Space's Computers.</p>
        </div>
        <div className="llm-usage-range" aria-label="Usage period">
          {RANGES.map((option) => (
            <button
              key={option.value}
              type="button"
              className={range === option.value ? "llm-usage-range--active" : undefined}
              aria-pressed={range === option.value}
              onClick={() => setRange(option.value)}
            >
              {option.label}
            </button>
          ))}
        </div>
      </header>

      {merged.agents.length === 0 || !selectedAgent ? (
        <p className="agent-statistics-empty" role="status">
          No LLM usage recorded yet. Usage appears after an Agent runs a model turn.
        </p>
      ) : (
        <div className="agent-statistics-layout">
          <ul className="agent-statistics-list" aria-label="Agents with usage">
            {merged.agents.map((agent) => (
              <li key={agent.member_id}>
                <button
                  type="button"
                  className={agent.member_id === selectedAgent.member_id ? "agent-statistics-item--active" : undefined}
                  onClick={() => setSelectedMemberId(agent.member_id)}
                >
                  <PixelIdentity name={agentName(agent.member_id)} kind="agent" seed={agent.member_id} />
                  <span>
                    <strong>{agentName(agent.member_id)}</strong>
                    <small>{formatCount(agent.input_tokens)} input · {formatCount(agent.output_tokens)} output</small>
                  </span>
                </button>
              </li>
            ))}
          </ul>

          <section className="agent-statistics-detail" aria-label={`${agentName(selectedAgent.member_id)} usage`}>
            <div className="llm-usage-cards" aria-label="Agent usage summary">
              <StatCard icon={Activity} label="Requests" value={formatCount(selectedAgent.requests)} />
              <StatCard icon={ArrowUpFromLine} label="Input tokens" value={formatCount(selectedAgent.input_tokens)} />
              <StatCard icon={ArrowDownToLine} label="Output tokens" value={formatCount(selectedAgent.output_tokens)} />
              <StatCard
                icon={Gauge}
                label="Cached input"
                value={formatCount(selectedAgent.cached_input_tokens)}
                detail={`${Math.round((selectedAgent.cached_input_tokens / Math.max(1, selectedAgent.input_tokens)) * 100)}% of input`}
              />
            </div>
            <TokenUsageChart series={selectedAgent.series} />
            <BreakdownTable title="By model" rows={merged.models} />
          </section>
        </div>
      )}
    </div>
  );
}

function mergeAgent(
  previous: MergedAgent | undefined,
  entry: LlmUsageAgentSeries,
): MergedAgent {
  const series = new Map(previous?.series.map((bucket) => [bucket.bucket, bucket]));
  for (const bucket of entry.series) {
    const existing = series.get(bucket.bucket);
    series.set(bucket.bucket, {
      bucket: bucket.bucket,
      requests: (existing?.requests ?? 0) + bucket.requests,
      input_tokens: (existing?.input_tokens ?? 0) + bucket.input_tokens,
      output_tokens: (existing?.output_tokens ?? 0) + bucket.output_tokens,
      cached_input_tokens: (existing?.cached_input_tokens ?? 0) + bucket.cached_input_tokens,
    });
  }
  return {
    member_id: entry.agent_id,
    requests: (previous?.requests ?? 0) + entry.requests,
    input_tokens: (previous?.input_tokens ?? 0) + entry.input_tokens,
    output_tokens: (previous?.output_tokens ?? 0) + entry.output_tokens,
    cached_input_tokens: (previous?.cached_input_tokens ?? 0) + entry.cached_input_tokens,
    series: [...series.values()].sort((a, b) => a.bucket.localeCompare(b.bucket)),
  };
}
