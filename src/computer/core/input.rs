use std::{collections::HashSet, fmt};

use serde::{Deserialize, Serialize};
use sha2::{Digest, Sha256};

use time::OffsetDateTime;

use crate::ids::{
    AgentId, ChannelId, InboxItemId, MemberId, MessageId, NoticeId, SpaceId, TaskId, ThreadId,
};

use super::scheduler::WorkStrength;

#[derive(Clone, Deserialize, Eq, PartialEq, Serialize)]
pub(in crate::computer) struct RunInput {
    #[serde(alias = "global_contract")]
    pub(in crate::computer) product_contract: String,
    pub(in crate::computer) agent: AgentInput,
    pub(in crate::computer) work: WorkInput,
    pub(in crate::computer) context: RunContextInput,
    pub(in crate::computer) channel_members: Vec<ChannelMemberInput>,
}

#[derive(Clone, Deserialize, Eq, PartialEq, Serialize)]
pub(in crate::computer) struct ChannelMemberInput {
    pub(in crate::computer) member_id: MemberId,
    pub(in crate::computer) display_name: String,
}

#[derive(Clone, Deserialize, Eq, PartialEq, Serialize)]
pub(in crate::computer) struct AgentInput {
    pub(in crate::computer) agent_id: AgentId,
    pub(in crate::computer) space_id: SpaceId,
    pub(in crate::computer) identity: String,
    pub(in crate::computer) role_revision: u64,
    pub(in crate::computer) role: String,
    pub(in crate::computer) memory: Vec<MemoryEntryInput>,
}

#[derive(Clone, Deserialize, Eq, PartialEq, Serialize)]
pub(in crate::computer) struct MemoryEntryInput {
    pub(in crate::computer) path: String,
    pub(in crate::computer) size: u64,
    pub(in crate::computer) sha256: String,
    #[serde(with = "time::serde::rfc3339")]
    pub(in crate::computer) updated_at: OffsetDateTime,
}

#[derive(Clone, Deserialize, Eq, PartialEq, Serialize)]
pub(in crate::computer) struct WorkInput {
    pub(in crate::computer) task: Option<TaskInput>,
    pub(in crate::computer) linked_thread_ids: Vec<ThreadId>,
    pub(in crate::computer) public_result_message_id: Option<MessageId>,
}

#[derive(Clone, Deserialize, Eq, PartialEq, Serialize)]
pub(in crate::computer) struct TaskInput {
    pub(in crate::computer) task_id: TaskId,
    pub(in crate::computer) seq: u64,
    pub(in crate::computer) title: String,
    pub(in crate::computer) status: String,
}

#[derive(Clone, Deserialize, Eq, PartialEq, Serialize)]
pub(in crate::computer) struct RunContextInput {
    pub(in crate::computer) focus_thread_id: ThreadId,
    pub(in crate::computer) message_snapshot_sequence: u64,
    pub(in crate::computer) focus_messages: Vec<ContextMessageInput>,
    pub(in crate::computer) channel_id: ChannelId,
    pub(in crate::computer) channel_snapshot_sequence: u64,
    pub(in crate::computer) channel_activity: Vec<ChannelActivityInput>,
    pub(in crate::computer) dispatched_items: Vec<DispatchedItemInput>,
}

#[derive(Clone, Deserialize, Eq, PartialEq, Serialize)]
pub(in crate::computer) struct ChannelActivityInput {
    pub(in crate::computer) thread_id: ThreadId,
    pub(in crate::computer) channel_seq: u64,
    pub(in crate::computer) message: ContextMessageInput,
}

#[derive(Clone, Deserialize, Eq, PartialEq, Serialize)]
pub(in crate::computer) struct ContextMessageInput {
    pub(in crate::computer) message_id: MessageId,
    pub(in crate::computer) author_member_id: MemberId,
    pub(in crate::computer) body: String,
}

