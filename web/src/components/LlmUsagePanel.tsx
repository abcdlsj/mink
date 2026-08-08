import { useQuery } from "@tanstack/react-query";
import { Activity, ArrowDownToLine, ArrowUpFromLine, Gauge } from "lucide-react";
import { useMemo, useState } from "react";

import { getComputerLlmUsage, type LlmUsage } from "../api/client";
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
  const maxTokens = useMemo(
    () =>
      Math.max(
        1,
        ...usage.series.flatMap((bucket) => [
          bucket.input_tokens,
          bucket.output_tokens,
          bucket.cached_input_tokens,
        ]),
      ),
    [usage.series],
  );
  const points = useMemo(() => {
    const width = 620;
    const height = 150;
    const padding = 8;
    const step = usage.series.length > 1 ? (width - padding * 2) / (usage.series.length - 1) : 0;
    const toY = (value: number) => height - padding - (value / maxTokens) * (height - padding * 2);
    const xAt = (index: number) => padding + index * step;
    return {
      input: usage.series
        .map((bucket, index) => `${xAt(index)},${toY(bucket.input_tokens)}`)
        .join(" "),
      cached: usage.series
        .map((bucket, index) => `${xAt(index)},${toY(bucket.cached_input_tokens)}`)
        .join(" "),
      output: usage.series
        .map((bucket, index) => `${xAt(index)},${toY(bucket.output_tokens)}`)
        .join(" "),
      area: `${padding},${150 - padding} ${usage.series
        .map((bucket, index) => `${xAt(index)},${toY(bucket.input_tokens)}`)
        .join(" ")} ${padding + step * (usage.series.length - 1)},${150 - padding}`,
    };
  }, [usage.series, maxTokens]);

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

      <figure className="llm-usage-chart" aria-label={`Token usage over the selected period; peak ${formatCount(maxTokens)} tokens`}>
        <svg viewBox="0 0 620 150" role="img" aria-label="Token usage curve: input, cached input, and output tokens per bucket">
          <polygon points={points.area} className="llm-usage-area" />
          <polyline points={points.input} className="llm-usage-line llm-usage-line--input" />
          <polyline points={points.cached} className="llm-usage-line llm-usage-line--cached" />
          <polyline points={points.output} className="llm-usage-line llm-usage-line--output" />
        </svg>
        <figcaption>
          <span className="llm-usage-legend llm-usage-legend--input">Input</span>
          <span className="llm-usage-legend llm-usage-legend--cached">Cached input</span>
          <span className="llm-usage-legend llm-usage-legend--output">Output</span>
        </figcaption>
      </figure>

      <BreakdownTable title="By model" rows={usage.by_model} />
      <BreakdownTable title="By agent" rows={usage.by_agent} />
    </div>
  );
}

function StatCard({
  icon: Icon,
  label,
  value,
  detail,
}: {
  icon: typeof Activity;
  label: string;
  value: string;
  detail?: string;
}) {
  return (
    <div className="llm-usage-card">
      <Icon aria-hidden="true" />
      <span>{label}</span>
      <strong>{value}</strong>
      {detail ? <small>{detail}</small> : null}
    </div>
  );
}

function BreakdownTable({ title, rows }: { title: string; rows: LlmUsage["by_model"] }) {
  if (!rows.length) return null;
  return (
    <div className="llm-usage-breakdown">
      <h4>{title}</h4>
      <table>
        <thead>
          <tr>
            <th scope="col">Name</th>
            <th scope="col">Requests</th>
            <th scope="col">Input</th>
            <th scope="col">Output</th>
            <th scope="col">Cached</th>
          </tr>
        </thead>
        <tbody>
          {rows.map((row) => (
            <tr key={row.key}>
              <th scope="row">{row.key}</th>
              <td>{formatCount(row.requests)}</td>
              <td>{formatCount(row.input_tokens)}</td>
              <td>{formatCount(row.output_tokens)}</td>
              <td>{formatCount(row.cached_input_tokens)}</td>
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  );
}

function formatCount(value: number): string {
  if (value < 1000) return String(value);
  if (value < 1_000_000) return `${(value / 1000).toFixed(1)}k`;
  return `${(value / 1_000_000).toFixed(2)}M`;
}
