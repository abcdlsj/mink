use std::collections::{BTreeMap, HashMap};

use time::OffsetDateTime;

use crate::ids::{ChannelId, MemberId, MessageId};

/// One Agent node in the coordination graph. Only Agents appear as nodes; Humans are not part of the
/// Agent-to-Agent relationship view.
#[derive(Clone, Debug, Eq, PartialEq)]
pub(in crate::server) struct AgentGraphNode {
    pub(in crate::server) member_id: MemberId,
    pub(in crate::server) display_name: String,
    pub(in crate::server) role_text: String,
}

#[derive(Clone, Copy, Debug, Eq, PartialEq)]
pub(in crate::server) enum AgentGraphInteractionKind {
    Dm,
    Mention,
    Reply,
}

/// A single Message-level interaction used to render the communication chain between two Agents.
/// `body_markdown` is only included for Messages the requesting Member can already read.
#[derive(Clone, Debug, Eq, PartialEq)]
pub(in crate::server) struct AgentGraphMessage {
    pub(in crate::server) id: MessageId,
    pub(in crate::server) channel_id: ChannelId,
    pub(in crate::server) kind: AgentGraphInteractionKind,
    pub(in crate::server) author_member_id: MemberId,
    pub(in crate::server) target_member_id: MemberId,
    pub(in crate::server) created_at: OffsetDateTime,
    pub(in crate::server) body_markdown: String,
}

/// An undirected relationship between two Agents. Directions are still counted separately so the UI
/// can show who initiated mentions and replies.
#[derive(Clone, Debug, Eq, PartialEq)]
pub(in crate::server) struct AgentGraphEdge {
    pub(in crate::server) member_a_id: MemberId,
    pub(in crate::server) member_b_id: MemberId,
    pub(in crate::server) dm_message_count: u64,
    pub(in crate::server) mention_a_to_b: u64,
    pub(in crate::server) mention_b_to_a: u64,
    pub(in crate::server) reply_a_to_b: u64,
    pub(in crate::server) reply_b_to_a: u64,
    pub(in crate::server) total_interactions: u64,
    pub(in crate::server) last_message_at: Option<OffsetDateTime>,
    pub(in crate::server) recent_messages: Vec<AgentGraphMessage>,
}

#[derive(Clone, Debug, Default, Eq, PartialEq)]
pub(in crate::server) struct AgentGraph {
    pub(in crate::server) nodes: Vec<AgentGraphNode>,
    pub(in crate::server) edges: Vec<AgentGraphEdge>,
}

/// Raw DM aggregate from the adapter, normalized to the smaller member first when built.
#[derive(Clone, Debug, Eq, PartialEq)]
pub(in crate::server) struct DirectMessageStat {
    pub(in crate::server) member_a_id: MemberId,
    pub(in crate::server) member_b_id: MemberId,
    pub(in crate::server) message_count: u64,
    pub(in crate::server) last_message_at: Option<OffsetDateTime>,
}

/// Raw directed aggregate (mention or reply) from the adapter.
#[derive(Clone, Debug, Eq, PartialEq)]
pub(in crate::server) struct DirectedInteractionStat {
    pub(in crate::server) from_member_id: MemberId,
    pub(in crate::server) to_member_id: MemberId,
    pub(in crate::server) message_count: u64,
    pub(in crate::server) last_message_at: Option<OffsetDateTime>,
}

/// Channel membership for every visible direct channel that contains exactly two Agents.
#[derive(Clone, Copy, Debug, Eq, PartialEq)]
pub(in crate::server) struct DirectChannelPair {
    pub(in crate::server) channel_id: ChannelId,
    pub(in crate::server) member_a_id: MemberId,
    pub(in crate::server) member_b_id: MemberId,
}

#[derive(Clone, Debug, Eq, PartialEq)]
pub(in crate::server) struct RecentDmMessage {
    pub(in crate::server) id: MessageId,
    pub(in crate::server) channel_id: ChannelId,
    pub(in crate::server) author_member_id: MemberId,
    pub(in crate::server) created_at: OffsetDateTime,
    pub(in crate::server) body_markdown: String,
}

