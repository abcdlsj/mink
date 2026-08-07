use serde_json::Value;
use sqlx::{FromRow, PgPool};
use time::OffsetDateTime;
use uuid::Uuid;

use crate::server::application::ports::ApplicationError;

use super::map_sqlx;

pub(in crate::server::adapters) struct ChannelLeaveReplayQuery {
    pub(in crate::server::adapters) agent_id: Uuid,
    pub(in crate::server::adapters) key: Uuid,
    pub(in crate::server::adapters) space_id: Uuid,
    pub(in crate::server::adapters) computer_id: Uuid,
    pub(in crate::server::adapters) run_id: Uuid,
    pub(in crate::server::adapters) task_id: Option<Uuid>,
    pub(in crate::server::adapters) focus_thread_id: Uuid,
}

#[derive(Clone)]
pub(in crate::server::adapters) struct PostgresQueries {
    pool: PgPool,
}

impl PostgresQueries {
    pub(in crate::server::adapters) fn new(pool: PgPool) -> Self {
        Self { pool }
    }

    pub(in crate::server::adapters) async fn spaces_for_user(
        &self,
        user_id: Uuid,
    ) -> Result<Vec<SpaceRow>, ApplicationError> {
        sqlx::query_as(
            "SELECT s.id,s.name,s.slug,s.accent,s.owner_member_id,
                    hm.member_id AS current_member_id,
                    (SELECT id FROM channels WHERE space_id=s.id AND slug='general' LIMIT 1)
                        AS general_channel_id
             FROM spaces s JOIN human_members hm ON hm.space_id=s.id
             WHERE hm.user_id=$1 AND s.deleted_at IS NULL ORDER BY s.created_at",
        )
        .bind(user_id)
        .fetch_all(&self.pool)
        .await
        .map_err(map_sqlx)
    }

    pub(in crate::server::adapters) async fn space_for_user_slug(
        &self,
        user_id: Uuid,
        slug: &str,
    ) -> Result<Option<SpaceRow>, ApplicationError> {
        sqlx::query_as(
            "SELECT s.id,s.name,s.slug,s.accent,s.owner_member_id,
                    hm.member_id AS current_member_id,
                    (SELECT id FROM channels WHERE space_id=s.id AND slug='general' LIMIT 1)
                        AS general_channel_id
             FROM spaces s JOIN human_members hm ON hm.space_id=s.id
             WHERE hm.user_id=$1 AND lower(s.slug)=lower($2) AND s.deleted_at IS NULL",
        )
        .bind(user_id)
        .bind(slug)
        .fetch_optional(&self.pool)
        .await
        .map_err(map_sqlx)
    }

    pub(in crate::server::adapters) async fn channels_in_space(
        &self,
        space_id: Uuid,
        member_id: Uuid,
    ) -> Result<Vec<ChannelRow>, ApplicationError> {
        sqlx::query_as(
            "SELECT c.id,c.space_id,c.kind,c.slug,c.topic,c.archived_at,
                    EXISTS(SELECT 1 FROM channel_members cm
                           WHERE cm.channel_id=c.id AND cm.member_id=$2) AS joined
             FROM channels c WHERE c.space_id=$1 AND c.kind<>'direct' ORDER BY c.created_at",
        )
        .bind(space_id)
        .bind(member_id)
        .fetch_all(&self.pool)
        .await
        .map_err(map_sqlx)
    }

    pub(in crate::server::adapters) async fn channel(
        &self,
        channel_id: Uuid,
        viewer_id: Uuid,
    ) -> Result<Option<ChannelRow>, ApplicationError> {
        sqlx::query_as(
            "SELECT c.id,c.space_id,c.kind,c.slug,c.topic,c.archived_at,
                    EXISTS(SELECT 1 FROM channel_members cm
                           WHERE cm.channel_id=c.id AND cm.member_id=$2) AS joined
             FROM channels c WHERE c.id=$1",
        )
        .bind(channel_id)
        .bind(viewer_id)
        .fetch_optional(&self.pool)
        .await
        .map_err(map_sqlx)
    }

    pub(in crate::server::adapters) async fn channel_members(
        &self,
        channel_id: Uuid,
    ) -> Result<Vec<MemberRow>, ApplicationError> {
        sqlx::query_as(
            "SELECT members.id,members.kind,members.display_name,members.access_level
             FROM channel_members JOIN members ON members.id=channel_members.member_id
             WHERE channel_members.channel_id=$1
             ORDER BY channel_members.joined_at,members.id",
        )
        .bind(channel_id)
        .fetch_all(&self.pool)
        .await
        .map_err(map_sqlx)
    }

    pub(in crate::server::adapters) async fn member_is_governor(
        &self,
        member_id: Uuid,
    ) -> Result<bool, ApplicationError> {
        sqlx::query_scalar("SELECT access_level IN ('owner','admin') FROM members WHERE id=$1")
            .bind(member_id)
            .fetch_one(&self.pool)
            .await
            .map_err(map_sqlx)
    }

    pub(in crate::server::adapters) async fn members_in_space(
        &self,
        space_id: Uuid,
    ) -> Result<Vec<MemberRow>, ApplicationError> {
        sqlx::query_as(
            "SELECT id,kind,display_name,access_level
             FROM members WHERE space_id=$1 AND retired_at IS NULL ORDER BY created_at",
        )
        .bind(space_id)
        .fetch_all(&self.pool)
        .await
        .map_err(map_sqlx)
    }

    pub(in crate::server::adapters) async fn member(
        &self,
        member_id: Uuid,
    ) -> Result<Option<MemberRow>, ApplicationError> {
        sqlx::query_as("SELECT id,kind,display_name,access_level FROM members WHERE id=$1")
            .bind(member_id)
            .fetch_optional(&self.pool)
            .await
            .map_err(map_sqlx)
    }

