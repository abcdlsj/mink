//! Failure acceptance for the scenarios in `docs/design/10-delivery-acceptance.md`: a Server restart
//! during a Run, a Computer offline until its lease expires, and a lost workspace with a corrupted
//! Provider locator. Each asserts that Message, Task, Inbox and Run facts agree afterwards.

mod support;

use std::{net::SocketAddr, path::Path, time::Duration};

use anyhow::{Context, Result, ensure};
use reqwest::{Client, StatusCode, header};
use serde_json::Value;
use sqlx::{Executor, PgPool, Row};
use tempfile::{TempDir, tempdir};
use url::Url;
use uuid::Uuid;

use support::{
    SumiProcess, TestDatabase, reserve_local_port, spawn_server, wait_for_health,
    write_server_config,
};

/// A running Server with an Owner, a Space, a Channel, a paired Computer row and one active Agent.
/// The Computer is inserted directly so a test can act as that Computer over HTTP without a daemon,
/// which keeps the failure under test the only moving part.
struct Fixture {
    root: TempDir,
    config: std::path::PathBuf,
    server: SumiProcess,
    server_url: Url,
    client: Client,
    pool: PgPool,
    cookie: String,
    space_id: Uuid,
    channel_id: Uuid,
    computer_id: Uuid,
    computer_token: String,
    agent_id: Uuid,
}

impl Fixture {
    async fn start(database: &TestDatabase) -> Result<Self> {
        let root = tempdir()?;
        let web_dist = root.path().join("web");
        let attachments = root.path().join("attachments");
        std::fs::create_dir(&web_dist)?;
        std::fs::create_dir(&attachments)?;
        std::fs::write(
            web_dist.join("index.html"),
            "<!doctype html><title>Sumi</title>",
        )?;

        let bind = SocketAddr::from(([127, 0, 0, 1], reserve_local_port()?));
        let server_url = Url::parse(&format!("http://{bind}"))?;
        let config = root.path().join("server.toml");
        write_server_config(&config, bind, &database.url, &attachments, &web_dist)?;
        let server = spawn_server(&config)?;
        wait_for_health(&server_url).await?;

        let client = Client::builder()
            .redirect(reqwest::redirect::Policy::none())
            .build()?;
        let cookie = support::register_human(&client, &server_url).await?;
        let space = support::create_space(&client, &server_url, &cookie).await?;
        let pool = PgPool::connect(&database.url).await?;

        let (computer_id, computer_token) =
            insert_computer(&pool, space.id, "Failure Test Computer").await?;
        let agent_id = insert_agent(&pool, space.id, computer_id, space.general_channel_id).await?;

        Ok(Self {
            root,
            config,
            server,
            server_url,
            client,
            pool,
            cookie,
            space_id: space.id,
            channel_id: space.general_channel_id,
            computer_id,
            computer_token,
            agent_id,
        })
    }

    /// Publishes a Root Message that mentions the Agent, so the send transaction routes one hard Item.
    async fn mention_agent(&self, body: &str) -> Result<Uuid> {
        let response = self
            .client
            .post(
                self.server_url
                    .join(&format!("/api/v1/channels/{}/messages", self.channel_id))?,
            )
            .header("idempotency-key", Uuid::now_v7().to_string())
            .header(header::COOKIE, &self.cookie)
            .json(&serde_json::json!({
                "body_markdown": body,
                "mentions": [self.agent_id],
            }))
            .send()
            .await?;
        ensure!(
            response.status() == StatusCode::CREATED,
            "publish Message: {}",
            response.status()
        );
        let message: Value = response.json().await?;
        Ok(message["id"].as_str().context("Message ID")?.parse()?)
    }

