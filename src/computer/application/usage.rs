use std::collections::BTreeMap;

use time::{OffsetDateTime, format_description::well_known::Rfc3339};
use uuid::Uuid;

use crate::ids::{AgentId, RunId};

/// One LLM invocation recorded by the Computer. Stored only in the local daemon database; the
/// Server never persists this data.
#[derive(Clone, Debug, Eq, PartialEq)]
pub(in crate::computer) struct LlmUsageRecord {
    pub(in crate::computer) id: Uuid,
    pub(in crate::computer) run_id: RunId,
    pub(in crate::computer) agent_id: AgentId,
    pub(in crate::computer) driver_kind: String,
    pub(in crate::computer) model: Option<String>,
    pub(in crate::computer) input_tokens: i64,
    pub(in crate::computer) output_tokens: i64,
    pub(in crate::computer) cached_input_tokens: i64,
    pub(in crate::computer) cache_write_tokens: i64,
    pub(in crate::computer) duration_ms: Option<i64>,
    pub(in crate::computer) created_at: OffsetDateTime,
}

#[derive(Clone, Debug, Default, Eq, PartialEq)]
pub(in crate::computer) struct LlmUsageBucket {
    pub(in crate::computer) bucket: String,
    pub(in crate::computer) requests: u64,
    pub(in crate::computer) input_tokens: u64,
    pub(in crate::computer) output_tokens: u64,
    pub(in crate::computer) cached_input_tokens: u64,
}

#[derive(Clone, Debug, Default, Eq, PartialEq)]
pub(in crate::computer) struct LlmUsageBreakdown {
    pub(in crate::computer) key: String,
    pub(in crate::computer) requests: u64,
    pub(in crate::computer) input_tokens: u64,
    pub(in crate::computer) output_tokens: u64,
    pub(in crate::computer) cached_input_tokens: u64,
}

#[derive(Clone, Debug, Eq, PartialEq)]
pub(in crate::computer) struct LlmUsageAgentSeries {
    pub(in crate::computer) agent_id: AgentId,
    pub(in crate::computer) requests: u64,
    pub(in crate::computer) input_tokens: u64,
    pub(in crate::computer) output_tokens: u64,
    pub(in crate::computer) cached_input_tokens: u64,
    pub(in crate::computer) series: Vec<LlmUsageBucket>,
}

#[derive(Clone, Debug, Eq, PartialEq)]
pub(in crate::computer) struct LlmUsageAgentModelBreakdown {
    pub(in crate::computer) agent_id: AgentId,
    pub(in crate::computer) model: String,
    pub(in crate::computer) requests: u64,
    pub(in crate::computer) input_tokens: u64,
    pub(in crate::computer) output_tokens: u64,
    pub(in crate::computer) cached_input_tokens: u64,
}

#[derive(Clone, Debug, Default, PartialEq)]
pub(in crate::computer) struct LlmUsageSummary {
    pub(in crate::computer) requests: u64,
    pub(in crate::computer) input_tokens: u64,
    pub(in crate::computer) output_tokens: u64,
    pub(in crate::computer) cached_input_tokens: u64,
    pub(in crate::computer) cache_write_tokens: u64,
    pub(in crate::computer) cache_hit_rate: f64,
    pub(in crate::computer) first_at: Option<OffsetDateTime>,
    pub(in crate::computer) last_at: Option<OffsetDateTime>,
    pub(in crate::computer) series: Vec<LlmUsageBucket>,
    pub(in crate::computer) by_model: Vec<LlmUsageBreakdown>,
    pub(in crate::computer) by_agent: Vec<LlmUsageBreakdown>,
    pub(in crate::computer) by_agent_series: Vec<LlmUsageAgentSeries>,
    pub(in crate::computer) by_agent_model: Vec<LlmUsageAgentModelBreakdown>,
}