    pub(in crate::server::adapters) async fn member_in_space(
        &self,
        member_id: Uuid,
        space_id: Uuid,
    ) -> Result<Option<MemberRow>, ApplicationError> {
        sqlx::query_as(
            "SELECT id,kind,display_name,access_level
             FROM members WHERE id=$1 AND space_id=$2",
        )
        .bind(member_id)
        .bind(space_id)
        .fetch_optional(&self.pool)
        .await
        .map_err(map_sqlx)
    }

    pub(in crate::server::adapters) async fn member_permissions(
        &self,
        member_id: Uuid,
    ) -> Result<Vec<String>, ApplicationError> {
        sqlx::query_scalar(
            "SELECT action_code FROM member_permissions WHERE member_id=$1 ORDER BY action_code",
        )
        .bind(member_id)
        .fetch_all(&self.pool)
        .await
        .map_err(map_sqlx)
    }

    pub(in crate::server::adapters) async fn member_space(
        &self,
        member_id: Uuid,
    ) -> Result<Option<Uuid>, ApplicationError> {
        sqlx::query_scalar("SELECT space_id FROM members WHERE id=$1")
            .bind(member_id)
            .fetch_optional(&self.pool)
            .await
            .map_err(map_sqlx)
    }

    pub(in crate::server::adapters) async fn channel_space(
        &self,
        channel_id: Uuid,
    ) -> Result<Option<Uuid>, ApplicationError> {
        sqlx::query_scalar("SELECT space_id FROM channels WHERE id=$1")
            .bind(channel_id)
            .fetch_optional(&self.pool)
            .await
            .map_err(map_sqlx)
    }

    pub(in crate::server::adapters) async fn root_thread(
        &self,
        thread_id: Uuid,
    ) -> Result<Option<ThreadRootRow>, ApplicationError> {
        sqlx::query_as(
            "SELECT space_id,channel_id FROM messages
             WHERE id=$1 AND placement='root'",
        )
        .bind(thread_id)
        .fetch_optional(&self.pool)
        .await
        .map_err(map_sqlx)
    }

    pub(in crate::server::adapters) async fn message(
        &self,
        message_id: Uuid,
    ) -> Result<Option<MessageRow>, ApplicationError> {
        sqlx::query_as("SELECT * FROM messages WHERE id=$1")
            .bind(message_id)
            .fetch_optional(&self.pool)
            .await
            .map_err(map_sqlx)
    }

    pub(in crate::server::adapters) async fn messages_in_thread(
        &self,
        thread_id: Uuid,
    ) -> Result<Vec<MessageRow>, ApplicationError> {
        sqlx::query_as("SELECT * FROM messages WHERE thread_id=$1 ORDER BY channel_seq")
            .bind(thread_id)
            .fetch_all(&self.pool)
            .await
            .map_err(map_sqlx)
    }

    pub(in crate::server::adapters) async fn root_messages_in_channel(
        &self,
        channel_id: Uuid,
    ) -> Result<Vec<MessageRow>, ApplicationError> {
        sqlx::query_as(
            "SELECT * FROM messages WHERE channel_id=$1 AND placement='root'
             ORDER BY channel_seq",
        )
        .bind(channel_id)
        .fetch_all(&self.pool)
        .await
        .map_err(map_sqlx)
    }

    pub(in crate::server::adapters) async fn channel_snapshot(
        &self,
        channel_id: Uuid,
    ) -> Result<i64, ApplicationError> {
        sqlx::query_scalar("SELECT next_seq-1 FROM channels WHERE id=$1")
            .bind(channel_id)
            .fetch_one(&self.pool)
            .await
            .map_err(map_sqlx)
    }

    pub(in crate::server::adapters) async fn thread_max_sequence(
        &self,
        thread_id: Uuid,
    ) -> Result<u64, ApplicationError> {
        let sequence: i64 = sqlx::query_scalar(
            "SELECT COALESCE(max(channel_seq), 0) FROM messages WHERE thread_id=$1",
        )
        .bind(thread_id)
        .fetch_one(&self.pool)
        .await
        .map_err(map_sqlx)?;
        u64::try_from(sequence).map_err(|_| ApplicationError::Internal)
    }