impl fmt::Debug for ContextMessageInput {
    fn fmt(&self, formatter: &mut fmt::Formatter<'_>) -> fmt::Result {
        formatter
            .debug_struct("ContextMessageInput")
            .field("message_id", &self.message_id)
            .field("author_member_id", &self.author_member_id)
            .field("has_body", &!self.body.is_empty())
            .finish()
    }
}

#[derive(Clone, Deserialize, Eq, PartialEq, Serialize)]
pub(in crate::computer) struct DispatchedItemInput {
    pub(in crate::computer) item_id: InboxItemId,
    pub(in crate::computer) source_kind: String,
    pub(in crate::computer) strength: WorkStrength,
    pub(in crate::computer) task_id: Option<TaskId>,
    pub(in crate::computer) channel_id: crate::ids::ChannelId,
    pub(in crate::computer) thread_id: ThreadId,
    pub(in crate::computer) message_id: Option<MessageId>,
    pub(in crate::computer) content: Option<String>,
    pub(in crate::computer) activity_events: Vec<ActivityEventInput>,
}

#[derive(Clone, Debug, Deserialize, Eq, PartialEq, Serialize)]
pub(in crate::computer) struct ActivityEventInput {
    pub(in crate::computer) sequence: u64,
    pub(in crate::computer) kind: String,
    pub(in crate::computer) message_id: Option<MessageId>,
    pub(in crate::computer) member_id: Option<MemberId>,
}

#[derive(Clone, Debug, Deserialize, Eq, PartialEq, Serialize)]
pub(in crate::computer) struct AttentionNoticeInput {
    pub(in crate::computer) notice_id: NoticeId,
    pub(in crate::computer) source_kind: String,
    pub(in crate::computer) location: NoticeLocationInput,
    pub(in crate::computer) explicit_human_redirect: bool,
    pub(in crate::computer) arrived_at: OffsetDateTime,
}

#[derive(Clone, Copy, Debug, Deserialize, Eq, PartialEq, Serialize)]
pub(in crate::computer) enum NoticeLocationInput {
    Restricted,
    Visible {
        task_id: Option<TaskId>,
        thread_id: ThreadId,
    },
}

impl DispatchedItemInput {
    pub(in crate::computer) fn content_hash(&self) -> String {
        let mut digest = Sha256::new();
        digest.update(self.item_id.to_string().as_bytes());
        digest.update(self.source_kind.as_bytes());
        digest.update(match self.strength {
            WorkStrength::Hard => b"hard" as &[u8],
            WorkStrength::Ambient => b"ambient" as &[u8],
        });
        digest.update(format!("{:?}", self.task_id).as_bytes());
        digest.update(self.channel_id.to_string().as_bytes());
        digest.update(self.thread_id.to_string().as_bytes());
        if let Some(message_id) = self.message_id {
            digest.update(message_id.to_string().as_bytes());
        }
        if let Some(content) = &self.content {
            digest.update(content.as_bytes());
        }
        for event in &self.activity_events {
            digest.update(event.sequence.to_le_bytes());
            digest.update(event.kind.as_bytes());
            if let Some(message_id) = event.message_id {
                digest.update(message_id.to_string().as_bytes());
            }
            if let Some(member_id) = event.member_id {
                digest.update(member_id.to_string().as_bytes());
            }
        }
        hex::encode(digest.finalize())
    }
}

impl fmt::Debug for DispatchedItemInput {
    fn fmt(&self, formatter: &mut fmt::Formatter<'_>) -> fmt::Result {
        formatter
            .debug_struct("DispatchedItemInput")
            .field("item_id", &self.item_id)
            .field("source_kind", &self.source_kind)
            .field("strength", &self.strength)
            .field("task_id", &self.task_id)
            .field("channel_id", &self.channel_id)
            .field("thread_id", &self.thread_id)
            .field("has_content", &self.content.is_some())
            .finish()
    }
}

