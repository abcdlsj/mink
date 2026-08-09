use super::*;
use crate::server::adapters::postgres::{
    AgentGraphChannelStatRow, AgentGraphNodeRow, DirectChannelPairRow, RecentDirectedMessageRow,
    RecentDmMessageRow,
};
use crate::server::application::agent_graph::{
    AgentGraphInputs, AgentGraphInteractionKind, DirectChannelPair, DirectMessageStat,
    DirectedInteractionStat, RecentDirectedMessage, RecentDmMessage, build_agent_graph,
};
use std::{
    collections::{HashMap, HashSet},
    sync::{Arc, Mutex},
};
use time::OffsetDateTime;

/// How long per-Space graph statistics stay cached. The graph is deliberately not real-time: it is
/// recomputed at most once every two hours per Space, then filtered per viewer on each request.
const AGENT_GRAPH_CACHE_TTL: time::Duration = time::Duration::hours(2);

#[derive(Clone, Default)]
pub(in crate::server::adapters) struct AgentGraphCache {
    inner: Arc<Mutex<HashMap<Uuid, AgentGraphCacheEntry>>>,
}

#[derive(Clone)]
struct AgentGraphCacheEntry {
    pairs: Vec<DirectChannelPairRow>,
    stats: Vec<AgentGraphChannelStatRow>,
    computed_at: OffsetDateTime,
}

impl AgentGraphCache {
    async fn stats(
        &self,
        read: &PostgresQueries,
        space_id: Uuid,
    ) -> Result<(Vec<DirectChannelPairRow>, Vec<AgentGraphChannelStatRow>), ApplicationError> {
        let now = OffsetDateTime::now_utc();
        let cached = {
            let guard = self.inner.lock().expect("agent graph cache lock");
            guard
                .get(&space_id)
                .filter(|entry| cache_is_fresh(entry.computed_at, now))
                .cloned()
        };
        if let Some(entry) = cached {
            return Ok((entry.pairs, entry.stats));
        }
        let pairs = read
            .direct_channel_pairs(space_id, Uuid::nil(), true)
            .await?;
        let stats = read.agent_graph_channel_stats(space_id).await?;
        self.inner.lock().expect("agent graph cache lock").insert(
            space_id,
            AgentGraphCacheEntry {
                pairs: pairs.clone(),
                stats: stats.clone(),
                computed_at: now,
            },
        );
        Ok((pairs, stats))
    }
}

fn cache_is_fresh(computed_at: OffsetDateTime, now: OffsetDateTime) -> bool {
    let elapsed_seconds = now.unix_timestamp() - computed_at.unix_timestamp();
    elapsed_seconds >= 0 && elapsed_seconds < AGENT_GRAPH_CACHE_TTL.whole_seconds()
}

pub(super) async fn agent_graph(
    State(state): State<RuntimeState>,
    jar: CookieJar,
    Path(space_id): Path<Uuid>,
) -> Result<Json<AgentGraphResponse>, ApiError> {
    let member_id = current_member(&state, &jar, space_id).await?;
    let viewer_is_governor = state
        .read
        .member_is_governor(member_id)
        .await
        .map_err(application_error)?;
    let nodes = state
        .read
        .agent_graph_nodes(space_id)
        .await
        .map_err(application_error)?;
    let readable = state
        .read
        .readable_channel_ids(space_id, member_id, viewer_is_governor)
        .await
        .map_err(application_error)?;
    let readable: HashSet<Uuid> = readable.into_iter().collect();
    let (direct_channel_pairs, channel_stats) = state
        .agent_graph_cache
        .stats(&state.read, space_id)
        .await
        .map_err(application_error)?;
    let mut direct_message_stats = Vec::new();
    let mut mention_stats = Vec::new();
    let mut reply_stats = Vec::new();
    for row in channel_stats {
        if !readable.contains(&row.channel_id) {
            continue;
        }
        let stat = DirectedInteractionStat {
            from_member_id: MemberId::from_uuid(row.from_member_id),
            to_member_id: MemberId::from_uuid(row.to_member_id),
            message_count: u64::try_from(row.message_count).unwrap_or_default(),
            last_message_at: row.last_message_at,
        };
        match row.source.as_str() {
            "dm" => direct_message_stats.push(DirectMessageStat {
                member_a_id: stat.from_member_id,
                member_b_id: stat.to_member_id,
                message_count: stat.message_count,
                last_message_at: stat.last_message_at,
            }),
            "mention" => mention_stats.push(stat),
            "reply" => reply_stats.push(stat),
            _ => {}
        }
    }
    let recent_dm_messages = state
        .read
        .recent_dm_messages(space_id, member_id)
        .await
        .map_err(application_error)?;
    let recent_directed_messages = state
        .read
        .recent_directed_messages(space_id, member_id)
        .await
        .map_err(application_error)?;

    let graph = build_agent_graph(AgentGraphInputs {
        nodes: nodes.into_iter().map(agent_graph_node).collect(),
        direct_channel_pairs: direct_channel_pairs
            .into_iter()
            .map(direct_channel_pair)
            .collect(),
        direct_message_stats,
        mention_stats,
        reply_stats,
        recent_dm_messages: recent_dm_messages
            .into_iter()
            .map(recent_dm_message)
            .collect(),
        recent_directed_messages: recent_directed_messages
            .into_iter()
            .map(recent_directed_message)
            .collect(),
    });

    Ok(Json(agent_graph_response(graph)))
}