#[derive(Clone, Debug, Eq, PartialEq)]
pub(in crate::server) struct RecentDirectedMessage {
    pub(in crate::server) id: MessageId,
    pub(in crate::server) channel_id: ChannelId,
    pub(in crate::server) author_member_id: MemberId,
    pub(in crate::server) target_member_id: MemberId,
    pub(in crate::server) kind: AgentGraphInteractionKind,
    pub(in crate::server) created_at: OffsetDateTime,
    pub(in crate::server) body_markdown: String,
}

#[derive(Clone, Debug, Default, Eq, PartialEq)]
pub(in crate::server) struct AgentGraphInputs {
    pub(in crate::server) nodes: Vec<AgentGraphNode>,
    pub(in crate::server) direct_channel_pairs: Vec<DirectChannelPair>,
    pub(in crate::server) direct_message_stats: Vec<DirectMessageStat>,
    pub(in crate::server) mention_stats: Vec<DirectedInteractionStat>,
    pub(in crate::server) reply_stats: Vec<DirectedInteractionStat>,
    pub(in crate::server) recent_dm_messages: Vec<RecentDmMessage>,
    pub(in crate::server) recent_directed_messages: Vec<RecentDirectedMessage>,
}

const MAX_RECENT_MESSAGES_PER_EDGE: usize = 5;

/// Merges raw adapter aggregates into the undirected Agent graph.
///
/// A pair key always stores the smaller member id first, so DM stats arrive from either direction
/// and still land on the same edge. Stats or recent Messages naming a member that is not a node are
/// dropped: the adapter already filters to Agents, and this keeps the graph consistent if a retired
/// Agent row slips through.
pub(in crate::server) fn build_agent_graph(inputs: AgentGraphInputs) -> AgentGraph {
    let nodes = inputs.nodes;
    let known: std::collections::HashSet<MemberId> =
        nodes.iter().map(|node| node.member_id).collect();
    let mut edges: BTreeMap<(MemberId, MemberId), EdgeBuilder> = BTreeMap::new();

    for stat in inputs.direct_message_stats {
        let Some((a, b)) = normalize_pair(stat.member_a_id, stat.member_b_id, &known) else {
            continue;
        };
        let edge = edges.entry((a, b)).or_default();
        edge.dm_message_count += stat.message_count;
        edge.last_message_at = max_time(edge.last_message_at, stat.last_message_at);
    }
    for (stats, field) in [
        (inputs.mention_stats, DirectedField::MentionAtoB),
        (inputs.reply_stats, DirectedField::ReplyAtoB),
    ] {
        for stat in stats {
            let Some((from, to)) = normalize_pair(stat.from_member_id, stat.to_member_id, &known)
            else {
                continue;
            };
            let edge = edges.entry((from, to)).or_default();
            edge.apply_directed(field, from == stat.from_member_id, stat.message_count);
            edge.last_message_at = max_time(edge.last_message_at, stat.last_message_at);
        }
    }

    let mut dm_pair_by_channel = HashMap::new();
    for pair in inputs.direct_channel_pairs {
        let Some((a, b)) = normalize_pair(pair.member_a_id, pair.member_b_id, &known) else {
            continue;
        };
        dm_pair_by_channel.insert(pair.channel_id, (a, b));
    }
    for message in inputs.recent_dm_messages {
        let Some(&(a, b)) = dm_pair_by_channel.get(&message.channel_id) else {
            continue;
        };
        let target = if message.author_member_id == a { b } else { a };
        let edge = edges.entry((a, b)).or_default();
        edge.recent_messages.push(AgentGraphMessage {
            id: message.id,
            channel_id: message.channel_id,
            kind: AgentGraphInteractionKind::Dm,
            author_member_id: message.author_member_id,
            target_member_id: target,
            created_at: message.created_at,
            body_markdown: message.body_markdown,
        });
    }
    for message in inputs.recent_directed_messages {
        let Some((a, b)) =
            normalize_pair(message.author_member_id, message.target_member_id, &known)
        else {
            continue;
        };
        let edge = edges.entry((a, b)).or_default();
        edge.recent_messages.push(AgentGraphMessage {
            id: message.id,
            channel_id: message.channel_id,
            kind: message.kind,
            author_member_id: message.author_member_id,
            target_member_id: message.target_member_id,
            created_at: message.created_at,
            body_markdown: message.body_markdown,
        });
    }

    let edges = edges
        .into_iter()
        .map(|((a, b), builder)| {
            let total_interactions = builder.total();
            let last_message_at = builder.last_message_at;
            let mut recent_messages = builder.recent_messages;
            recent_messages.sort_by_key(|right| std::cmp::Reverse(right.created_at));
            recent_messages.truncate(MAX_RECENT_MESSAGES_PER_EDGE);
            AgentGraphEdge {
                member_a_id: a,
                member_b_id: b,
                dm_message_count: builder.dm_message_count,
                mention_a_to_b: builder.mention_a_to_b,
                mention_b_to_a: builder.mention_b_to_a,
                reply_a_to_b: builder.reply_a_to_b,
                reply_b_to_a: builder.reply_b_to_a,
                total_interactions,
                last_message_at,
                recent_messages,
            }
        })
        .collect();

    AgentGraph { nodes, edges }
}