impl RunInput {
    pub(in crate::computer) fn content_hash(&self) -> String {
        let mut digest = Sha256::new();
        digest.update(self.product_contract.as_bytes());
        digest.update(self.agent.agent_id.to_string().as_bytes());
        digest.update(self.agent.space_id.to_string().as_bytes());
        digest.update(self.agent.identity.as_bytes());
        digest.update(self.agent.role_revision.to_le_bytes());
        digest.update(self.agent.role.as_bytes());
        for entry in &self.agent.memory {
            digest.update(entry.path.as_bytes());
            digest.update(entry.size.to_le_bytes());
            digest.update(entry.sha256.as_bytes());
            digest.update(entry.updated_at.unix_timestamp_nanos().to_le_bytes());
        }
        if let Some(task) = &self.work.task {
            digest.update(task.task_id.to_string().as_bytes());
            digest.update(task.title.as_bytes());
            digest.update(task.status.as_bytes());
        }
        for thread_id in &self.work.linked_thread_ids {
            digest.update(thread_id.to_string().as_bytes());
        }
        if let Some(message_id) = self.work.public_result_message_id {
            digest.update(message_id.to_string().as_bytes());
        }
        digest.update(self.context.focus_thread_id.to_string().as_bytes());
        digest.update(self.context.message_snapshot_sequence.to_le_bytes());
        for message in &self.context.focus_messages {
            digest.update(message.message_id.to_string().as_bytes());
            digest.update(message.author_member_id.to_string().as_bytes());
            digest.update(message.body.as_bytes());
        }
        digest.update(self.context.channel_id.to_string().as_bytes());
        digest.update(self.context.channel_snapshot_sequence.to_le_bytes());
        for activity in &self.context.channel_activity {
            digest.update(activity.thread_id.to_string().as_bytes());
            digest.update(activity.channel_seq.to_le_bytes());
            digest.update(activity.message.message_id.to_string().as_bytes());
            digest.update(activity.message.author_member_id.to_string().as_bytes());
            digest.update(activity.message.body.as_bytes());
        }
        for item in &self.context.dispatched_items {
            digest.update(item.content_hash().as_bytes());
        }
        for member in &self.channel_members {
            digest.update(member.member_id.to_string().as_bytes());
            digest.update(member.display_name.as_bytes());
        }
        hex::encode(digest.finalize())
    }