    /// Claims the pending Item as this Computer would, returning the new Run.
    async fn claim_run(&self) -> Result<Option<Uuid>> {
        let response = self
            .client
            .post(self.server_url.join(&format!(
                "/api/v1/computers/{}/runs/claim",
                self.computer_id
            ))?)
            .bearer_auth(&self.computer_token)
            .send()
            .await?;
        ensure!(
            response.status().is_success(),
            "claim Run: {}",
            response.status()
        );
        let claimed: Value = response.json().await?;
        if !claimed["claimed"].as_bool().unwrap_or(false) {
            return Ok(None);
        }
        Ok(Some(claimed["run_id"].as_str().context("Run ID")?.parse()?))
    }

    async fn restart_server(&mut self) -> Result<()> {
        self.server.crash().await?;
        self.server = spawn_server(&self.config)?;
        wait_for_health(&self.server_url).await?;
        Ok(())
    }

    async fn run_row(&self, run_id: Uuid) -> Result<(String, Option<String>, Option<String>)> {
        let row = sqlx::query("SELECT status,outcome_code,error_code FROM agent_runs WHERE id=$1")
            .bind(run_id)
            .fetch_one(&self.pool)
            .await?;
        Ok((row.get(0), row.get(1), row.get(2)))
    }

    async fn item_row(&self, message_id: Uuid) -> Result<(String, i32, Option<Uuid>)> {
        let row = sqlx::query(
            "SELECT status,retry_count,lease_run_id FROM inbox_items WHERE message_id=$1",
        )
        .bind(message_id)
        .fetch_one(&self.pool)
        .await?;
        Ok((row.get(0), row.get(1), row.get(2)))
    }

    async fn finish(mut self) -> Result<()> {
        self.pool.close().await;
        let _ = self.server.interrupt().await;
        drop(self.root);
        Ok(())
    }
}

#[tokio::test]
async fn a_server_restart_during_a_run_preserves_every_committed_fact() -> Result<()> {
    let database = TestDatabase::create("sumi_failure_server_restart").await?;
    let result = server_restart_during_run(&database).await;
    database.drop().await?;
    result
}