#[derive(Clone, Copy)]
enum DirectedField {
    MentionAtoB,
    ReplyAtoB,
}

#[derive(Default)]
struct EdgeBuilder {
    dm_message_count: u64,
    mention_a_to_b: u64,
    mention_b_to_a: u64,
    reply_a_to_b: u64,
    reply_b_to_a: u64,
    last_message_at: Option<OffsetDateTime>,
    recent_messages: Vec<AgentGraphMessage>,
}

impl EdgeBuilder {
    fn apply_directed(&mut self, field: DirectedField, forward: bool, count: u64) {
        let slot = match (field, forward) {
            (DirectedField::MentionAtoB, true) => &mut self.mention_a_to_b,
            (DirectedField::MentionAtoB, false) => &mut self.mention_b_to_a,
            (DirectedField::ReplyAtoB, true) => &mut self.reply_a_to_b,
            (DirectedField::ReplyAtoB, false) => &mut self.reply_b_to_a,
        };
        *slot += count;
    }

    fn total(&self) -> u64 {
        self.dm_message_count
            + self.mention_a_to_b
            + self.mention_b_to_a
            + self.reply_a_to_b
            + self.reply_b_to_a
    }
}

fn normalize_pair(
    first: MemberId,
    second: MemberId,
    known: &std::collections::HashSet<MemberId>,
) -> Option<(MemberId, MemberId)> {
    if !known.contains(&first) || !known.contains(&second) || first == second {
        return None;
    }
    Some(if first < second {
        (first, second)
    } else {
        (second, first)
    })
}

