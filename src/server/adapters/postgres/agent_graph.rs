use sqlx::FromRow;
use time::OffsetDateTime;
use uuid::Uuid;

use crate::server::application::ports::ApplicationError;

use super::map_sqlx;
use super::query::PostgresQueries;

#[derive(Debug, FromRow)]
pub(in crate::server::adapters) struct AgentGraphNodeRow {
    pub(in crate::server::adapters) member_id: Uuid,
    pub(in crate::server::adapters) display_name: String,
    pub(in crate::server::adapters) role_text: String,
}

#[derive(Debug, FromRow)]
pub(in crate::server::adapters) struct DirectChannelPairRow {
    pub(in crate::server::adapters) channel_id: Uuid,
    pub(in crate::server::adapters) member_a: Uuid,
    pub(in crate::server::adapters) member_b: Uuid,
}

#[derive(Debug, FromRow)]
pub(in crate::server::adapters) struct DirectMessageStatRow {
    pub(in crate::server::adapters) member_a: Uuid,
    pub(in crate::server::adapters) member_b: Uuid,
    pub(in crate::server::adapters) message_count: i64,
    pub(in crate::server::adapters) last_message_at: Option<OffsetDateTime>,
}

#[derive(Debug, FromRow)]
pub(in crate::server::adapters) struct DirectedInteractionStatRow {
    pub(in crate::server::adapters) from_member_id: Uuid,
    pub(in crate::server::adapters) to_member_id: Uuid,
    pub(in crate::server::adapters) message_count: i64,
    pub(in crate::server::adapters) last_message_at: Option<OffsetDateTime>,
}

#[derive(Debug, FromRow)]
pub(in crate::server::adapters) struct RecentDmMessageRow {
    pub(in crate::server::adapters) id: Uuid,
    pub(in crate::server::adapters) channel_id: Uuid,
    pub(in crate::server::adapters) author_member_id: Uuid,
    pub(in crate::server::adapters) created_at: OffsetDateTime,
    pub(in crate::server::adapters) body_markdown: Option<String>,
}

#[derive(Debug, FromRow)]
pub(in crate::server::adapters) struct RecentDirectedMessageRow {
    pub(in crate::server::adapters) id: Uuid,
    pub(in crate::server::adapters) channel_id: Uuid,
    pub(in crate::server::adapters) author_member_id: Uuid,
    pub(in crate::server::adapters) target_member_id: Uuid,
    pub(in crate::server::adapters) kind: String,
    pub(in crate::server::adapters) created_at: OffsetDateTime,
    pub(in crate::server::adapters) body_markdown: Option<String>,
}

impl PostgresQueries {
    pub(in crate::server::adapters) async fn agent_graph_nodes(
        &self,
        space_id: Uuid,
    ) -> Result<Vec<AgentGraphNodeRow>, ApplicationError> {
        sqlx::query_as(
            "SELECT m.id AS member_id, m.display_name, a.role_text
             FROM members m
             JOIN agents a ON a.member_id = m.id
             WHERE m.space_id = $1 AND m.kind = 'agent' AND m.retired_at IS NULL
             ORDER BY m.display_name, m.id",
        )
        .bind(space_id)
        .fetch_all(&self.pool)
        .await
        .map_err(map_sqlx)
    }

    pub(in crate::server::adapters) async fn direct_channel_pairs(
        &self,
        space_id: Uuid,
        viewer_id: Uuid,
        viewer_is_governor: bool,
    ) -> Result<Vec<DirectChannelPairRow>, ApplicationError> {
        sqlx::query_as(
            "SELECT c.id AS channel_id, cm1.member_id AS member_a, cm2.member_id AS member_b
             FROM channels c
             JOIN channel_members cm1 ON cm1.channel_id = c.id
             JOIN channel_members cm2 ON cm2.channel_id = c.id AND cm2.member_id > cm1.member_id
             WHERE c.space_id = $1 AND c.kind = 'direct'
               AND ($3 OR EXISTS (SELECT 1 FROM channel_members v
                                  WHERE v.channel_id = c.id AND v.member_id = $2))
               AND cm1.member_id IN (SELECT id FROM members
                                     WHERE space_id = $1 AND kind = 'agent' AND retired_at IS NULL)
               AND cm2.member_id IN (SELECT id FROM members
                                     WHERE space_id = $1 AND kind = 'agent' AND retired_at IS NULL)
             ORDER BY cm1.member_id, cm2.member_id",
        )
        .bind(space_id)
        .bind(viewer_id)
        .bind(viewer_is_governor)
        .fetch_all(&self.pool)
        .await
        .map_err(map_sqlx)
    }