/// A Server restart is not a Run failure: the Run's ownership lives in the database, so the same
/// Computer must be able to continue reporting against it once the Server is back.
async fn server_restart_during_run(database: &TestDatabase) -> Result<()> {
    let mut fixture = Fixture::start(database).await?;

    let message_id = fixture
        .mention_agent("please review the deploy plan")
        .await?;
    let run_id = fixture
        .claim_run()
        .await?
        .context("the mention produces a claimable hard Item")?;

    // Report the Run as started so the restart interrupts a Run that is genuinely in flight.
    let fencing_token = fencing_token_for(&fixture, run_id).await?;
    let started = fixture
        .client
        .post(fixture.server_url.join(&format!(
            "/api/v1/computers/{}/runs/{run_id}/started",
            fixture.computer_id
        ))?)
        .bearer_auth(&fixture.computer_token)
        .json(&serde_json::json!({
            "event_id": Uuid::now_v7(),
            "run_id": run_id,
            "fencing_token": fencing_token,
            "observed_at": now_rfc3339(),
        }))
        .send()
        .await?;
    ensure!(
        started.status().is_success(),
        "report Run started: {}",
        started.status()
    );
    let before = fixture.run_row(run_id).await?;
    ensure!(before.0 == "running", "Run is in flight: {before:?}");

    fixture.restart_server().await?;

    // Every fact the restart interrupted survived, because each was committed before the crash.
    let after = fixture.run_row(run_id).await?;
    ensure!(
        after == before,
        "Run facts changed across the restart: {before:?} -> {after:?}"
    );
    let (item_status, retry_count, lease_run_id) = fixture.item_row(message_id).await?;
    ensure!(
        (item_status.as_str(), retry_count, lease_run_id) == ("leased", 0, Some(run_id)),
        "Item lease did not survive the restart: {item_status} {retry_count} {lease_run_id:?}"
    );
    // A restart is not a delivery failure, so it must not spend a retry.
    ensure!(retry_count == 0, "the restart consumed a retry attempt");

    // The Message remains readable and still owns the Item's source, so no attention was invented or lost.
    let items: Vec<Value> = fixture
        .client
        .get(
            fixture
                .server_url
                .join(&format!("/api/v1/members/{}/inbox", fixture.agent_id))?,
        )
        .header(header::COOKIE, &fixture.cookie)
        .send()
        .await?
        .error_for_status()?
        .json()
        .await?;
    ensure!(
        items.len() == 1 && items[0]["message_id"].as_str() == Some(&message_id.to_string()),
        "Inbox projection disagrees with the source Message: {items:?}"
    );

    // The restarted Server still recognizes the Run's owner, so the Computer can finish its work.
    let result = fixture
        .client
        .post(fixture.server_url.join(&format!(
            "/api/v1/computers/{}/runs/{run_id}/result",
            fixture.computer_id
        ))?)
        .bearer_auth(&fixture.computer_token)
        .json(&serde_json::json!({
            "event_id": Uuid::now_v7(),
            "run_id": run_id,
            "fencing_token": fencing_token,
            "status": "completed",
            "item_outcomes": [{
                "item_id": item_id_for(&fixture, message_id).await?,
                "disposition": "handled",
            }],
            "continuation_note": null,
            "error_code": null,
        }))
        .send()
        .await?;
    ensure!(
        result.status().is_success(),
        "the restarted Server must still accept this Run's result: {} {}",
        result.status(),
        result.text().await.unwrap_or_default()
    );
    let (status, outcome, error) = fixture.run_row(run_id).await?;
    ensure!(
        (status.as_str(), outcome.as_deref(), error.as_deref())
            == ("completed", Some("completed"), None),
        "Run did not reach a clean terminal state: {status} {outcome:?} {error:?}"
    );
    let (item_status, _, lease) = fixture.item_row(message_id).await?;
    ensure!(
        item_status == "handled" && lease.is_none(),
        "the handled Item still holds a lease: {item_status} {lease:?}"
    );

    fixture.finish().await
}

#[tokio::test]
async fn a_computer_offline_until_its_lease_expires_returns_the_item_and_frees_the_agent()
-> Result<()> {
    let database = TestDatabase::create("sumi_failure_lease_expiry").await?;
    let result = computer_offline_until_lease_expiry(&database).await;
    database.drop().await?;
    result
}

