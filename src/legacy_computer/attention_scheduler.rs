use std::time::Duration;

use anyhow::{Context, Result};
use sqlx::SqlitePool;
use tokio_util::sync::CancellationToken;
use url::Url;
use uuid::Uuid;

use super::ConnectionTaskExit;
use crate::computer_protocol::{AgentClaimResponse, HostedAgent};

pub(super) const ATTENTION_PREFETCH_RUNS: usize = 1;
const ATTENTION_POLL_INTERVAL: Duration = Duration::from_millis(250);

pub(super) async fn attention_scheduler_task(
    client: reqwest::Client,
    server: Url,
    computer_id: Uuid,
    token: String,
    database: SqlitePool,
    max_claimed_runs: usize,
    cancellation: CancellationToken,
) -> Result<ConnectionTaskExit> {
    let mut interval = tokio::time::interval(ATTENTION_POLL_INTERVAL);
    let mut scheduler = AttentionSchedulerState {
        database,
        max_claimed_runs,
        next_agent_index: 0,
        pending_claims: std::collections::HashSet::new(),
    };
    interval.set_missed_tick_behavior(tokio::time::MissedTickBehavior::Delay);
    interval.tick().await;
    loop {
        tokio::select! {
            _ = cancellation.cancelled() => return Ok(ConnectionTaskExit::Cancelled),
            _ = interval.tick() => {}
        }
        let result = tokio::select! {
            _ = cancellation.cancelled() => return Ok(ConnectionTaskExit::Cancelled),
            result = poll_agent_inbox(
                &client,
                &server,
                computer_id,
                &token,
                &mut scheduler,
            ) => result,
        };
        if let Err(error) = result {
            tracing::warn!(computer_id = %computer_id, error = %error, "Agent Inbox poll failed");
        }
    }
}

pub(super) struct AttentionSchedulerState {
    pub(super) database: SqlitePool,
    pub(super) max_claimed_runs: usize,
    pub(super) next_agent_index: usize,
    pub(super) pending_claims: std::collections::HashSet<Uuid>,
}

pub(super) async fn poll_agent_inbox(
    client: &reqwest::Client,
    server: &Url,
    computer_id: Uuid,
    token: &str,
    scheduler: &mut AttentionSchedulerState,
) -> Result<()> {
    let local_runs: Vec<(String, String)> =
        sqlx::query_as("SELECT run_id, status FROM local_agent_runs")
            .fetch_all(&scheduler.database)
            .await?;
    let local_run_ids = local_runs
        .iter()
        .filter_map(|(run_id, _)| Uuid::parse_str(run_id).ok())
        .collect::<std::collections::HashSet<_>>();
    scheduler
        .pending_claims
        .retain(|run_id| !local_run_ids.contains(run_id));
    let active_runs = local_runs
        .iter()
        .filter(|(_, status)| !matches!(status.as_str(), "completed" | "failed" | "canceled"))
        .count();
    let mut claim_budget = scheduler
        .max_claimed_runs
        .saturating_sub(active_runs.saturating_add(scheduler.pending_claims.len()));
    if claim_budget == 0 {
        return Ok(());
    }
    let agents: Vec<HostedAgent> = client
        .get(server.join(&format!("/api/v1/computers/{computer_id}/agents"))?)
        .bearer_auth(token)
        .send()
        .await?
        .error_for_status()?
        .json()
        .await?;
    let agents = agents
        .into_iter()
        .filter(|agent| agent.desired_lifecycle == "active" && agent.provision_status == "ready")
        .collect::<Vec<_>>();
    if agents.is_empty() {
        scheduler.next_agent_index = 0;
        return Ok(());
    }
    let start = scheduler.next_agent_index % agents.len();
    let mut visited = 0;
    while visited < agents.len() && claim_budget > 0 {
        let index = (start + visited) % agents.len();
        let agent = &agents[index];
        let claim: AgentClaimResponse = client
            .post(server.join(&format!(
                "/api/v1/computers/{computer_id}/agents/{}/inbox/claim",
                agent.member_id
            ))?)
            .bearer_auth(token)
            .json(&serde_json::json!({}))
            .send()
            .await?
            .error_for_status()?
            .json()
            .await?;
        if claim.claimed {
            let run_id = claim.run_id.context("claimed Agent Run has no run id")?;
            scheduler.pending_claims.insert(run_id);
            claim_budget -= 1;
            tracing::info!(
                computer_id = %computer_id,
                agent_member_id = %agent.member_id,
                run_id = %run_id,
                inbox_items_count = claim.inbox_item_ids.len(),
                "Agent Inbox claimed"
            );
        }
        visited += 1;
    }
    scheduler.next_agent_index = (start + visited) % agents.len();
    Ok(())
}