    pub(in crate::server::adapters) async fn direct_message_stats(
        &self,
        space_id: Uuid,
        viewer_id: Uuid,
        viewer_is_governor: bool,
    ) -> Result<Vec<DirectMessageStatRow>, ApplicationError> {
        sqlx::query_as(
            "SELECT cm1.member_id AS member_a, cm2.member_id AS member_b,
                    COUNT(m.id) AS message_count, MAX(m.created_at) AS last_message_at
             FROM channels c
             JOIN channel_members cm1 ON cm1.channel_id = c.id
             JOIN channel_members cm2 ON cm2.channel_id = c.id AND cm2.member_id > cm1.member_id
             LEFT JOIN messages m ON m.channel_id = c.id AND m.content_kind = 'text'
                    AND m.deleted_at IS NULL
                    AND m.author_member_id IN (cm1.member_id, cm2.member_id)
             WHERE c.space_id = $1 AND c.kind = 'direct'
               AND ($3 OR EXISTS (SELECT 1 FROM channel_members v
                                  WHERE v.channel_id = c.id AND v.member_id = $2))
               AND cm1.member_id IN (SELECT id FROM members
                                     WHERE space_id = $1 AND kind = 'agent' AND retired_at IS NULL)
               AND cm2.member_id IN (SELECT id FROM members
                                     WHERE space_id = $1 AND kind = 'agent' AND retired_at IS NULL)
             GROUP BY cm1.member_id, cm2.member_id
             HAVING COUNT(m.id) > 0",
        )
        .bind(space_id)
        .bind(viewer_id)
        .bind(viewer_is_governor)
        .fetch_all(&self.pool)
        .await
        .map_err(map_sqlx)
    }

    pub(in crate::server::adapters) async fn mention_stats(
        &self,
        space_id: Uuid,
        viewer_id: Uuid,
        viewer_is_governor: bool,
    ) -> Result<Vec<DirectedInteractionStatRow>, ApplicationError> {
        sqlx::query_as(
            "SELECT m.author_member_id AS from_member_id, mm.member_id AS to_member_id,
                    COUNT(*) AS message_count, MAX(m.created_at) AS last_message_at
             FROM messages m
             JOIN message_mentions mm ON mm.message_id = m.id
             JOIN channels c ON c.id = m.channel_id
             WHERE c.space_id = $1 AND m.content_kind = 'text' AND m.deleted_at IS NULL
               AND ($3 OR EXISTS (SELECT 1 FROM channel_members v
                                  WHERE v.channel_id = c.id AND v.member_id = $2))
               AND m.author_member_id IN (SELECT id FROM members
                                          WHERE space_id = $1 AND kind = 'agent' AND retired_at IS NULL)
               AND mm.member_id IN (SELECT id FROM members
                                    WHERE space_id = $1 AND kind = 'agent' AND retired_at IS NULL)
             GROUP BY m.author_member_id, mm.member_id",
        )
        .bind(space_id)
        .bind(viewer_id)
        .bind(viewer_is_governor)
        .fetch_all(&self.pool)
        .await
        .map_err(map_sqlx)
    }

    pub(in crate::server::adapters) async fn reply_stats(
        &self,
        space_id: Uuid,
        viewer_id: Uuid,
        viewer_is_governor: bool,
    ) -> Result<Vec<DirectedInteractionStatRow>, ApplicationError> {
        sqlx::query_as(
            "SELECT r.author_member_id AS from_member_id, p.author_member_id AS to_member_id,
                    COUNT(*) AS message_count, MAX(r.created_at) AS last_message_at
             FROM messages r
             JOIN messages p ON p.id = r.reply_to_message_id
             JOIN channels c ON c.id = r.channel_id
             WHERE c.space_id = $1 AND r.placement = 'reply'
               AND r.deleted_at IS NULL AND p.deleted_at IS NULL
               AND ($3 OR EXISTS (SELECT 1 FROM channel_members v
                                  WHERE v.channel_id = c.id AND v.member_id = $2))
               AND r.author_member_id IN (SELECT id FROM members
                                          WHERE space_id = $1 AND kind = 'agent' AND retired_at IS NULL)
               AND p.author_member_id IN (SELECT id FROM members
                                          WHERE space_id = $1 AND kind = 'agent' AND retired_at IS NULL)
             GROUP BY r.author_member_id, p.author_member_id",
        )
        .bind(space_id)
        .bind(viewer_id)
        .bind(viewer_is_governor)
        .fetch_all(&self.pool)
        .await
        .map_err(map_sqlx)
    }