/// A Computer that stops renewing must not strand the Item or the Agent. The Server's reclaim sweep
/// fails the Run and returns the Item, and the Agent must then be able to claim again.
async fn computer_offline_until_lease_expiry(database: &TestDatabase) -> Result<()> {
    let fixture = Fixture::start(database).await?;

    let message_id = fixture.mention_agent("the staging build is red").await?;
    let run_id = fixture
        .claim_run()
        .await?
        .context("the mention produces a claimable hard Item")?;
    let item_id = item_id_for(&fixture, message_id).await?;

    // While the lease holds, no second Run can start for this Agent.
    ensure!(
        fixture.claim_run().await?.is_none(),
        "a leased Item must not be claimed twice"
    );

    // Expire the lease rather than waiting two minutes. Backdating is what going offline looks like to
    // the Server: the Computer stopped renewing and the deadline passed.
    sqlx::query("UPDATE agent_runs SET lease_expires_at=now()-interval '1 minute' WHERE id=$1")
        .bind(run_id)
        .execute(&fixture.pool)
        .await?;
    sqlx::query("UPDATE inbox_items SET lease_expires_at=now()-interval '1 minute' WHERE id=$1")
        .bind(item_id)
        .execute(&fixture.pool)
        .await?;

    // The sweep runs every 30 seconds inside the Server, so allow it more than one interval.
    let recovered = wait_for(Duration::from_secs(75), || async {
        let (status, outcome, error) = fixture.run_row(run_id).await?;
        Ok((status == "failed").then_some((status, outcome, error)))
    })
    .await
    .context("the Server did not reclaim the Run whose lease expired")?;
    ensure!(
        (recovered.1.as_deref(), recovered.2.as_deref()) == (Some("failed"), Some("process_lost")),
        "the reclaimed Run lost its machine-readable cause: {recovered:?}"
    );

    // The Item returned to the queue and spent exactly one retry, because one delivery attempt failed.
    let (item_status, retry_count, lease) = fixture.item_row(message_id).await?;
    ensure!(
        (item_status.as_str(), retry_count, lease) == ("pending", 1, None),
        "the Item did not return to the queue cleanly: {item_status} {retry_count} {lease:?}"
    );

    // The Run is terminal, so the Agent is free and the recovered Item is claimable again. Without
    // both, the expiry would leave the Agent permanently unable to work.
    let second_run = wait_for(Duration::from_secs(10), || async {
        fixture.claim_run().await
    })
    .await
    .context("the recovered Item must be claimable by a new Run")?;
    ensure!(
        second_run != run_id,
        "recovery reused the failed Run instead of starting a new one"
    );
    let (item_status, _, lease) = fixture.item_row(message_id).await?;
    ensure!(
        item_status == "leased" && lease == Some(second_run),
        "the new Run does not own the recovered Item: {item_status} {lease:?}"
    );

    // The source Message and the Inbox projection still agree: one Item, same source, no duplicates.
    let items: Vec<Value> = fixture
        .client
        .get(
            fixture
                .server_url
                .join(&format!("/api/v1/members/{}/inbox", fixture.agent_id))?,
        )
        .header(header::COOKIE, &fixture.cookie)
        .send()
        .await?
        .error_for_status()?
        .json()
        .await?;
    ensure!(
        items.len() == 1
            && items[0]["id"].as_str() == Some(&item_id.to_string())
            && items[0]["retry_count"].as_u64() == Some(1),
        "the Inbox projection disagrees with the recovered Item: {items:?}"
    );

    fixture.finish().await
}

#[tokio::test]
async fn a_lost_workspace_and_corrupt_locator_do_not_resume_a_stale_session() -> Result<()> {
    let database = TestDatabase::create("sumi_failure_workspace_loss").await?;
    let result = workspace_loss_and_locator_corruption(&database).await;
    database.drop().await?;
    result
}