    /// Model-facing view: keeps only facts the model needs, drops internal sync fields,
    /// empty fields and duplicated bodies, and caps the focus message window.
    pub(in crate::computer) fn model_view(&self) -> serde_json::Value {
        const FOCUS_MESSAGE_WINDOW: usize = 5;

        // The root is always retained; only the last 5 replies are kept in full,
        // older messages are read on demand via thread.read.
        let mut retained: Vec<&ContextMessageInput> = Vec::new();
        let mut omitted_replies = 0usize;
        if let Some(root) = self.context.focus_messages.first() {
            retained.push(root);
        }
        let replies = &self.context.focus_messages[1..];
        if replies.len() > FOCUS_MESSAGE_WINDOW {
            omitted_replies = replies.len() - FOCUS_MESSAGE_WINDOW;
            retained.extend(&replies[replies.len() - FOCUS_MESSAGE_WINDOW..]);
        } else {
            retained.extend(replies);
        }
        let retained_ids: HashSet<MessageId> =
            retained.iter().map(|message| message.message_id).collect();

        let focus_messages = retained
            .iter()
            .filter_map(|message| serde_json::to_value(message).ok())
            .collect::<Vec<_>>();

        // Inject only the item identity when the source message is inside the window;
        // the body is already in focus_messages. Bodies outside the window are kept.
        let dispatched_items = self
            .context
            .dispatched_items
            .iter()
            .map(|item| {
                let mut view = serde_json::Map::new();
                view.insert("item_id".to_owned(), serde_json::json!(item.item_id));
                view.insert(
                    "source_kind".to_owned(),
                    serde_json::json!(&item.source_kind),
                );
                view.insert(
                    "strength".to_owned(),
                    serde_json::json!(match item.strength {
                        WorkStrength::Hard => "hard",
                        WorkStrength::Ambient => "ambient",
                    }),
                );
                if let Some(task_id) = item.task_id {
                    view.insert("task_id".to_owned(), serde_json::json!(task_id));
                }
                view.insert("channel_id".to_owned(), serde_json::json!(item.channel_id));
                view.insert("thread_id".to_owned(), serde_json::json!(item.thread_id));
                let content_in_window = item
                    .message_id
                    .is_some_and(|message_id| retained_ids.contains(&message_id));
                if let Some(content) = &item.content
                    && !content_in_window
                {
                    view.insert("content".to_owned(), serde_json::json!(content));
                }
                if !item.activity_events.is_empty() {
                    view.insert(
                        "activity_events".to_owned(),
                        serde_json::json!(item.activity_events),
                    );
                }
                serde_json::Value::Object(view)
            })
            .collect::<Vec<_>>();

        let mut work = serde_json::Map::new();
        if let Some(task) = &self.work.task {
            work.insert("task".to_owned(), serde_json::json!(task));
        }
        if !self.work.linked_thread_ids.is_empty() {
            work.insert(
                "linked_thread_ids".to_owned(),
                serde_json::json!(self.work.linked_thread_ids),
            );
        }
        if let Some(message_id) = self.work.public_result_message_id {
            work.insert(
                "public_result_message_id".to_owned(),
                serde_json::json!(message_id),
            );
        }

        let mut context = serde_json::Map::new();
        context.insert(
            "focus_thread_id".to_owned(),
            serde_json::json!(self.context.focus_thread_id),
        );
        context.insert(
            "focus_messages".to_owned(),
            serde_json::Value::Array(focus_messages),
        );
        context.insert(
            "channel_id".to_owned(),
            serde_json::json!(self.context.channel_id),
        );
        context.insert(
            "channel_snapshot_sequence".to_owned(),
            serde_json::json!(self.context.channel_snapshot_sequence),
        );
        if !self.context.channel_activity.is_empty() {
            context.insert(
                "channel_activity".to_owned(),
                serde_json::json!(self.context.channel_activity),
            );
        }
        if omitted_replies > 0 {
            context.insert(
                "omitted_earlier_message_count".to_owned(),
                serde_json::json!(omitted_replies),
            );
        }
        context.insert(
            "dispatched_items".to_owned(),
            serde_json::Value::Array(dispatched_items),
        );

        serde_json::json!({
            "agent": {
                "identity": self.agent.identity.clone(),
                "role": self.agent.role.clone(),
                "memory": self.agent.memory.iter()
                    .filter_map(|entry| serde_json::to_value(entry).ok())
                    .collect::<Vec<_>>(),
            },
            "work": work,
            "run_context": context,
            "channel_members": self.channel_members,
            "reference": {
                "agent_id": self.agent.agent_id,
                "space_id": self.agent.space_id,
            },
        })
    }
}

