use time::OffsetDateTime;
use uuid::Uuid;

use super::{MessageAuthor, MessageResponse};

#[test]
fn message_timestamps_use_rfc3339_wire_format() {
    let response = MessageResponse {
        id: Uuid::nil(),
        channel_id: Uuid::nil(),
        seq: 1,
        author: MessageAuthor {
            id: Uuid::nil(),
            kind: "human".to_owned(),
            display_name: "Ada".to_owned(),
            handle: "ada".to_owned(),
        },
        body_markdown: "hello".to_owned(),
        mentions: Vec::new(),
        attachments: Vec::new(),
        created_at: OffsetDateTime::UNIX_EPOCH,
        edited_at: None,
        deleted_at: None,
        thread_id: None,
        reply_count: 0,
    };

    let json = serde_json::to_value(response).expect("Message response serializes");
    let created_at = json["created_at"].as_str().expect("created_at is a string");
    let parsed = OffsetDateTime::parse(created_at, &time::format_description::well_known::Rfc3339)
        .expect("created_at is RFC3339");

    assert_eq!(parsed, OffsetDateTime::UNIX_EPOCH);
    assert!(json["edited_at"].is_null());
}