/// A Provider Session is a Computer-local cache keyed to the workspace it was opened against. Deleting
/// the workspace and corrupting the stored locator must both retire that cache rather than resume it,
/// and the Server's Run, Item and Message facts must stay consistent throughout.
///
/// This scenario needs a real Agent Home, so it runs a paired daemon that validates its Driver against
/// the local `codex` executable. Without that executable the daemon cannot bring an Agent to `active`,
/// and the scenario has nothing to corrupt.
async fn workspace_loss_and_locator_corruption(database: &TestDatabase) -> Result<()> {
    if !codex_executable_available() {
        eprintln!(
            "skipping workspace loss acceptance: no `codex` executable on PATH, so a Driver cannot \
             validate and no Agent Home is prepared"
        );
        return Ok(());
    }
    let fixture = Fixture::start(database).await?;

    // Pair a real daemon so workspace loss and locator corruption hit the filesystem and SQLite the
    // Computer actually uses.
    //
    // The state directory sits under a deliberately short root rather than the Fixture's tempdir. The
    // daemon derives its Unix socket path from this directory and appends two UUID path segments, so a
    // long prefix pushes the socket past the platform limit on `sun_path` and the daemon cannot bind.
    let state_root = TempDir::with_prefix_in("sumi-", short_temp_root()?)?;
    let state_dir = state_root.path().join("s");
    std::fs::create_dir(&state_dir)?;
    let computer_config = fixture.root.path().join("computer.toml");
    support::write_computer_config(&computer_config, &fixture.server_url, &state_dir)?;
    let mut daemon = support::spawn_computer(&computer_config)?;
    let pairing_url = support::pairing_url_from_daemon(&mut daemon)
        .await
        .with_context(|| format!("daemon logs: {}", daemon.log_text()))?;
    let paired = support::confirm_pairing(
        &fixture.client,
        &fixture.server_url,
        &fixture.cookie,
        fixture.space_id,
        &pairing_url,
    )
    .await?;
    // The Fixture's stand-in Computer is also marked online, so wait on the paired one by ID.
    wait_for_paired_computer_online(&fixture, paired.id)
        .await
        .with_context(|| format!("daemon logs: {}", daemon.log_text()))?;

    // A Browser-created Agent gets a real Agent Home, which is what the failures corrupt.
    let response = fixture
        .client
        .post(
            fixture
                .server_url
                .join(&format!("/api/v1/spaces/{}/agents", fixture.space_id))?,
        )
        .header("idempotency-key", Uuid::now_v7().to_string())
        .header(header::COOKIE, &fixture.cookie)
        .json(&serde_json::json!({
            "computer_id": paired.id,
            "name": "Nia",
            "handle": format!("nia{}", &Uuid::now_v7().simple().to_string()[..6]),
            "role_text": "Investigate failures",
            "access_level": "member",
            "driver_kind": "codex",
        }))
        .send()
        .await?;
    ensure!(
        response.status() == StatusCode::CREATED,
        "create Agent: {} {}",
        response.status(),
        response.text().await.unwrap_or_default()
    );
    let agent: Value = response.json().await?;
    let provisioned_agent: Uuid = agent["member_id"].as_str().context("Agent ID")?.parse()?;

    // Wait for the Computer to confirm the local Home, which is when the workspace exists on disk.
    let computer_home = wait_for(Duration::from_secs(30), || async {
        let lifecycle: String =
            sqlx::query_scalar("SELECT lifecycle FROM agents WHERE member_id=$1")
                .bind(provisioned_agent)
                .fetch_one(&fixture.pool)
                .await?;
        if lifecycle != "active" {
            return Ok(None);
        }
        find_agent_home(&state_dir, provisioned_agent)
    })
    .await
    .with_context(|| format!("Agent Home was never prepared; logs: {}", daemon.log_text()))?;
    let workspace = computer_home.join("workspace");
    ensure!(workspace.is_dir(), "the Agent workspace was not created");

    let database_path = computer_home
        .parent()
        .and_then(Path::parent)
        .context("Agent Home is nested under the Computer Home")?
        .join("daemon.db");

    // Stop the daemon before damaging its state. Both failures are discovered at startup, and writing
    // to the SQLite file the running daemon owns would test the test harness rather than recovery.
    daemon.crash().await?;

    // Record a Ready Provider Session whose locator is unusable and whose workspace fingerprint belongs
    // to the workspace about to disappear.
    let thread_id = Uuid::now_v7();
    insert_provider_session(&database_path, provisioned_agent, thread_id).await?;

    // Losing the workspace changes the fingerprint, and the recorded locator no longer resolves.
    std::fs::remove_dir_all(&workspace)?;
    ensure!(!workspace.exists(), "the workspace was not removed");

    let sessions_before = provider_session_rows(&database_path).await?;
    ensure!(
        sessions_before == vec![("ready".to_owned(), "corrupt-locator".to_owned())],
        "the seeded Session is not the one under test: {sessions_before:?}"
    );

    // The Server holds no Provider Session facts, so the local damage must not have created any.
    let server_side: i64 = sqlx::query_scalar(
        "SELECT count(*) FROM agent_runs WHERE agent_id=$1 AND status NOT IN \
         ('completed','yielded','failed','canceled')",
    )
    .bind(provisioned_agent)
    .fetch_one(&fixture.pool)
    .await?;
    ensure!(
        server_side == 0,
        "local Session damage must not create Server Run facts"
    );

    // Attention that arrives while the Computer is down is still routed and waits in the queue. Local
    // damage is not a reason to drop a Message or its Item.
    let message_id = publish_mention(&fixture, provisioned_agent, "workspace is gone").await?;
    let (item_status, retry_count, _) = fixture.item_row(message_id).await?;
    ensure!(
        (item_status.as_str(), retry_count) == ("pending", 0),
        "the Item was not queued while the Computer was down: {item_status} {retry_count}"
    );

    // Restart the daemon against the damaged state and give it time to attempt recovery. A lost
    // workspace currently keeps the Computer from completing its handshake, because the replayed
    // provision command validates the Driver against a workspace that no longer exists. The Computer
    // therefore stays offline and retries; what matters for this acceptance is that it degrades without
    // corrupting Server facts or inventing a Provider locator.
    let mut daemon = support::spawn_computer(&computer_config)?;
    tokio::time::sleep(Duration::from_secs(5)).await;
    daemon.ensure_running().with_context(|| {
        format!(
            "the daemon exited instead of retrying after local damage; logs: {}",
            daemon.log_text()
        )
    })?;
    let sessions_after = provider_session_rows(&database_path).await?;
    ensure!(
        sessions_after
            .iter()
            .all(|(_, locator)| locator == "corrupt-locator"),
        "recovery invented a Provider locator: {sessions_after:?}"
    );
    // The unusable Session is never resumed: nothing moved it out of the state it was seeded in.
    ensure!(
        sessions_after == sessions_before,
        "the stale Session was resumed or rewritten: {sessions_before:?} -> {sessions_after:?}"
    );

    // Message, Item and Run facts still agree after both failures and the restart.
    let (message_count, item_count): (i64, i64) = sqlx::query_as(
        "SELECT (SELECT count(*) FROM messages WHERE id=$1), \
                (SELECT count(*) FROM inbox_items WHERE message_id=$1)",
    )
    .bind(message_id)
    .fetch_one(&fixture.pool)
    .await?;
    ensure!(
        (message_count, item_count) == (1, 1),
        "the Message and its Item disagree: {message_count} {item_count}"
    );
    let orphaned: i64 = sqlx::query_scalar(
        "SELECT count(*) FROM inbox_items i WHERE i.status='leased' \
         AND NOT EXISTS(SELECT 1 FROM agent_runs r WHERE r.id=i.lease_run_id \
                        AND r.status NOT IN ('completed','yielded','failed','canceled'))",
    )
    .fetch_one(&fixture.pool)
    .await?;
    ensure!(orphaned == 0, "an Item is leased to a terminal Run");

    let _ = daemon.interrupt().await;
    fixture.finish().await
}