    pub(in crate::server::adapters) async fn recent_dm_messages(
        &self,
        space_id: Uuid,
        viewer_id: Uuid,
    ) -> Result<Vec<RecentDmMessageRow>, ApplicationError> {
        sqlx::query_as(
            "SELECT id, channel_id, author_member_id, created_at, body_markdown
             FROM (
                SELECT m.id, m.channel_id, m.author_member_id, m.created_at, m.body_markdown,
                       ROW_NUMBER() OVER (
                           PARTITION BY m.channel_id ORDER BY m.created_at DESC, m.id DESC
                       ) AS rn
                FROM messages m
                JOIN channels c ON c.id = m.channel_id
                WHERE c.space_id = $1 AND c.kind = 'direct'
                  AND m.content_kind = 'text' AND m.deleted_at IS NULL
                  AND EXISTS (SELECT 1 FROM channel_members v
                              WHERE v.channel_id = c.id AND v.member_id = $2)
             ) ranked
             WHERE ranked.rn <= 5
             ORDER BY ranked.created_at DESC",
        )
        .bind(space_id)
        .bind(viewer_id)
        .fetch_all(&self.pool)
        .await
        .map_err(map_sqlx)
    }

    pub(in crate::server::adapters) async fn recent_directed_messages(
        &self,
        space_id: Uuid,
        viewer_id: Uuid,
    ) -> Result<Vec<RecentDirectedMessageRow>, ApplicationError> {
        sqlx::query_as(
            "SELECT id, channel_id, author_member_id, target_member_id, kind, created_at, body_markdown
             FROM (
                SELECT m.id, m.channel_id, m.author_member_id, mm.member_id AS target_member_id,
                       'mention' AS kind, m.created_at, m.body_markdown,
                       ROW_NUMBER() OVER (
                           PARTITION BY m.author_member_id, mm.member_id
                           ORDER BY m.created_at DESC, m.id DESC
                       ) AS rn
                FROM messages m
                JOIN message_mentions mm ON mm.message_id = m.id
                JOIN channels c ON c.id = m.channel_id
                WHERE c.space_id = $1 AND m.content_kind = 'text' AND m.deleted_at IS NULL
                  AND EXISTS (SELECT 1 FROM channel_members v
                              WHERE v.channel_id = c.id AND v.member_id = $2)
                  AND m.author_member_id IN (SELECT id FROM members
                                             WHERE space_id = $1 AND kind = 'agent' AND retired_at IS NULL)
                  AND mm.member_id IN (SELECT id FROM members
                                       WHERE space_id = $1 AND kind = 'agent' AND retired_at IS NULL)
                UNION ALL
                SELECT r.id, r.channel_id, r.author_member_id, p.author_member_id AS target_member_id,
                       'reply' AS kind, r.created_at, r.body_markdown,
                       ROW_NUMBER() OVER (
                           PARTITION BY r.author_member_id, p.author_member_id
                           ORDER BY r.created_at DESC, r.id DESC
                       ) AS rn
                FROM messages r
                JOIN messages p ON p.id = r.reply_to_message_id
                JOIN channels c ON c.id = r.channel_id
                WHERE c.space_id = $1 AND r.placement = 'reply'
                  AND r.deleted_at IS NULL AND p.deleted_at IS NULL
                  AND EXISTS (SELECT 1 FROM channel_members v
                              WHERE v.channel_id = c.id AND v.member_id = $2)
                  AND r.author_member_id IN (SELECT id FROM members
                                             WHERE space_id = $1 AND kind = 'agent' AND retired_at IS NULL)
                  AND p.author_member_id IN (SELECT id FROM members
                                             WHERE space_id = $1 AND kind = 'agent' AND retired_at IS NULL)
             ) ranked
             WHERE ranked.rn <= 5
             ORDER BY ranked.created_at DESC",
        )
        .bind(space_id)
        .bind(viewer_id)
        .fetch_all(&self.pool)
        .await
        .map_err(map_sqlx)
    }
}