    pub(in crate::server::adapters) async fn thread_following(
        &self,
        thread_id: Uuid,
        member_id: Uuid,
    ) -> Result<bool, ApplicationError> {
        sqlx::query_scalar(
            "SELECT EXISTS(SELECT 1 FROM thread_subscriptions
                           WHERE thread_id=$1 AND member_id=$2)",
        )
        .bind(thread_id)
        .bind(member_id)
        .fetch_one(&self.pool)
        .await
        .map_err(map_sqlx)
    }

    pub(in crate::server::adapters) async fn task_is_source(
        &self,
        task_id: Uuid,
        thread_id: Uuid,
    ) -> Result<bool, ApplicationError> {
        sqlx::query_scalar("SELECT EXISTS(SELECT 1 FROM tasks WHERE id=$1 AND source_thread_id=$2)")
            .bind(task_id)
            .bind(thread_id)
            .fetch_one(&self.pool)
            .await
            .map_err(map_sqlx)
    }

    pub(in crate::server::adapters) async fn task_refs(
        &self,
        space_id: Uuid,
        sequences: &[i64],
    ) -> Result<Vec<TaskRefRow>, ApplicationError> {
        sqlx::query_as("SELECT id,seq,title,status FROM tasks WHERE space_id=$1 AND seq = ANY($2)")
            .bind(space_id)
            .bind(sequences)
            .fetch_all(&self.pool)
            .await
            .map_err(map_sqlx)
    }

    pub(in crate::server::adapters) async fn task_for_thread(
        &self,
        thread_id: Uuid,
    ) -> Result<Option<MessageTaskRow>, ApplicationError> {
        sqlx::query_as(
            "SELECT t.id,t.seq,t.title,t.status,t.assignee_agent_member_id,
                    m.display_name AS assignee_name,
                    (SELECT r.focus_thread_id FROM agent_runs r
                     WHERE r.task_id=t.id AND r.status IN ('dispatched','working')
                     ORDER BY r.created_at DESC LIMIT 1) AS active_focus_thread_id
             FROM tasks t LEFT JOIN members m ON m.id=t.assignee_agent_member_id
             WHERE t.source_thread_id=$1 OR EXISTS
                 (SELECT 1 FROM task_threads tt WHERE tt.task_id=t.id AND tt.thread_id=$1)
             ORDER BY (t.source_thread_id=$1) DESC,t.created_at DESC LIMIT 1",
        )
        .bind(thread_id)
        .fetch_optional(&self.pool)
        .await
        .map_err(map_sqlx)
    }

    pub(in crate::server::adapters) async fn message_author(
        &self,
        member_id: Uuid,
    ) -> Result<AuthorRow, ApplicationError> {
        sqlx::query_as("SELECT id,kind,display_name FROM members WHERE id=$1")
            .bind(member_id)
            .fetch_one(&self.pool)
            .await
            .map_err(map_sqlx)
    }

    pub(in crate::server::adapters) async fn reply_count(
        &self,
        thread_id: Uuid,
    ) -> Result<i64, ApplicationError> {
        sqlx::query_scalar("SELECT count(*) FROM messages WHERE thread_id=$1 AND placement='reply'")
            .bind(thread_id)
            .fetch_one(&self.pool)
            .await
            .map_err(map_sqlx)
    }

    pub(in crate::server::adapters) async fn attachments_for_message(
        &self,
        message_id: Uuid,
    ) -> Result<Vec<AttachmentRow>, ApplicationError> {
        sqlx::query_as(
            "SELECT a.* FROM attachments a
             JOIN message_attachments ma ON ma.attachment_id=a.id
             WHERE ma.message_id=$1 ORDER BY ma.position",
        )
        .bind(message_id)
        .fetch_all(&self.pool)
        .await
        .map_err(map_sqlx)
    }

    pub(in crate::server::adapters) async fn attention_failures(
        &self,
        message_id: Uuid,
    ) -> Result<Vec<AttentionFailureRow>, ApplicationError> {
        sqlx::query_as(
            "SELECT member_id,last_error_code FROM inbox_items
             WHERE message_id=$1 AND last_error_code IS NOT NULL ORDER BY member_id",
        )
        .bind(message_id)
        .fetch_all(&self.pool)
        .await
        .map_err(map_sqlx)
    }

    pub(in crate::server::adapters) async fn message_mentions(
        &self,
        message_id: Uuid,
    ) -> Result<Vec<Uuid>, ApplicationError> {
        sqlx::query_scalar(
            "SELECT member_id FROM message_mentions WHERE message_id=$1 ORDER BY member_id",
        )
        .bind(message_id)
        .fetch_all(&self.pool)
        .await
        .map_err(map_sqlx)
    }

    pub(in crate::server::adapters) async fn channel_action(
        &self,
        channel_id: Uuid,
    ) -> Result<ActionChannelRow, ApplicationError> {
        sqlx::query_as("SELECT id,slug,archived_at FROM channels WHERE id=$1")
            .bind(channel_id)
            .fetch_one(&self.pool)
            .await
            .map_err(map_sqlx)
    }

    pub(in crate::server::adapters) async fn agent_action(
        &self,
        member_id: Uuid,
    ) -> Result<ActionAgentRow, ApplicationError> {
        sqlx::query_as(
            "SELECT a.member_id,a.lifecycle,m.display_name,m.retired_at
             FROM agents a JOIN members m ON m.id=a.member_id WHERE a.member_id=$1",
        )
        .bind(member_id)
        .fetch_one(&self.pool)
        .await
        .map_err(map_sqlx)
    }

    pub(in crate::server::adapters) async fn task(
        &self,
        task_id: Uuid,
    ) -> Result<Option<TaskRow>, ApplicationError> {
        sqlx::query_as(
            "SELECT t.*,creator.display_name AS creator_name,
                    assignee.display_name AS assignee_name
             FROM tasks t JOIN members creator ON creator.id=t.creator_member_id
             LEFT JOIN members assignee ON assignee.id=t.assignee_agent_member_id
             WHERE t.id=$1",
        )
        .bind(task_id)
        .fetch_optional(&self.pool)
        .await
        .map_err(map_sqlx)
    }

    pub(in crate::server::adapters) async fn task_related_threads(
        &self,
        task_id: Uuid,
    ) -> Result<Vec<Uuid>, ApplicationError> {
        sqlx::query_scalar("SELECT thread_id FROM task_threads WHERE task_id=$1 ORDER BY linked_at")
            .bind(task_id)
            .fetch_all(&self.pool)
            .await
            .map_err(map_sqlx)
    }

    pub(in crate::server::adapters) async fn task_run_ids(
        &self,
        task_id: Uuid,
    ) -> Result<Vec<Uuid>, ApplicationError> {
        sqlx::query_scalar(
            "SELECT id FROM agent_runs WHERE task_id=$1 ORDER BY created_at DESC,id DESC LIMIT 20",
        )
        .bind(task_id)
        .fetch_all(&self.pool)
        .await
        .map_err(map_sqlx)
    }

    pub(in crate::server::adapters) async fn run(
        &self,
        run_id: Uuid,
    ) -> Result<Option<RunRow>, ApplicationError> {
        sqlx::query_as(
            "SELECT r.*,m.display_name AS agent_name,
                    CASE WHEN task.id IS NULL OR task.source_thread_id=r.focus_thread_id
                         THEN 'source' ELSE 'related' END AS relation
             FROM agent_runs r JOIN members m ON m.id=r.agent_id
             LEFT JOIN tasks task ON task.id=r.task_id WHERE r.id=$1",
        )
        .bind(run_id)
        .fetch_optional(&self.pool)
        .await
        .map_err(map_sqlx)
    }

    pub(in crate::server::adapters) async fn thread_reference(
        &self,
        thread_id: Uuid,
    ) -> Result<ThreadReferenceRow, ApplicationError> {
        sqlx::query_as(
            "SELECT m.id,m.channel_id,c.slug,m.channel_seq
             FROM messages m JOIN channels c ON c.id=m.channel_id
             WHERE m.id=$1 AND m.placement='root'",
        )
        .bind(thread_id)
        .fetch_one(&self.pool)
        .await
        .map_err(map_sqlx)
    }

    pub(in crate::server::adapters) async fn task_ids(
        &self,
        space_id: Uuid,
    ) -> Result<Vec<Uuid>, ApplicationError> {
        sqlx::query_scalar(
            "SELECT id FROM tasks WHERE space_id=$1 ORDER BY updated_at DESC,id DESC",
        )
        .bind(space_id)
        .fetch_all(&self.pool)
        .await
        .map_err(map_sqlx)
    }

    pub(in crate::server::adapters) async fn task_space(
        &self,
        task_id: Uuid,
    ) -> Result<Option<Uuid>, ApplicationError> {
        sqlx::query_scalar("SELECT space_id FROM tasks WHERE id=$1")
            .bind(task_id)
            .fetch_optional(&self.pool)
            .await
            .map_err(map_sqlx)
    }

    pub(in crate::server::adapters) async fn task_readable(
        &self,
        task_id: Uuid,
        member_id: Uuid,
    ) -> Result<bool, ApplicationError> {
        sqlx::query_scalar(
            "SELECT EXISTS(
                 SELECT 1 FROM tasks task
                 JOIN messages source ON source.id=task.source_thread_id
                     AND source.placement='root'
                 JOIN channel_members membership ON membership.channel_id=source.channel_id
                 WHERE task.id=$1 AND membership.member_id=$2
             )",
        )
        .bind(task_id)
        .bind(member_id)
        .fetch_one(&self.pool)
        .await
        .map_err(map_sqlx)
    }

    pub(in crate::server::adapters) async fn task_source(
        &self,
        message_id: Uuid,
    ) -> Result<Option<TaskSourceRow>, ApplicationError> {
        sqlx::query_as(
            "SELECT m.space_id,m.thread_id,m.body_markdown FROM messages m
             WHERE m.id=$1 AND m.placement='root'",
        )
        .bind(message_id)
        .fetch_optional(&self.pool)
        .await
        .map_err(map_sqlx)
    }

    pub(in crate::server::adapters) async fn task_run_list(
        &self,
        task_id: Uuid,
    ) -> Result<Vec<Uuid>, ApplicationError> {
        sqlx::query_scalar(
            "SELECT id FROM agent_runs WHERE task_id=$1 ORDER BY created_at DESC,id DESC",
        )
        .bind(task_id)
        .fetch_all(&self.pool)
        .await
        .map_err(map_sqlx)
    }

    pub(in crate::server::adapters) async fn agent_computer(
        &self,
        agent_id: Uuid,
    ) -> Result<Option<Uuid>, ApplicationError> {
        sqlx::query_scalar("SELECT computer_id FROM agents WHERE member_id=$1")
            .bind(agent_id)
            .fetch_optional(&self.pool)
            .await
            .map_err(map_sqlx)
    }

    pub(in crate::server::adapters) async fn computer_agents(
        &self,
        computer_id: Uuid,
    ) -> Result<Vec<ComputerAgentRow>, ApplicationError> {
        sqlx::query_as(
            "SELECT a.member_id,a.space_id,a.role_text,a.role_revision,a.lifecycle,
                    a.driver_kind,a.driver_config_json,m.display_name,m.access_level
             FROM agents a JOIN members m ON m.id=a.member_id
             WHERE a.computer_id=$1 AND a.lifecycle<>'retired'
             ORDER BY a.created_at,a.member_id",
        )
        .bind(computer_id)
        .fetch_all(&self.pool)
        .await
        .map_err(map_sqlx)
    }

    pub(in crate::server::adapters) async fn agents_in_space(
        &self,
        space_id: Uuid,
    ) -> Result<Vec<AgentRow>, ApplicationError> {
        self.agent_query("WHERE a.space_id=$1 ORDER BY a.created_at", &[space_id])
            .await
    }

    pub(in crate::server::adapters) async fn agent(
        &self,
        agent_id: Uuid,
    ) -> Result<Option<AgentRow>, ApplicationError> {
        self.agent_query("WHERE a.member_id=$1", &[agent_id])
            .await
            .map(|mut rows| rows.pop())
    }

    async fn agent_query(
        &self,
        predicate: &str,
        ids: &[Uuid],
    ) -> Result<Vec<AgentRow>, ApplicationError> {
        let query = format!(
            "SELECT a.member_id,a.space_id,a.computer_id,a.role_text,a.role_revision,
                    a.lifecycle,a.driver_kind,a.created_at,a.retired_at,
                    m.display_name,m.access_level,c.connection_status,
                    c.deleted_at AS computer_deleted_at,
                    active_run.status AS run_status,active_run.task_id AS run_task_id,
                    active_task.title AS run_task_title,
                    focus_channel.slug AS run_focus_slug,
                    focus_thread.channel_seq AS run_focus_seq,
                    COALESCE(
                        (SELECT r.error_code FROM agent_runs r
                         WHERE r.agent_id=a.member_id AND r.outcome_code='failed'
                           AND r.error_code IS NOT NULL
                         ORDER BY r.finished_at DESC NULLS LAST,r.id DESC LIMIT 1),
                        (SELECT i.last_error_code FROM inbox_items i
                         WHERE i.member_id=a.member_id AND i.status='pending'
                           AND i.last_error_code IS NOT NULL
                         ORDER BY i.created_at DESC,i.id DESC LIMIT 1)
                    ) AS last_error_code
             FROM agents a JOIN members m ON m.id=a.member_id
             LEFT JOIN computers c ON c.id=a.computer_id
             LEFT JOIN LATERAL (
                 SELECT r.status,r.task_id,r.focus_thread_id FROM agent_runs r
                 WHERE r.agent_id=a.member_id
                   AND r.status NOT IN ('completed','yielded','failed','canceled')
                 ORDER BY r.created_at DESC LIMIT 1
             ) active_run ON true
             LEFT JOIN tasks active_task ON active_task.id=active_run.task_id
             LEFT JOIN messages focus_thread ON focus_thread.id=active_run.focus_thread_id
                 AND focus_thread.placement='root'
             LEFT JOIN channels focus_channel ON focus_channel.id=focus_thread.channel_id
             {predicate}"
        );
        let mut query = sqlx::query_as::<_, AgentRow>(&query);
        for id in ids {
            query = query.bind(id);
        }
        query.fetch_all(&self.pool).await.map_err(map_sqlx)
    }

    pub(in crate::server::adapters) async fn agent_thread_messages(
        &self,
        thread_id: Uuid,
        before: Option<i64>,
        after: Option<i64>,
        limit: i64,
    ) -> Result<Vec<AgentThreadMessageRow>, ApplicationError> {
        sqlx::query_as(
            "SELECT id,channel_seq,author_member_id,content_kind,body_markdown,created_at
             FROM messages WHERE thread_id=$1
               AND ($2::bigint IS NULL OR channel_seq<$2)
               AND ($3::bigint IS NULL OR channel_seq>$3)
             ORDER BY channel_seq LIMIT $4",
        )
        .bind(thread_id)
        .bind(before)
        .bind(after)
        .bind(limit)
        .fetch_all(&self.pool)
        .await
        .map_err(map_sqlx)
    }

    pub(in crate::server::adapters) async fn channel_messages(
        &self,
        channel_id: Uuid,
        around_sequence: Option<i64>,
        limit: i64,
    ) -> Result<Vec<MessageRow>, ApplicationError> {
        match around_sequence {
            Some(sequence) => sqlx::query_as(
                "SELECT * FROM messages WHERE channel_id=$1 AND deleted_at IS NULL
                 ORDER BY abs(channel_seq-$2),channel_seq LIMIT $3",
            )
            .bind(channel_id)
            .bind(sequence)
            .bind(limit)
            .fetch_all(&self.pool)
            .await
            .map_err(map_sqlx),
            None => sqlx::query_as(
                "SELECT * FROM messages WHERE channel_id=$1 AND deleted_at IS NULL
                 ORDER BY channel_seq DESC LIMIT $2",
            )
            .bind(channel_id)
            .bind(limit)
            .fetch_all(&self.pool)
            .await
            .map_err(map_sqlx),
        }
    }

    pub(in crate::server::adapters) async fn computer_options(
        &self,
        space_id: Uuid,
    ) -> Result<Vec<ComputerOptionRow>, ApplicationError> {
        sqlx::query_as(
            "SELECT id,name,hostname,os,connection_status FROM computers
             WHERE space_id=$1 AND deleted_at IS NULL AND connection_status='online'
             ORDER BY name,id",
        )
        .bind(space_id)
        .fetch_all(&self.pool)
        .await
        .map_err(map_sqlx)
    }

    pub(in crate::server::adapters) async fn permission_granted(
        &self,
        member_id: Uuid,
        space_id: Uuid,
        action: &str,
    ) -> Result<bool, ApplicationError> {
        sqlx::query_scalar(
            "SELECT EXISTS(SELECT 1 FROM member_permissions
                           WHERE member_id=$1 AND space_id=$2 AND action_code=$3)",
        )
        .bind(member_id)
        .bind(space_id)
        .bind(action)
        .fetch_one(&self.pool)
        .await
        .map_err(map_sqlx)
    }

    pub(in crate::server::adapters) async fn run_items(
        &self,
        run_id: Uuid,
    ) -> Result<Vec<RunItemRow>, ApplicationError> {
        sqlx::query_as(
            "SELECT i.id,i.kind,i.strength,i.status,i.available_at,ri.disposition
             FROM run_items ri JOIN inbox_items i ON i.id=ri.inbox_item_id
             WHERE ri.run_id=$1 ORDER BY ri.delivery_seq",
        )
        .bind(run_id)
        .fetch_all(&self.pool)
        .await
        .map_err(map_sqlx)
    }

    pub(in crate::server::adapters) async fn focus_body(
        &self,
        thread_id: Uuid,
    ) -> Result<String, ApplicationError> {
        sqlx::query_scalar("SELECT body_markdown FROM messages WHERE id=$1")
            .bind(thread_id)
            .fetch_one(&self.pool)
            .await
            .map_err(map_sqlx)
    }

    pub(in crate::server::adapters) async fn active_run_proof(
        &self,
        computer_id: Uuid,
        token_hash: &str,
        agent_id: Uuid,
        run_id: Uuid,
    ) -> Result<Option<Uuid>, ApplicationError> {
        sqlx::query_scalar(
            "SELECT runs.space_id FROM agent_runs runs
             JOIN agents ON agents.member_id=runs.agent_id
             JOIN computers ON computers.id=agents.computer_id
             WHERE computers.id=$1 AND computers.token_hash=$2 AND computers.deleted_at IS NULL
               AND agents.member_id=$3 AND runs.id=$4 AND runs.status='working'",
        )
        .bind(computer_id)
        .bind(token_hash)
        .bind(agent_id)
        .bind(run_id)
        .fetch_optional(&self.pool)
        .await
        .map_err(map_sqlx)
    }

    pub(in crate::server::adapters) async fn computer_authenticated(
        &self,
        computer_id: Uuid,
        token_hash: &str,
    ) -> Result<bool, ApplicationError> {
        sqlx::query_scalar(
            "SELECT EXISTS(SELECT 1 FROM computers
                           WHERE id=$1 AND token_hash=$2 AND deleted_at IS NULL)",
        )
        .bind(computer_id)
        .bind(token_hash)
        .fetch_one(&self.pool)
        .await
        .map_err(map_sqlx)
    }

    pub(in crate::server::adapters) async fn activity_thread(
        &self,
        thread_id: Uuid,
    ) -> Result<Option<ActivityThreadRow>, ApplicationError> {
        sqlx::query_as(
            "SELECT messages.channel_id,messages.channel_seq,channels.slug
             FROM messages JOIN channels ON channels.id=messages.channel_id
             WHERE messages.id=$1 AND messages.placement='root'",
        )
        .bind(thread_id)
        .fetch_optional(&self.pool)
        .await
        .map_err(map_sqlx)
    }

    pub(in crate::server::adapters) async fn channel_slug(
        &self,
        channel_id: Uuid,
    ) -> Result<Option<String>, ApplicationError> {
        sqlx::query_scalar("SELECT slug FROM channels WHERE id=$1")
            .bind(channel_id)
            .fetch_optional(&self.pool)
            .await
            .map_err(map_sqlx)
    }

    pub(in crate::server::adapters) async fn member_name(
        &self,
        member_id: Uuid,
    ) -> Result<Option<String>, ApplicationError> {
        sqlx::query_scalar("SELECT display_name FROM members WHERE id=$1")
            .bind(member_id)
            .fetch_optional(&self.pool)
            .await
            .map_err(map_sqlx)
    }

    pub(in crate::server::adapters) async fn computer_name(
        &self,
        computer_id: Uuid,
    ) -> Result<Option<String>, ApplicationError> {
        sqlx::query_scalar("SELECT name FROM computers WHERE id=$1")
            .bind(computer_id)
            .fetch_optional(&self.pool)
            .await
            .map_err(map_sqlx)
    }

    pub(in crate::server::adapters) async fn inbox_thread(
        &self,
        item_id: Uuid,
    ) -> Result<Option<Uuid>, ApplicationError> {
        sqlx::query_scalar("SELECT thread_id FROM inbox_items WHERE id=$1")
            .bind(item_id)
            .fetch_optional(&self.pool)
            .await
            .map_err(map_sqlx)
    }

    pub(in crate::server::adapters) async fn inbox_space(
        &self,
        item_id: Uuid,
    ) -> Result<Option<Uuid>, ApplicationError> {
        sqlx::query_scalar("SELECT space_id FROM inbox_items WHERE id=$1")
            .bind(item_id)
            .fetch_optional(&self.pool)
            .await
            .map_err(map_sqlx)
    }

    pub(in crate::server::adapters) async fn task_source_thread(
        &self,
        task_id: Uuid,
    ) -> Result<Option<Uuid>, ApplicationError> {
        sqlx::query_scalar("SELECT source_thread_id FROM tasks WHERE id=$1")
            .bind(task_id)
            .fetch_optional(&self.pool)
            .await
            .map_err(map_sqlx)
    }

    pub(in crate::server::adapters) async fn agent_mention_members(
        &self,
        channel_id: Uuid,
    ) -> Result<Vec<(Uuid, String)>, ApplicationError> {
        sqlx::query_as(
            "SELECT m.id,m.display_name FROM channel_members cm
             JOIN members m ON m.id=cm.member_id
             WHERE cm.channel_id=$1 AND m.retired_at IS NULL",
        )
        .bind(channel_id)
        .fetch_all(&self.pool)
        .await
        .map_err(map_sqlx)
    }

    pub(in crate::server::adapters) async fn task_action_replay(
        &self,
        agent_id: Uuid,
        action: &str,
        key: Uuid,
        task_id: Option<Uuid>,
        space_id: Uuid,
        computer_id: Uuid,
    ) -> Result<Option<Uuid>, ApplicationError> {
        sqlx::query_scalar(
            "SELECT records.resource_id FROM idempotency_records records
             JOIN tasks ON tasks.id=records.resource_id
             JOIN agents ON agents.member_id=records.actor_member_id
             WHERE records.actor_member_id=$1 AND records.action=$2
               AND records.idempotency_key=$3 AND tasks.id=$4 AND tasks.space_id=$5
               AND agents.computer_id=$6",
        )
        .bind(agent_id)
        .bind(action)
        .bind(key)
        .bind(task_id)
        .bind(space_id)
        .bind(computer_id)
        .fetch_optional(&self.pool)
        .await
        .map_err(map_sqlx)
    }

    pub(in crate::server::adapters) async fn channel_leave_replay(
        &self,
        query: ChannelLeaveReplayQuery,
    ) -> Result<Option<Uuid>, ApplicationError> {
        sqlx::query_scalar(
            "SELECT records.resource_id FROM idempotency_records records
             JOIN agents ON agents.member_id=records.actor_member_id
             JOIN agent_runs runs ON runs.id=$5
             WHERE records.actor_member_id=$1 AND records.action='channel.leave'
               AND records.idempotency_key=$2 AND agents.space_id=$3 AND agents.computer_id=$4
               AND runs.agent_id=records.actor_member_id AND runs.space_id=$3
               AND runs.task_id IS NOT DISTINCT FROM $6 AND runs.focus_thread_id=$7
               AND runs.status='working'",
        )
        .bind(query.agent_id)
        .bind(query.key)
        .bind(query.space_id)
        .bind(query.computer_id)
        .bind(query.run_id)
        .bind(query.task_id)
        .bind(query.focus_thread_id)
        .fetch_optional(&self.pool)
        .await
        .map_err(map_sqlx)
    }
}