fn max_time(left: Option<OffsetDateTime>, right: Option<OffsetDateTime>) -> Option<OffsetDateTime> {
    match (left, right) {
        (Some(left), Some(right)) => Some(left.max(right)),
        (Some(left), None) => Some(left),
        (None, Some(right)) => Some(right),
        (None, None) => None,
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::ids::{ChannelId, MessageId};
    use uuid::Uuid;

    fn member(id: u128) -> MemberId {
        MemberId::from_uuid(Uuid::from_u128(id))
    }

    fn message(id: u128) -> MessageId {
        MessageId::from_uuid(Uuid::from_u128(id))
    }

    fn channel(id: u128) -> ChannelId {
        ChannelId::from_uuid(Uuid::from_u128(id))
    }

    fn at(seconds: i64) -> OffsetDateTime {
        OffsetDateTime::from_unix_timestamp(seconds).expect("test timestamp")
    }

    fn node(id: u128, name: &str) -> AgentGraphNode {
        AgentGraphNode {
            member_id: member(id),
            display_name: name.to_owned(),
            role_text: "role".to_owned(),
        }
    }

    #[test]
    fn dm_stats_normalize_pair_direction() {
        let graph = build_agent_graph(AgentGraphInputs {
            nodes: vec![node(1, "a"), node(2, "b")],
            direct_message_stats: vec![DirectMessageStat {
                member_a_id: member(2),
                member_b_id: member(1),
                message_count: 4,
                last_message_at: Some(at(10)),
            }],
            ..Default::default()
        });
        assert_eq!(graph.edges.len(), 1);
        let edge = &graph.edges[0];
        assert_eq!(edge.member_a_id, member(1));
        assert_eq!(edge.member_b_id, member(2));
        assert_eq!(edge.dm_message_count, 4);
        assert_eq!(edge.total_interactions, 4);
        assert_eq!(edge.last_message_at, Some(at(10)));
    }

    #[test]
    fn directed_interactions_keep_direction_and_sum() {
        let graph = build_agent_graph(AgentGraphInputs {
            nodes: vec![node(1, "a"), node(2, "b")],
            mention_stats: vec![
                DirectedInteractionStat {
                    from_member_id: member(1),
                    to_member_id: member(2),
                    message_count: 2,
                    last_message_at: Some(at(20)),
                },
                DirectedInteractionStat {
                    from_member_id: member(2),
                    to_member_id: member(1),
                    message_count: 1,
                    last_message_at: Some(at(30)),
                },
            ],
            reply_stats: vec![DirectedInteractionStat {
                from_member_id: member(2),
                to_member_id: member(1),
                message_count: 3,
                last_message_at: Some(at(25)),
            }],
            ..Default::default()
        });
        let edge = &graph.edges[0];
        assert_eq!(edge.mention_a_to_b, 2);
        assert_eq!(edge.mention_b_to_a, 1);
        assert_eq!(edge.reply_a_to_b, 0);
        assert_eq!(edge.reply_b_to_a, 3);
        assert_eq!(edge.total_interactions, 6);
        assert_eq!(edge.last_message_at, Some(at(30)));
    }

    #[test]
    fn recent_dm_messages_map_to_pair_and_sort_descending() {
        let graph = build_agent_graph(AgentGraphInputs {
            nodes: vec![node(1, "a"), node(2, "b")],
            direct_channel_pairs: vec![DirectChannelPair {
                channel_id: channel(10),
                member_a_id: member(2),
                member_b_id: member(1),
            }],
            recent_dm_messages: vec![
                RecentDmMessage {
                    id: message(100),
                    channel_id: channel(10),
                    author_member_id: member(1),
                    created_at: at(1),
                    body_markdown: "first".to_owned(),
                },
                RecentDmMessage {
                    id: message(101),
                    channel_id: channel(10),
                    author_member_id: member(2),
                    created_at: at(2),
                    body_markdown: "second".to_owned(),
                },
            ],
            ..Default::default()
        });
        let edge = &graph.edges[0];
        assert_eq!(edge.recent_messages.len(), 2);
        assert_eq!(edge.recent_messages[0].body_markdown, "second");
        assert_eq!(edge.recent_messages[0].target_member_id, member(1));
        assert_eq!(edge.recent_messages[1].body_markdown, "first");
        assert_eq!(edge.recent_messages[1].target_member_id, member(2));
    }

    #[test]
    fn recent_messages_are_capped_and_unknown_members_are_dropped() {
        let inputs = AgentGraphInputs {
            nodes: vec![node(1, "a"), node(2, "b")],
            direct_channel_pairs: vec![DirectChannelPair {
                channel_id: channel(10),
                member_a_id: member(1),
                member_b_id: member(2),
            }],
            direct_message_stats: vec![DirectMessageStat {
                member_a_id: member(1),
                member_b_id: member(2),
                message_count: 7,
                last_message_at: Some(at(7)),
            }],
            recent_dm_messages: (0..7)
                .map(|index| RecentDmMessage {
                    id: message(200 + index),
                    channel_id: channel(10),
                    author_member_id: member(1),
                    created_at: at(index as i64),
                    body_markdown: format!("m{index}"),
                })
                .collect(),
            mention_stats: vec![DirectedInteractionStat {
                from_member_id: member(1),
                to_member_id: member(99),
                message_count: 5,
                last_message_at: Some(at(40)),
            }],
            ..Default::default()
        };
        let graph = build_agent_graph(inputs);
        assert_eq!(graph.edges.len(), 1);
        let edge = &graph.edges[0];
        assert_eq!(edge.recent_messages.len(), 5);
        assert_eq!(edge.mention_a_to_b, 0);
        assert_eq!(edge.total_interactions, 7);
    }

    #[test]
    fn graph_without_interactions_keeps_all_nodes() {
        let graph = build_agent_graph(AgentGraphInputs {
            nodes: vec![node(1, "a"), node(2, "b"), node(3, "c")],
            ..Default::default()
        });
        assert_eq!(graph.nodes.len(), 3);
        assert!(graph.edges.is_empty());
    }
}