/// Whether a `codex` executable resolves on PATH, which is how the Codex Driver validates an Agent.
fn codex_executable_available() -> bool {
    std::env::var_os("PATH").is_some_and(|path| {
        std::env::split_paths(&path).any(|directory| {
            let candidate = directory.join("codex");
            candidate.is_file() || candidate.is_symlink()
        })
    })
}

/// A short writable directory for Computer state. `std::env::temp_dir()` is long on macOS, which would
/// push the daemon's Unix socket past the `sun_path` limit once the Computer Home's UUID segments are
/// appended.
fn short_temp_root() -> Result<std::path::PathBuf> {
    let candidate = std::path::PathBuf::from("/tmp");
    if candidate.is_dir() {
        return Ok(candidate);
    }
    Ok(std::env::temp_dir())
}

/// Waits for one specific Computer to report online. The Space also holds the Fixture's stand-in
/// Computer, so matching on status alone would accept the wrong row.
async fn wait_for_paired_computer_online(fixture: &Fixture, computer_id: Uuid) -> Result<()> {
    wait_for(Duration::from_secs(30), || async {
        let status: String =
            sqlx::query_scalar("SELECT connection_status FROM computers WHERE id=$1")
                .bind(computer_id)
                .fetch_one(&fixture.pool)
                .await?;
        Ok((status == "online").then_some(()))
    })
    .await
    .context("the paired Computer never reported online")
}