#[derive(Debug, FromRow)]
pub(in crate::server::adapters) struct SpaceRow {
    pub(in crate::server::adapters) id: Uuid,
    pub(in crate::server::adapters) name: String,
    pub(in crate::server::adapters) slug: String,
    pub(in crate::server::adapters) accent: String,
    pub(in crate::server::adapters) owner_member_id: Uuid,
    pub(in crate::server::adapters) current_member_id: Uuid,
    pub(in crate::server::adapters) general_channel_id: Uuid,
}

#[derive(Debug, FromRow)]
pub(in crate::server::adapters) struct ChannelRow {
    pub(in crate::server::adapters) id: Uuid,
    pub(in crate::server::adapters) space_id: Uuid,
    pub(in crate::server::adapters) kind: String,
    pub(in crate::server::adapters) slug: Option<String>,
    pub(in crate::server::adapters) topic: Option<String>,
    pub(in crate::server::adapters) archived_at: Option<OffsetDateTime>,
    pub(in crate::server::adapters) joined: bool,
}

#[derive(Debug, FromRow)]
pub(in crate::server::adapters) struct MemberRow {
    pub(in crate::server::adapters) id: Uuid,
    pub(in crate::server::adapters) kind: String,
    pub(in crate::server::adapters) display_name: String,
    pub(in crate::server::adapters) access_level: String,
}

