import { useQuery } from "@tanstack/react-query";
import { Activity, ArrowDownToLine, ArrowUpFromLine, Gauge } from "lucide-react";
import { useState } from "react";

import { getComputerLlmUsage, type LlmUsage } from "../api/client";
import { BreakdownTable, StatCard, TokenUsageChart, formatCount } from "./TokenUsageChart";
import "./llmUsage.css";

export type LlmUsageRange = "24h" | "7d" | "30d";

const RANGES: Array<{ value: LlmUsageRange; label: string }> = [
  { value: "24h", label: "24h" },
  { value: "7d", label: "7d" },
  { value: "30d", label: "30d" },
];

export function LlmUsagePanel({
  computerId,
  online,
}: {
  computerId: string;
  online: boolean;
}) {
  const [range, setRange] = useState<LlmUsageRange>("24h");
  const usage = useQuery({
    queryKey: ["llm-usage", computerId, range],
    queryFn: () => getComputerLlmUsage(computerId, range),
    enabled: online,
    refetchInterval: 30_000,
    retry: false,
  });

  return (
    <section className="detail-panel llm-usage-panel" aria-labelledby="llm-usage-heading">
      <header className="detail-panel-heading">
        <div>
          <p className="detail-section-label">Local model telemetry</p>
          <h3 id="llm-usage-heading">LLM usage</h3>
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

      {!online ? (
        <p className="llm-usage-state" role="status">
          Computer is offline. Usage stored on the machine will appear when it reconnects.
        </p>
      ) : usage.isPending ? (
        <p className="llm-usage-state">Loading usage…</p>
      ) : usage.error ? (
        <p className="llm-usage-state" role="status">
          Usage is unavailable right now. The daemon may be restarting.
        </p>
      ) : usage.data.requests === 0 ? (
        <p className="llm-usage-state" role="status">
          No LLM usage recorded on this Computer yet.
        </p>
      ) : (
        <UsageBody usage={usage.data} />
      )}
    </section>
  );
}

function UsageBody({ usage }: { usage: LlmUsage }) {
  return (
    <div className="llm-usage-body">
      <div className="llm-usage-cards" aria-label="LLM usage summary">
        <StatCard icon={Activity} label="Requests" value={formatCount(usage.requests)} />
        <StatCard icon={ArrowUpFromLine} label="Input tokens" value={formatCount(usage.input_tokens)} />
        <StatCard icon={ArrowDownToLine} label="Output tokens" value={formatCount(usage.output_tokens)} />
        <StatCard
          icon={Gauge}
          label="Cache hit rate"
          value={`${Math.round(usage.cache_hit_rate * 100)}%`}
          detail={`${formatCount(usage.cached_input_tokens)} cached`}
        />
      </div>

      <TokenUsageChart series={usage.series} />
      <BreakdownTable title="By model" rows={usage.by_model} />
      <BreakdownTable title="By agent" rows={usage.by_agent} />
    </div>
  );
}