fn agent_graph_node(
    row: AgentGraphNodeRow,
) -> crate::server::application::agent_graph::AgentGraphNode {
    crate::server::application::agent_graph::AgentGraphNode {
        member_id: MemberId::from_uuid(row.member_id),
        display_name: row.display_name,
        role_text: row.role_text,
    }
}

fn direct_channel_pair(row: DirectChannelPairRow) -> DirectChannelPair {
    DirectChannelPair {
        channel_id: ChannelId::from_uuid(row.channel_id),
        member_a_id: MemberId::from_uuid(row.member_a),
        member_b_id: MemberId::from_uuid(row.member_b),
    }
}

fn recent_dm_message(row: RecentDmMessageRow) -> RecentDmMessage {
    RecentDmMessage {
        id: MessageId::from_uuid(row.id),
        channel_id: ChannelId::from_uuid(row.channel_id),
        author_member_id: MemberId::from_uuid(row.author_member_id),
        created_at: row.created_at,
        body_markdown: row.body_markdown.unwrap_or_default(),
    }
}

fn recent_directed_message(row: RecentDirectedMessageRow) -> RecentDirectedMessage {
    RecentDirectedMessage {
        id: MessageId::from_uuid(row.id),
        channel_id: ChannelId::from_uuid(row.channel_id),
        author_member_id: MemberId::from_uuid(row.author_member_id),
        target_member_id: MemberId::from_uuid(row.target_member_id),
        kind: match row.kind.as_str() {
            "mention" => AgentGraphInteractionKind::Mention,
            "reply" => AgentGraphInteractionKind::Reply,
            _ => AgentGraphInteractionKind::Mention,
        },
        created_at: row.created_at,
        body_markdown: row.body_markdown.unwrap_or_default(),
    }
}

fn agent_graph_response(
    graph: crate::server::application::agent_graph::AgentGraph,
) -> AgentGraphResponse {
    AgentGraphResponse {
        nodes: graph
            .nodes
            .into_iter()
            .map(|node| AgentGraphNodeResponse {
                member_id: node.member_id.into_uuid(),
                display_name: node.display_name,
                role_text: node.role_text,
            })
            .collect(),
        edges: graph
            .edges
            .into_iter()
            .map(|edge| AgentGraphEdgeResponse {
                member_a_id: edge.member_a_id.into_uuid(),
                member_b_id: edge.member_b_id.into_uuid(),
                dm_message_count: count(edge.dm_message_count),
                mention_a_to_b: count(edge.mention_a_to_b),
                mention_b_to_a: count(edge.mention_b_to_a),
                reply_a_to_b: count(edge.reply_a_to_b),
                reply_b_to_a: count(edge.reply_b_to_a),
                total_interactions: count(edge.total_interactions),
                last_message_at: edge.last_message_at.map(timestamp),
                recent_messages: edge
                    .recent_messages
                    .into_iter()
                    .map(|message| AgentGraphMessageResponse {
                        id: message.id.into_uuid(),
                        channel_id: message.channel_id.into_uuid(),
                        kind: match message.kind {
                            AgentGraphInteractionKind::Dm => "dm",
                            AgentGraphInteractionKind::Mention => "mention",
                            AgentGraphInteractionKind::Reply => "reply",
                        }
                        .to_owned(),
                        author_member_id: message.author_member_id.into_uuid(),
                        target_member_id: message.target_member_id.into_uuid(),
                        created_at: timestamp(message.created_at),
                        body_markdown: message.body_markdown,
                    })
                    .collect(),
            })
            .collect(),
    }
}

fn count(value: u64) -> i64 {
    i64::try_from(value).unwrap_or(i64::MAX)
}

#[cfg(test)]
mod tests {
    use super::*;
    use time::OffsetDateTime;

    #[test]
    fn cache_entries_are_fresh_for_two_hours() {
        let now = OffsetDateTime::from_unix_timestamp(1_700_000_000).expect("test timestamp");
        assert!(cache_is_fresh(now, now));
        assert!(cache_is_fresh(
            now,
            now + time::Duration::hours(2) - time::Duration::seconds(1)
        ));
        assert!(!cache_is_fresh(now, now + time::Duration::hours(2)));
        assert!(!cache_is_fresh(now, now + time::Duration::hours(3)));
    }
}