#[derive(Debug, FromRow)]
pub(in crate::server::adapters) struct ThreadRootRow {
    pub(in crate::server::adapters) space_id: Uuid,
    pub(in crate::server::adapters) channel_id: Uuid,
}

#[allow(dead_code)]
#[derive(Debug, FromRow)]
pub(in crate::server::adapters) struct MessageRow {
    pub(in crate::server::adapters) id: Uuid,
    pub(in crate::server::adapters) space_id: Uuid,
    pub(in crate::server::adapters) channel_id: Uuid,
    pub(in crate::server::adapters) thread_id: Uuid,
    pub(in crate::server::adapters) channel_seq: i64,
    pub(in crate::server::adapters) placement: String,
    pub(in crate::server::adapters) content_kind: String,
    pub(in crate::server::adapters) reply_to_message_id: Option<Uuid>,
    pub(in crate::server::adapters) author_member_id: Uuid,
    pub(in crate::server::adapters) body_markdown: Option<String>,
    pub(in crate::server::adapters) mention_all: bool,
    pub(in crate::server::adapters) action_channel_id: Option<Uuid>,
    pub(in crate::server::adapters) action_agent_member_id: Option<Uuid>,
    pub(in crate::server::adapters) created_at: OffsetDateTime,
    pub(in crate::server::adapters) edited_at: Option<OffsetDateTime>,
    pub(in crate::server::adapters) deleted_at: Option<OffsetDateTime>,
}

