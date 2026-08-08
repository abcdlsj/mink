use super::*;
use crate::server::adapters::postgres::{
    AgentGraphNodeRow, DirectChannelPairRow, DirectMessageStatRow, DirectedInteractionStatRow,
    RecentDirectedMessageRow, RecentDmMessageRow,
};
use crate::server::application::agent_graph::{
    AgentGraphInputs, AgentGraphInteractionKind, DirectChannelPair, DirectMessageStat,
    DirectedInteractionStat, RecentDirectedMessage, RecentDmMessage, build_agent_graph,
};

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
    let direct_channel_pairs = state
        .read
        .direct_channel_pairs(space_id, member_id, viewer_is_governor)
        .await
        .map_err(application_error)?;
    let direct_message_stats = state
        .read
        .direct_message_stats(space_id, member_id, viewer_is_governor)
        .await
        .map_err(application_error)?;
    let mention_stats = state
        .read
        .mention_stats(space_id, member_id, viewer_is_governor)
        .await
        .map_err(application_error)?;
    let reply_stats = state
        .read
        .reply_stats(space_id, member_id, viewer_is_governor)
        .await
        .map_err(application_error)?;
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
        direct_message_stats: direct_message_stats
            .into_iter()
            .map(direct_message_stat)
            .collect(),
        mention_stats: mention_stats
            .into_iter()
            .map(directed_interaction_stat)
            .collect(),
        reply_stats: reply_stats
            .into_iter()
            .map(directed_interaction_stat)
            .collect(),
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

fn direct_message_stat(row: DirectMessageStatRow) -> DirectMessageStat {
    DirectMessageStat {
        member_a_id: MemberId::from_uuid(row.member_a),
        member_b_id: MemberId::from_uuid(row.member_b),
        message_count: u64::try_from(row.message_count).unwrap_or_default(),
        last_message_at: row.last_message_at,
    }
}

fn directed_interaction_stat(row: DirectedInteractionStatRow) -> DirectedInteractionStat {
    DirectedInteractionStat {
        from_member_id: MemberId::from_uuid(row.from_member_id),
        to_member_id: MemberId::from_uuid(row.to_member_id),
        message_count: u64::try_from(row.message_count).unwrap_or_default(),
        last_message_at: row.last_message_at,
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