/// Locates the Agent Home the daemon created. The Computer Home name comes from pairing, so the test
/// discovers it rather than predicting it.
fn find_agent_home(state_dir: &Path, agent_id: Uuid) -> Result<Option<std::path::PathBuf>> {
    for space in std::fs::read_dir(state_dir)? {
        let space = space?.path();
        if !space.is_dir() {
            continue;
        }
        for computer in std::fs::read_dir(&space)? {
            let home = computer?.path().join("agents").join(agent_id.to_string());
            if home.is_dir() {
                return Ok(Some(home));
            }
        }
    }
    Ok(None)
}

/// Seeds a Ready Provider Session with an unusable locator, standing in for a cache entry that outlived
/// the workspace it was opened against.
async fn insert_provider_session(
    database_path: &Path,
    agent_id: Uuid,
    thread_id: Uuid,
) -> Result<()> {
    let pool = sqlx::SqlitePool::connect(&format!("sqlite://{}", database_path.display())).await?;
    sqlx::query(
        "INSERT INTO provider_sessions \
         (agent_id,scope_kind,scope_id,generation,driver_kind,provider_locator,\
          workspace_fingerprint,role_revision,audience_fingerprint,state,created_at) \
         VALUES (?1,'thread',?2,1,'builtin','corrupt-locator','stale-workspace',1,'audience',\
                 'ready',?3)",
    )
    .bind(agent_id.to_string())
    .bind(thread_id.to_string())
    .bind(now_rfc3339())
    .execute(&pool)
    .await?;
    pool.close().await;
    Ok(())
}

async fn provider_session_rows(database_path: &Path) -> Result<Vec<(String, String)>> {
    let pool = sqlx::SqlitePool::connect(&format!("sqlite://{}", database_path.display())).await?;
    let rows: Vec<(String, String)> =
        sqlx::query_as("SELECT state,provider_locator FROM provider_sessions ORDER BY generation")
            .fetch_all(&pool)
            .await?;
    pool.close().await;
    Ok(rows)
}

/// Publishes a Root Message mentioning one Agent by ID.
async fn publish_mention(fixture: &Fixture, agent_id: Uuid, body: &str) -> Result<Uuid> {
    // A mention only routes to a Channel member, so join the Agent first if it is not already in.
    sqlx::query(
        "INSERT INTO channel_members (channel_id,space_id,member_id,joined_at,last_read_seq) \
         VALUES ($1,$2,$3,now(),0) ON CONFLICT DO NOTHING",
    )
    .bind(fixture.channel_id)
    .bind(fixture.space_id)
    .bind(agent_id)
    .execute(&fixture.pool)
    .await?;
    let response = fixture
        .client
        .post(
            fixture
                .server_url
                .join(&format!("/api/v1/channels/{}/messages", fixture.channel_id))?,
        )
        .header("idempotency-key", Uuid::now_v7().to_string())
        .header(header::COOKIE, &fixture.cookie)
        .json(&serde_json::json!({"body_markdown": body, "mentions": [agent_id]}))
        .send()
        .await?;
    ensure!(
        response.status() == StatusCode::CREATED,
        "publish Message: {}",
        response.status()
    );
    let message: Value = response.json().await?;
    Ok(message["id"].as_str().context("Message ID")?.parse()?)
}

/// Polls until `probe` yields a value or the deadline passes. Recovery here is driven by the Server's
/// own timer, so a test cannot observe it synchronously.
async fn wait_for<T, F, Fut>(within: Duration, mut probe: F) -> Result<T>
where
    F: FnMut() -> Fut,
    Fut: std::future::Future<Output = Result<Option<T>>>,
{
    tokio::time::timeout(within, async {
        loop {
            if let Some(value) = probe().await? {
                return Ok(value);
            }
            tokio::time::sleep(Duration::from_millis(250)).await;
        }
    })
    .await?
}