#[derive(Debug, FromRow)]
pub(in crate::server::adapters) struct TaskRefRow {
    pub(in crate::server::adapters) id: Uuid,
    pub(in crate::server::adapters) seq: i64,
    pub(in crate::server::adapters) title: String,
    pub(in crate::server::adapters) status: String,
}

#[derive(Debug, FromRow)]
pub(in crate::server::adapters) struct MessageTaskRow {
    pub(in crate::server::adapters) id: Uuid,
    pub(in crate::server::adapters) seq: i64,
    pub(in crate::server::adapters) title: String,
    pub(in crate::server::adapters) status: String,
    pub(in crate::server::adapters) assignee_agent_member_id: Option<Uuid>,
    pub(in crate::server::adapters) assignee_name: Option<String>,
    pub(in crate::server::adapters) active_focus_thread_id: Option<Uuid>,
}

#[derive(Debug, FromRow)]
pub(in crate::server::adapters) struct AuthorRow {
    pub(in crate::server::adapters) id: Uuid,
    pub(in crate::server::adapters) kind: String,
    pub(in crate::server::adapters) display_name: String,
}

#[allow(dead_code)]
#[derive(Debug, FromRow)]
pub(in crate::server::adapters) struct AttachmentRow {
    pub(in crate::server::adapters) id: Uuid,
    pub(in crate::server::adapters) space_id: Uuid,
    pub(in crate::server::adapters) uploader_member_id: Uuid,
    pub(in crate::server::adapters) name: String,
    pub(in crate::server::adapters) media_type: String,
    pub(in crate::server::adapters) length: Option<i64>,
    pub(in crate::server::adapters) sha256: Option<Vec<u8>>,
    pub(in crate::server::adapters) object_key: String,
    pub(in crate::server::adapters) status: String,
    pub(in crate::server::adapters) created_at: OffsetDateTime,
    pub(in crate::server::adapters) ready_at: Option<OffsetDateTime>,
    pub(in crate::server::adapters) deleted_at: Option<OffsetDateTime>,
}

