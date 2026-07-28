use std::time::Duration;

use anyhow::Result;
use serde::Deserialize;
use sqlx::SqlitePool;
use time::OffsetDateTime;
use tokio_util::sync::CancellationToken;
use url::Url;
use uuid::Uuid;

use super::ConnectionTaskExit;

#[derive(Deserialize)]
pub(super) struct AgentLeaseResponse {
    #[serde(with = "time::serde::rfc3339")]
    ownership_lease_expires_at: OffsetDateTime,
}

pub(super) async fn lease_renewer_task(
    client: reqwest::Client,
    server: Url,
    computer_id: Uuid,
    token: String,
    database: SqlitePool,
    cancellation: CancellationToken,
) -> Result<ConnectionTaskExit> {
    let mut interval = tokio::time::interval(Duration::from_secs(60));
    interval.set_missed_tick_behavior(tokio::time::MissedTickBehavior::Delay);
    interval.tick().await;
    loop {
        tokio::select! {
            _ = cancellation.cancelled() => return Ok(ConnectionTaskExit::Cancelled),
            _ = interval.tick() => {}
        }
        let result = tokio::select! {
            _ = cancellation.cancelled() => return Ok(ConnectionTaskExit::Cancelled),
            result = renew_active_run_leases(&client, &server, computer_id, &token, &database) => result,
        };
        if let Err(error) = result {
            tracing::warn!(computer_id = %computer_id, error = %error, "Agent Inbox lease renewal failed");
        }
    }
}

pub(super) async fn renew_active_run_leases(
    client: &reqwest::Client,
    server: &Url,
    computer_id: Uuid,
    token: &str,
    database: &SqlitePool,
) -> Result<()> {
    let runs: Vec<(String, String, String)> = sqlx::query_as(
        "SELECT run_id, agent_member_id, fencing_token FROM local_agent_runs \
         WHERE status IN ('queued', 'running') ORDER BY run_id",
    )
    .fetch_all(database)
    .await?;
    for (run_id, agent_id, fencing_token) in runs {
        let run_id = Uuid::parse_str(&run_id)?;
        let agent_id = Uuid::parse_str(&agent_id)?;
        let response = client
            .post(server.join(&format!(
                "/api/v1/computers/{computer_id}/agents/{agent_id}/inbox/renew"
            ))?)
            .bearer_auth(token)
            .json(&serde_json::json!({ "run_id": run_id, "fencing_token": fencing_token }))
            .send()
            .await;
        match response {
            Ok(response) if response.status().is_success() => {
                let response: AgentLeaseResponse = response.json().await?;
                sqlx::query(
                    "UPDATE local_agent_runs SET ownership_lease_expires_at = ?2 WHERE run_id = ?1",
                )
                .bind(run_id.to_string())
                .bind(response.ownership_lease_expires_at.to_string())
                .execute(database)
                .await?;
                tracing::info!(computer_id = %computer_id, agent_member_id = %agent_id, run_id = %run_id, "Agent run ownership lease renewed")
            }
            Ok(_) | Err(_) => {
                tracing::warn!(computer_id = %computer_id, agent_member_id = %agent_id, run_id = %run_id, error_code = "lease_renew_failed", "Agent Inbox lease renewal failed")
            }
        }
    }
    Ok(())
}

pub(super) async fn release_interrupted_runs(
    client: &reqwest::Client,
    server: &Url,
    computer_id: Uuid,
    token: &str,
    database: &SqlitePool,
) -> Result<()> {
    let runs: Vec<(String, String, String)> = sqlx::query_as(
        "SELECT run_id, agent_member_id, fencing_token FROM local_agent_runs \
         WHERE status = 'failed' AND last_error_code = 'process_lost' \
           AND server_recovery_reported_at IS NULL ORDER BY run_id",
    )
    .fetch_all(database)
    .await?;
    for (run_id, agent_id, fencing_token) in runs {
        let run_id = Uuid::parse_str(&run_id)?;
        let agent_id = Uuid::parse_str(&agent_id)?;
        client
            .post(server.join(&format!(
                "/api/v1/computers/{computer_id}/agents/{agent_id}/inbox/release"
            ))?)
            .bearer_auth(token)
            .json(&serde_json::json!({
                "run_id": run_id,
                "fencing_token": fencing_token,
                "error_code": "process_lost",
            }))
            .send()
            .await?
            .error_for_status()?;
        tracing::info!(
            computer_id = %computer_id,
            agent_member_id = %agent_id,
            run_id = %run_id,
            error_code = "process_lost",
            "Interrupted Agent run lease released"
        );
        sqlx::query(
            "UPDATE local_agent_runs SET server_recovery_reported_at = ?2 WHERE run_id = ?1",
        )
        .bind(run_id.to_string())
        .bind(OffsetDateTime::now_utc().to_string())
        .execute(database)
        .await?;
    }
    Ok(())
}