/// Aggregates raw local usage rows into the dashboard payload. Buckets are hourly for ranges of up
/// to 48 hours and daily beyond that, keeping the curve readable at any zoom level.
pub(in crate::computer) fn summarize(
    records: &[LlmUsageRecord],
    range_hours: i64,
) -> LlmUsageSummary {
    let hourly = range_hours <= 48;
    let mut summary = LlmUsageSummary::default();
    let mut by_model: BTreeMap<String, LlmUsageBreakdown> = BTreeMap::new();
    let mut by_agent: BTreeMap<String, LlmUsageBreakdown> = BTreeMap::new();
    let mut by_agent_model: BTreeMap<(AgentId, String), LlmUsageAgentModelBreakdown> =
        BTreeMap::new();
    let mut by_agent_records: BTreeMap<AgentId, Vec<&LlmUsageRecord>> = BTreeMap::new();

    for record in records {
        summary.requests += 1;
        summary.input_tokens += as_u64(record.input_tokens);
        summary.output_tokens += as_u64(record.output_tokens);
        summary.cached_input_tokens += as_u64(record.cached_input_tokens);
        summary.cache_write_tokens += as_u64(record.cache_write_tokens);
        summary.first_at = Some(
            summary
                .first_at
                .map_or(record.created_at, |first| first.min(record.created_at)),
        );
        summary.last_at = Some(
            summary
                .last_at
                .map_or(record.created_at, |last| last.max(record.created_at)),
        );

        let model = record.model.clone().unwrap_or_else(|| "unknown".to_owned());
        let entry = by_model.entry(model.clone()).or_insert(LlmUsageBreakdown {
            key: model.clone(),
            ..Default::default()
        });
        add_breakdown(entry, record);

        let agent = record.agent_id.to_string();
        let entry = by_agent.entry(agent.clone()).or_insert(LlmUsageBreakdown {
            key: agent,
            ..Default::default()
        });
        add_breakdown(entry, record);
        let model_entry = by_agent_model
            .entry((record.agent_id, model.clone()))
            .or_insert_with(|| LlmUsageAgentModelBreakdown {
                agent_id: record.agent_id,
                model: model.clone(),
                requests: 0,
                input_tokens: 0,
                output_tokens: 0,
                cached_input_tokens: 0,
            });
        model_entry.requests += 1;
        model_entry.input_tokens += as_u64(record.input_tokens);
        model_entry.output_tokens += as_u64(record.output_tokens);
        model_entry.cached_input_tokens += as_u64(record.cached_input_tokens);
        by_agent_records
            .entry(record.agent_id)
            .or_default()
            .push(record);
    }

    summary.cache_hit_rate = if summary.input_tokens > 0 {
        (summary.cached_input_tokens as f64 / summary.input_tokens as f64).min(1.0)
    } else {
        0.0
    };
    summary.series = bucket_series(&records.iter().collect::<Vec<_>>(), hourly);
    summary.by_model = by_model.into_values().collect();
    summary.by_agent = by_agent.into_values().collect();
    summary.by_agent_model = by_agent_model.into_values().collect();
    summary.by_agent_series = by_agent_records
        .into_iter()
        .map(|(agent_id, agent_records)| {
            let mut totals = LlmUsageAgentSeries {
                agent_id,
                requests: 0,
                input_tokens: 0,
                output_tokens: 0,
                cached_input_tokens: 0,
                series: Vec::new(),
            };
            for record in &agent_records {
                totals.requests += 1;
                totals.input_tokens += as_u64(record.input_tokens);
                totals.output_tokens += as_u64(record.output_tokens);
                totals.cached_input_tokens += as_u64(record.cached_input_tokens);
            }
            totals.series = bucket_series(&agent_records, hourly);
            totals
        })
        .collect();
    summary
}

fn bucket_series(records: &[&LlmUsageRecord], hourly: bool) -> Vec<LlmUsageBucket> {
    let mut series: BTreeMap<String, LlmUsageBucket> = BTreeMap::new();
    for record in records {
        let bucket = bucket_key(record.created_at, hourly);
        let entry = series.entry(bucket.clone()).or_insert(LlmUsageBucket {
            bucket,
            ..Default::default()
        });
        entry.requests += 1;
        entry.input_tokens += as_u64(record.input_tokens);
        entry.output_tokens += as_u64(record.output_tokens);
        entry.cached_input_tokens += as_u64(record.cached_input_tokens);
    }
    series.into_values().collect()
}

fn add_breakdown(entry: &mut LlmUsageBreakdown, record: &LlmUsageRecord) {
    entry.requests += 1;
    entry.input_tokens += as_u64(record.input_tokens);
    entry.output_tokens += as_u64(record.output_tokens);
    entry.cached_input_tokens += as_u64(record.cached_input_tokens);
}