#[derive(Debug, FromRow)]
pub(in crate::server::adapters) struct AttentionFailureRow {
    pub(in crate::server::adapters) member_id: Uuid,
    pub(in crate::server::adapters) last_error_code: String,
}

#[derive(Debug, FromRow)]
pub(in crate::server::adapters) struct ActionChannelRow {
    pub(in crate::server::adapters) id: Uuid,
    pub(in crate::server::adapters) slug: Option<String>,
    pub(in crate::server::adapters) archived_at: Option<OffsetDateTime>,
}

#[derive(Debug, FromRow)]
pub(in crate::server::adapters) struct ActionAgentRow {
    pub(in crate::server::adapters) member_id: Uuid,
    pub(in crate::server::adapters) lifecycle: String,
    pub(in crate::server::adapters) display_name: String,
    pub(in crate::server::adapters) retired_at: Option<OffsetDateTime>,
}

#[allow(dead_code)]
#[derive(Debug, FromRow)]
pub(in crate::server::adapters) struct TaskRow {
    pub(in crate::server::adapters) id: Uuid,
    pub(in crate::server::adapters) seq: i64,
    pub(in crate::server::adapters) space_id: Uuid,
    pub(in crate::server::adapters) title: String,
    pub(in crate::server::adapters) status: String,
    pub(in crate::server::adapters) source_thread_id: Uuid,
    pub(in crate::server::adapters) creator_member_id: Uuid,
    pub(in crate::server::adapters) assignee_agent_member_id: Option<Uuid>,
    pub(in crate::server::adapters) result_message_id: Option<Uuid>,
    pub(in crate::server::adapters) close_reason_code: Option<String>,
    pub(in crate::server::adapters) close_reason_note: Option<String>,
    pub(in crate::server::adapters) created_at: OffsetDateTime,
    pub(in crate::server::adapters) updated_at: OffsetDateTime,
    pub(in crate::server::adapters) finished_at: Option<OffsetDateTime>,
    pub(in crate::server::adapters) creator_name: String,
    pub(in crate::server::adapters) assignee_name: Option<String>,
}

