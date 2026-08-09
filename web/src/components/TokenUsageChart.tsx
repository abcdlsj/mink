import { useMemo } from "react";

import type { LlmUsageBucket } from "../api/client";

export function TokenUsageChart({ series }: { series: LlmUsageBucket[] }) {
  const maxTokens = useMemo(
    () =>
      Math.max(
        1,
        ...series.flatMap((bucket) => [
          bucket.input_tokens,
          bucket.output_tokens,
          bucket.cached_input_tokens,
        ]),
      ),
    [series],
  );
  const points = useMemo(() => {
    const width = 620;
    const height = 150;
    const padding = 8;
    const step = series.length > 1 ? (width - padding * 2) / (series.length - 1) : 0;
    const toY = (value: number) => height - padding - (value / maxTokens) * (height - padding * 2);
    const xAt = (index: number) => padding + index * step;
    return {
      input: series
        .map((bucket, index) => `${xAt(index)},${toY(bucket.input_tokens)}`)
        .join(" "),
      cached: series
        .map((bucket, index) => `${xAt(index)},${toY(bucket.cached_input_tokens)}`)
        .join(" "),
      output: series
        .map((bucket, index) => `${xAt(index)},${toY(bucket.output_tokens)}`)
        .join(" "),
      area: `${padding},${150 - padding} ${series
        .map((bucket, index) => `${xAt(index)},${toY(bucket.input_tokens)}`)
        .join(" ")} ${padding + step * (series.length - 1)},${150 - padding}`,
    };
  }, [series, maxTokens]);

  return (
    <figure
      className="llm-usage-chart"
      aria-label={`Token usage over the selected period; peak ${formatCount(maxTokens)} tokens`}
    >
      <svg
        viewBox="0 0 620 150"
        role="img"
        aria-label="Token usage curve: input, cached input, and output tokens per bucket"
      >
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
  );
}

export function StatCard({
  icon: Icon,
  label,
  value,
  detail,
}: {
  icon: typeof import("lucide-react").Activity;
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

export function BreakdownTable({
  title,
  rows,
}: {
  title: string;
  rows: Array<{ key: string; requests: number; input_tokens: number; output_tokens: number; cached_input_tokens: number }>;
}) {
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

export function formatCount(value: number): string {
  if (value < 1000) return String(value);
  if (value < 1_000_000) return `${(value / 1000).toFixed(1)}k`;
  return `${(value / 1_000_000).toFixed(2)}M`;
}