impl fmt::Debug for RunInput {
    fn fmt(&self, formatter: &mut fmt::Formatter<'_>) -> fmt::Result {
        formatter
            .debug_struct("RunInput")
            .field("agent_id", &self.agent.agent_id)
            .field("task_id", &self.work.task.as_ref().map(|task| task.task_id))
            .field("focus_thread_id", &self.context.focus_thread_id)
            .field("message_count", &self.context.focus_messages.len())
            .field(
                "dispatched_item_count",
                &self.context.dispatched_items.len(),
            )
            .finish()
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::ids::{MemberId, SpaceId, TaskId, ThreadId};
    use time::OffsetDateTime;
    use uuid::Uuid;

    fn message(sequence: u64, body: &str) -> ContextMessageInput {
        ContextMessageInput {
            message_id: MessageId::from_uuid(Uuid::from_u128(100 + u128::from(sequence))),
            author_member_id: MemberId::from_uuid(Uuid::from_u128(1)),
            body: body.to_owned(),
        }
    }

    fn item(item_sequence: u64, message_id: Option<MessageId>) -> DispatchedItemInput {
        DispatchedItemInput {
            item_id: InboxItemId::from_uuid(Uuid::from_u128(200 + u128::from(item_sequence))),
            source_kind: "mention".to_owned(),
            strength: WorkStrength::Hard,
            task_id: None,
            channel_id: crate::ids::ChannelId::from_uuid(Uuid::nil()),
            thread_id: ThreadId::from_uuid(Uuid::from_u128(3)),
            message_id,
            content: Some("item body".to_owned()),
            activity_events: Vec::new(),
        }
    }

    fn run_input(
        focus_messages: Vec<ContextMessageInput>,
        dispatched_items: Vec<DispatchedItemInput>,
    ) -> RunInput {
        RunInput {
            product_contract: "contract".to_owned(),
            agent: AgentInput {
                agent_id: AgentId::from_uuid(Uuid::from_u128(1)),
                space_id: SpaceId::from_uuid(Uuid::from_u128(2)),
                identity: "agent".to_owned(),
                role_revision: 3,
                role: "role".to_owned(),
                memory: Vec::new(),
            },
            work: WorkInput {
                task: None,
                linked_thread_ids: Vec::new(),
                public_result_message_id: None,
            },
            context: RunContextInput {
                focus_thread_id: ThreadId::from_uuid(Uuid::from_u128(3)),
                message_snapshot_sequence: 7,
                focus_messages,
                channel_id: crate::ids::ChannelId::from_uuid(Uuid::from_u128(4)),
                channel_snapshot_sequence: 7,
                channel_activity: Vec::new(),
                dispatched_items,
            },
            channel_members: Vec::new(),
        }
    }

    #[test]
    fn model_view_drops_internal_and_empty_fields() {
        let view = run_input(vec![message(1, "root")], Vec::new()).model_view();

        assert!(view.get("global_contract").is_none());
        assert_eq!(view["agent"]["identity"], "agent");
        assert_eq!(view["agent"]["role"], "role");
        assert_eq!(
            view["run_context"]["focus_thread_id"],
            serde_json::to_value(ThreadId::from_uuid(Uuid::from_u128(3))).unwrap()
        );
        assert_eq!(
            view["reference"]["agent_id"],
            serde_json::to_value(AgentId::from_uuid(Uuid::from_u128(1))).unwrap()
        );
        assert_eq!(
            view["reference"]["space_id"],
            serde_json::to_value(SpaceId::from_uuid(Uuid::from_u128(2))).unwrap()
        );

        assert!(view["agent"].get("role_revision").is_none());
        assert!(
            view["run_context"]
                .get("message_snapshot_sequence")
                .is_none()
        );
        assert!(view["work"].get("task").is_none());
        assert!(view["work"].get("linked_thread_ids").is_none());
        assert!(view["work"].get("public_result_message_id").is_none());
        assert!(
            view["run_context"]
                .get("omitted_earlier_message_count")
                .is_none()
        );
        assert_eq!(
            view["run_context"]["dispatched_items"],
            serde_json::json!([])
        );
        assert_eq!(view["channel_members"], serde_json::json!([]));
    }

    #[test]
    fn model_view_includes_channel_member_display_names() {
        let mut input = run_input(vec![message(1, "root")], Vec::new());
        input.channel_members = vec![ChannelMemberInput {
            member_id: MemberId::from_uuid(Uuid::from_u128(9)),
            display_name: "Lin".to_owned(),
        }];

        let view = input.model_view();

        assert_eq!(view["channel_members"][0]["display_name"], "Lin");
        assert_eq!(
            view["channel_members"][0]["member_id"],
            serde_json::to_value(MemberId::from_uuid(Uuid::from_u128(9))).unwrap()
        );
    }

    #[test]
    fn model_view_caps_focus_message_window() {
        let mut messages = vec![message(1, "root")];
        for sequence in 2..=8 {
            messages.push(message(sequence, &format!("reply {sequence}")));
        }
        let view = run_input(messages, Vec::new()).model_view();

        let focus = view["run_context"]["focus_messages"].as_array().unwrap();
        assert_eq!(focus.len(), 6);
        assert_eq!(focus[0]["body"], "root");
        assert_eq!(focus[1]["body"], "reply 4");
        assert_eq!(focus[5]["body"], "reply 8");
        assert_eq!(view["run_context"]["omitted_earlier_message_count"], 2);
    }

    #[test]
    fn model_view_deduplicates_item_body_inside_window() {
        let mut messages = vec![message(1, "root")];
        for sequence in 2..=8 {
            messages.push(message(sequence, &format!("reply {sequence}")));
        }
        let root_id = messages[0].message_id;
        let outside_id = messages[1].message_id;
        let inside_id = messages[7].message_id;
        let items = vec![
            item(1, Some(root_id)),
            item(2, Some(outside_id)),
            item(3, Some(inside_id)),
            item(4, None),
        ];
        let view = run_input(messages, items.clone()).model_view();

        let claimed = view["run_context"]["dispatched_items"].as_array().unwrap();
        assert!(claimed[0].get("content").is_none());
        assert!(claimed[1].get("content").is_some());
        assert!(claimed[2].get("content").is_none());
        assert!(claimed[3].get("content").is_some());
        assert_eq!(
            claimed[0]["item_id"],
            serde_json::to_value(items[0].item_id).unwrap()
        );
        assert_eq!(claimed[0]["source_kind"], "mention");
        assert_eq!(claimed[0]["strength"], "hard");
    }

    #[test]
    fn model_view_includes_memory_projection() {
        let updated_at = OffsetDateTime::from_unix_timestamp(1_700_000_000).unwrap();
        let mut input = run_input(vec![message(1, "root")], Vec::new());
        input.agent.memory = vec![MemoryEntryInput {
            path: "projects/sumi.md".to_owned(),
            size: 42,
            sha256: "abc".to_owned(),
            updated_at,
        }];

        let view = input.model_view();
        let memory = view["agent"]["memory"].as_array().unwrap();
        assert_eq!(memory.len(), 1);
        assert_eq!(memory[0]["path"], "projects/sumi.md");
        assert_eq!(memory[0]["size"], 42);
        assert_eq!(memory[0]["sha256"], "abc");
        assert_eq!(memory[0]["updated_at"], "2023-11-14T22:13:20Z");
    }

    #[test]
    fn model_view_keeps_task_and_result_when_present() {
        let mut input = run_input(vec![message(1, "root")], Vec::new());
        input.work.task = Some(TaskInput {
            task_id: TaskId::from_uuid(Uuid::from_u128(9)),
            seq: 0,
            title: "title".to_owned(),
            status: "in_progress".to_owned(),
        });
        input.work.linked_thread_ids = vec![ThreadId::from_uuid(Uuid::from_u128(3))];
        input.work.public_result_message_id = Some(MessageId::from_uuid(Uuid::from_u128(10)));

        let work = input.model_view()["work"].clone();
        assert_eq!(work["task"]["title"], "title");
        assert_eq!(work["task"]["status"], "in_progress");
        assert_eq!(
            work["public_result_message_id"],
            serde_json::to_value(MessageId::from_uuid(Uuid::from_u128(10))).unwrap()
        );
        assert_eq!(
            work["linked_thread_ids"],
            serde_json::to_value([ThreadId::from_uuid(Uuid::from_u128(3))]).unwrap()
        );
    }
}