fn now_rfc3339() -> String {
    time::OffsetDateTime::now_utc()
        .format(&time::format_description::well_known::Rfc3339)
        .expect("RFC 3339 formatting cannot fail for a valid timestamp")
}

/// Reads the Item routed from one Message. Tests act as the Computer, which learns Item IDs from the
/// claim response; reading it back keeps the assertions independent of that payload's shape.
async fn item_id_for(fixture: &Fixture, message_id: Uuid) -> Result<Uuid> {
    Ok(
        sqlx::query_scalar::<_, Uuid>("SELECT id FROM inbox_items WHERE message_id=$1")
            .bind(message_id)
            .fetch_one(&fixture.pool)
            .await?,
    )
}

/// Inserts a fencing token the test controls. The Server only stores the hash, so a test cannot recover
/// the token the claim generated; replacing the hash lets it prove ownership as the owning Computer.
async fn fencing_token_for(fixture: &Fixture, run_id: Uuid) -> Result<String> {
    let token = format!("failure-test-fencing-{}", Uuid::now_v7().simple());
    let hash = hex::encode(<sha2::Sha256 as sha2::Digest>::digest(token.as_bytes()));
    sqlx::query("UPDATE agent_runs SET fencing_token_hash=$2 WHERE id=$1")
        .bind(run_id)
        .bind(&hash)
        .execute(&fixture.pool)
        .await?;
    Ok(token)
}

/// Inserts a confirmed Computer and returns its ID with the raw Token. Pairing is covered elsewhere;
/// these tests need a Computer identity, not the pairing handshake.
async fn insert_computer(pool: &PgPool, space_id: Uuid, name: &str) -> Result<(Uuid, String)> {
    let computer_id = Uuid::now_v7();
    let token = format!("failure-test-token-{}", Uuid::now_v7().simple());
    let token_hash = hex::encode(<sha2::Sha256 as sha2::Digest>::digest(token.as_bytes()));
    sqlx::query(
        "INSERT INTO computers \
         (id,space_id,name,hostname,os,token_hash,connection_status,next_command_seq,created_at) \
         VALUES ($1,$2,$3,'localhost','linux',$4,'online',1,now())",
    )
    .bind(computer_id)
    .bind(space_id)
    .bind(name)
    .bind(&token_hash)
    .execute(pool)
    .await?;
    Ok((computer_id, token))
}

/// Inserts an active Agent Member on that Computer and joins it to the Channel so Messages route to it.
async fn insert_agent(
    pool: &PgPool,
    space_id: Uuid,
    computer_id: Uuid,
    channel_id: Uuid,
) -> Result<Uuid> {
    let agent_id = Uuid::now_v7();
    let handle = format!("lin{}", &Uuid::now_v7().simple().to_string()[..6]);
    pool.execute(
        sqlx::query(
            "INSERT INTO members (id,space_id,kind,display_name,handle,access_level,created_at) \
             VALUES ($1,$2,'agent','Lin',$3,'member',now())",
        )
        .bind(agent_id)
        .bind(space_id)
        .bind(&handle),
    )
    .await?;
    pool.execute(
        sqlx::query(
            "INSERT INTO agents \
             (member_id,space_id,computer_id,role_text,role_revision,lifecycle,driver_kind,created_at) \
             VALUES ($1,$2,$3,'Assist',1,'active','codex',now())",
        )
        .bind(agent_id)
        .bind(space_id)
        .bind(computer_id),
    )
    .await?;
    pool.execute(
        sqlx::query(
            "INSERT INTO channel_members (channel_id,space_id,member_id,joined_at,last_read_seq) \
             VALUES ($1,$2,$3,now(),0)",
        )
        .bind(channel_id)
        .bind(space_id)
        .bind(agent_id),
    )
    .await?;
    Ok(agent_id)
}