#[allow(dead_code)]
#[derive(Debug, FromRow)]
pub(in crate::server::adapters) struct RunRow {
    pub(in crate::server::adapters) id: Uuid,
    pub(in crate::server::adapters) space_id: Uuid,
    pub(in crate::server::adapters) agent_id: Uuid,
    pub(in crate::server::adapters) task_id: Option<Uuid>,
    pub(in crate::server::adapters) focus_thread_id: Uuid,
    pub(in crate::server::adapters) status: String,
    pub(in crate::server::adapters) trigger_kind: String,
    pub(in crate::server::adapters) cancel_requested: bool,
    pub(in crate::server::adapters) outcome_code: Option<String>,
    pub(in crate::server::adapters) error_code: Option<String>,
    pub(in crate::server::adapters) continuation_note: Option<String>,
    pub(in crate::server::adapters) created_at: OffsetDateTime,
    pub(in crate::server::adapters) started_at: Option<OffsetDateTime>,
    pub(in crate::server::adapters) finished_at: Option<OffsetDateTime>,
    pub(in crate::server::adapters) agent_name: String,
    pub(in crate::server::adapters) relation: String,
}

#[derive(Debug, FromRow)]
pub(in crate::server::adapters) struct ThreadReferenceRow {
    pub(in crate::server::adapters) id: Uuid,
    pub(in crate::server::adapters) channel_id: Uuid,
    pub(in crate::server::adapters) slug: Option<String>,
    pub(in crate::server::adapters) channel_seq: i64,
}

#[allow(dead_code)]
#[derive(Debug, FromRow)]
pub(in crate::server::adapters) struct TaskSourceRow {
    pub(in crate::server::adapters) space_id: Uuid,
    pub(in crate::server::adapters) thread_id: Uuid,
    pub(in crate::server::adapters) body_markdown: Option<String>,
}

#[derive(Debug, FromRow)]
pub(in crate::server::adapters) struct ComputerAgentRow {
    pub(in crate::server::adapters) member_id: Uuid,
    pub(in crate::server::adapters) space_id: Uuid,
    pub(in crate::server::adapters) role_text: String,
    pub(in crate::server::adapters) role_revision: i64,
    pub(in crate::server::adapters) lifecycle: String,
    pub(in crate::server::adapters) driver_kind: String,
    pub(in crate::server::adapters) driver_config_json: Value,
    pub(in crate::server::adapters) display_name: String,
    pub(in crate::server::adapters) access_level: String,
}

#[allow(dead_code)]
#[derive(Debug, FromRow)]
pub(in crate::server::adapters) struct AgentRow {
    pub(in crate::server::adapters) member_id: Uuid,
    pub(in crate::server::adapters) space_id: Uuid,
    pub(in crate::server::adapters) computer_id: Option<Uuid>,
    pub(in crate::server::adapters) role_text: String,
    pub(in crate::server::adapters) role_revision: i64,
    pub(in crate::server::adapters) lifecycle: String,
    pub(in crate::server::adapters) driver_kind: String,
    pub(in crate::server::adapters) created_at: OffsetDateTime,
    pub(in crate::server::adapters) retired_at: Option<OffsetDateTime>,
    pub(in crate::server::adapters) display_name: String,
    pub(in crate::server::adapters) access_level: String,
    pub(in crate::server::adapters) connection_status: Option<String>,
    pub(in crate::server::adapters) computer_deleted_at: Option<OffsetDateTime>,
    pub(in crate::server::adapters) run_status: Option<String>,
    pub(in crate::server::adapters) run_task_id: Option<Uuid>,
    pub(in crate::server::adapters) run_task_title: Option<String>,
    pub(in crate::server::adapters) run_focus_slug: Option<String>,
    pub(in crate::server::adapters) run_focus_seq: Option<i64>,
    pub(in crate::server::adapters) last_error_code: Option<String>,
}

#[allow(dead_code)]
#[derive(Debug, FromRow)]
pub(in crate::server::adapters) struct AgentThreadMessageRow {
    pub(in crate::server::adapters) id: Uuid,
    pub(in crate::server::adapters) channel_seq: i64,
    pub(in crate::server::adapters) author_member_id: Uuid,
    pub(in crate::server::adapters) content_kind: String,
    pub(in crate::server::adapters) body_markdown: Option<String>,
    pub(in crate::server::adapters) created_at: OffsetDateTime,
}

#[derive(Debug, FromRow)]
pub(in crate::server::adapters) struct ComputerOptionRow {
    pub(in crate::server::adapters) id: Uuid,
    pub(in crate::server::adapters) name: String,
    pub(in crate::server::adapters) hostname: String,
    pub(in crate::server::adapters) os: String,
    pub(in crate::server::adapters) connection_status: String,
}

#[derive(Debug, FromRow)]
pub(in crate::server::adapters) struct RunItemRow {
    pub(in crate::server::adapters) id: Uuid,
    pub(in crate::server::adapters) kind: String,
    pub(in crate::server::adapters) strength: String,
    pub(in crate::server::adapters) status: String,
    pub(in crate::server::adapters) available_at: OffsetDateTime,
    pub(in crate::server::adapters) disposition: Option<String>,
}

#[derive(Debug, FromRow)]
pub(in crate::server::adapters) struct ActivityThreadRow {
    pub(in crate::server::adapters) channel_id: Uuid,
    pub(in crate::server::adapters) channel_seq: i64,
    pub(in crate::server::adapters) slug: Option<String>,
}