fn as_u64(value: i64) -> u64 {
    u64::try_from(value).unwrap_or_default()
}

fn bucket_key(value: OffsetDateTime, hourly: bool) -> String {
    let seconds = if hourly {
        value.unix_timestamp() - value.unix_timestamp().rem_euclid(3600)
    } else {
        let midnight = value.date().midnight().assume_utc();
        midnight.unix_timestamp()
    };
    OffsetDateTime::from_unix_timestamp(seconds)
        .ok()
        .and_then(|time| time.format(&Rfc3339).ok())
        .unwrap_or_else(|| "unknown".to_owned())
}

#[cfg(test)]
mod tests {
    use super::*;
    use uuid::Uuid;

    fn record(
        agent: u128,
        run: u128,
        model: &str,
        input: i64,
        output: i64,
        cached: i64,
        created: i64,
    ) -> LlmUsageRecord {
        LlmUsageRecord {
            id: Uuid::now_v7(),
            run_id: RunId::from_uuid(Uuid::from_u128(run)),
            agent_id: AgentId::from_uuid(Uuid::from_u128(agent)),
            driver_kind: "builtin".to_owned(),
            model: Some(model.to_owned()),
            input_tokens: input,
            output_tokens: output,
            cached_input_tokens: cached,
            cache_write_tokens: 0,
            duration_ms: Some(1200),
            created_at: OffsetDateTime::from_unix_timestamp(created).expect("timestamp"),
        }
    }

    #[test]
    fn summarize_totals_and_cache_rate() {
        let records = vec![
            record(1, 10, "deepseek-v4-pro", 1000, 200, 600, 1000),
            record(1, 10, "deepseek-v4-pro", 2000, 300, 400, 4600),
        ];
        let summary = summarize(&records, 24);
        assert_eq!(summary.requests, 2);
        assert_eq!(summary.input_tokens, 3000);
        assert_eq!(summary.output_tokens, 500);
        assert_eq!(summary.cached_input_tokens, 1000);
        assert!((summary.cache_hit_rate - 1000.0 / 3000.0).abs() < 1e-9);
        assert_eq!(summary.series.len(), 2);
        assert_eq!(summary.by_model.len(), 1);
        assert_eq!(summary.by_agent.len(), 1);
    }

    #[test]
    fn daily_buckets_collapse_hours() {
        let records = vec![
            record(1, 10, "model", 100, 10, 0, 1_700_000_000),
            record(1, 10, "model", 200, 20, 0, 1_700_003_600),
            record(1, 10, "model", 300, 30, 0, 1_700_086_400),
        ];
        let summary = summarize(&records, 168);
        assert_eq!(summary.series.len(), 2);
        assert_eq!(summary.series[0].input_tokens, 300);
        assert_eq!(summary.series[1].input_tokens, 300);
    }

    #[test]
    fn empty_records_produce_empty_summary() {
        let summary = summarize(&[], 24);
        assert_eq!(summary.requests, 0);
        assert!(summary.series.is_empty());
        assert!(summary.by_model.is_empty());
        assert!(summary.by_agent_series.is_empty());
        assert!(summary.by_agent_model.is_empty());
        assert_eq!(summary.cache_hit_rate, 0.0);
    }

    #[test]
    fn summarize_splits_series_per_agent() {
        let records = vec![
            record(1, 10, "model", 1000, 100, 500, 1000),
            record(1, 10, "model", 2000, 200, 0, 4600),
            record(2, 11, "model", 500, 50, 100, 1000),
        ];
        let summary = summarize(&records, 24);
        assert_eq!(summary.by_agent_series.len(), 2);
        let first = summary
            .by_agent_series
            .iter()
            .find(|entry| entry.agent_id == AgentId::from_uuid(Uuid::from_u128(1)))
            .expect("agent one series");
        assert_eq!(first.requests, 2);
        assert_eq!(first.input_tokens, 3000);
        assert_eq!(first.output_tokens, 300);
        assert_eq!(first.series.len(), 2);
        assert_eq!(first.series[0].cached_input_tokens, 500);
        assert_eq!(summary.by_agent_model.len(), 2);
    }
}
