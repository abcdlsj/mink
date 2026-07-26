use super::*;

pub(super) async fn run(database_url: &str) -> Result<()> {
    let pool = database::connect_postgres(database_url).await?;
    let web_dist = tempdir()?;
    std::fs::write(
        web_dist.path().join("index.html"),
        "<!doctype html><title>Sumi</title>",
    )?;
    let app = router(
        pool.clone(),
        ServerConfig {
            database_url: database_url.to_owned(),
            web_dist: web_dist.path().to_owned(),
            attachment_dir: web_dist.path().join("attachments"),
            auth_ip_attempts_per_minute: 100,
            auth_email_attempts_per_minute: 100,
            ..ServerConfig::default()
        },
    )?;
    let deep_link = app
        .clone()
        .oneshot(
            Request::builder()
                .uri("/s/sumi-lab/members")
                .body(Body::empty())?,
        )
        .await?;
    ensure!(deep_link.status() == StatusCode::OK);
    let registration_key = Uuid::now_v7();
    let registration_body = serde_json::json!({
        "display_name": "Ada Lovelace",
        "email": "ADA@example.test",
        "password": "correct-horse-battery"
    });

    let registration = app
        .clone()
        .oneshot(json_request(
            "/api/v1/auth/register",
            registration_key,
            &registration_body,
            None,
        )?)
        .await?;
    ensure!(registration.status() == StatusCode::CREATED);
    let mut cookie = response_cookie(&registration)?;
    let registration: RegisterResponse = decode_json(registration).await?;

    let retry = app
        .clone()
        .oneshot(json_request(
            "/api/v1/auth/register",
            registration_key,
            &registration_body,
            None,
        )?)
        .await?;
    ensure!(retry.status() == StatusCode::CREATED);
    let retry: RegisterResponse = decode_json(retry).await?;
    ensure!(retry.user.id == registration.user.id);

    let me = app
        .clone()
        .oneshot(
            Request::builder()
                .uri("/api/v1/auth/me")
                .header(header::COOKIE, &cookie)
                .body(Body::empty())?,
        )
        .await?;
    ensure!(me.status() == StatusCode::OK);

    let logout = app
        .clone()
        .oneshot(json_request(
            "/api/v1/auth/logout",
            Uuid::now_v7(),
            &serde_json::json!({}),
            Some(&cookie),
        )?)
        .await?;
    ensure!(logout.status() == StatusCode::NO_CONTENT);
    let logged_out = app
        .clone()
        .oneshot(
            Request::builder()
                .uri("/api/v1/auth/me")
                .header(header::COOKIE, &cookie)
                .body(Body::empty())?,
        )
        .await?;
    ensure!(logged_out.status() == StatusCode::UNAUTHORIZED);

    let login = app
        .clone()
        .oneshot(json_request(
            "/api/v1/auth/login",
            Uuid::now_v7(),
            &serde_json::json!({
                "email": "ada@example.test",
                "password": "correct-horse-battery"
            }),
            None,
        )?)
        .await?;
    ensure!(login.status() == StatusCode::OK);
    cookie = response_cookie(&login)?;
    let login: LoginResponse = decode_json(login).await?;
    ensure!(login.user.id == registration.user.id);

    let space_key = Uuid::now_v7();
    let space_body = serde_json::json!({
        "name": "Sumi Lab",
        "slug": "sumi-lab",
        "accent": "#5065D8"
    });
    let created = app
        .clone()
        .oneshot(json_request(
            "/api/v1/spaces",
            space_key,
            &space_body,
            Some(&cookie),
        )?)
        .await?;
    ensure!(created.status() == StatusCode::CREATED);
    let created: SpaceResponse = decode_json(created).await?;

    let retry = app
        .clone()
        .oneshot(json_request(
            "/api/v1/spaces",
            space_key,
            &space_body,
            Some(&cookie),
        )?)
        .await?;
    ensure!(retry.status() == StatusCode::CREATED);
    let retry: SpaceResponse = decode_json(retry).await?;
    ensure!(retry.id == created.id);

    let fetched = app
        .clone()
        .oneshot(
            Request::builder()
                .uri("/api/v1/spaces/by-slug/sumi-lab")
                .header(header::COOKIE, &cookie)
                .body(Body::empty())?,
        )
        .await?;
    ensure!(fetched.status() == StatusCode::OK);
    let fetched: SpaceResponse = decode_json(fetched).await?;
    ensure!(fetched.id == created.id);

    let invariants: (i64, i64, i64, i64) = sqlx::query_as(
        "SELECT \
           (SELECT count(*) FROM members WHERE space_id = $1 AND kind = 'human' \
              AND access_level = 'owner'), \
           (SELECT count(*) FROM channels WHERE space_id = $1 AND slug = 'general' \
              AND kind = 'public'), \
           (SELECT count(*) FROM audit_events WHERE space_id = $1 \
              AND action = 'space.created'), \
           (SELECT count(*) FROM outbox_events WHERE aggregate_id = $2 \
              AND topic = 'channel.created')",
    )
    .bind(created.id)
    .bind(created.general_channel_id)
    .fetch_one(&pool)
    .await?;
    ensure!(invariants == (1, 1, 1, 1));
    ensure!(
        sqlx::query_scalar::<_, i64>("SELECT count(*) FROM users")
            .fetch_one(&pool)
            .await?
            == 1
    );

    let grace = register_human(
        &app,
        "Grace Hopper",
        "grace@example.test",
        "compiler-correct-horse",
    )
    .await?;
    let grace_token = URL_SAFE_NO_PAD.encode([7_u8; 32]);
    let grace_invitation = app
        .clone()
        .oneshot(json_request(
            &format!("/api/v1/spaces/{}/invites", created.id),
            Uuid::now_v7(),
            &serde_json::json!({
                "email": "GRACE@example.test",
                "invite_token": grace_token
            }),
            Some(&cookie),
        )?)
        .await?;
    ensure!(grace_invitation.status() == StatusCode::CREATED);
    let grace_invitation: InvitationResponse = decode_json(grace_invitation).await?;
    ensure!(grace_invitation.email == "grace@example.test");

    let preview = app
        .clone()
        .oneshot(
            Request::builder()
                .uri(format!("/api/v1/invites/{grace_token}"))
                .body(Body::empty())?,
        )
        .await?;
    ensure!(preview.status() == StatusCode::OK);

    let accept_key = Uuid::now_v7();
    let accepted = app
        .clone()
        .oneshot(json_request(
            &format!("/api/v1/invites/{grace_token}/accept"),
            accept_key,
            &serde_json::json!({}),
            Some(&grace.cookie),
        )?)
        .await?;
    ensure!(accepted.status() == StatusCode::OK);
    let grace_member: MemberResponse = decode_json(accepted).await?;
    ensure!(grace_member.access_level == "member");

    let accept_retry = app
        .clone()
        .oneshot(json_request(
            &format!("/api/v1/invites/{grace_token}/accept"),
            accept_key,
            &serde_json::json!({}),
            Some(&grace.cookie),
        )?)
        .await?;
    ensure!(accept_retry.status() == StatusCode::OK);
    let accept_retry: MemberResponse = decode_json(accept_retry).await?;
    ensure!(accept_retry.id == grace_member.id);

    let promote = app
        .clone()
        .oneshot(json_request_with_method(
            "PATCH",
            &format!("/api/v1/spaces/{}/members/{}", created.id, grace_member.id),
            Uuid::now_v7(),
            &serde_json::json!({ "access_level": "admin" }),
            Some(&cookie),
        )?)
        .await?;
    ensure!(promote.status() == StatusCode::OK);

    let alan = register_human(
        &app,
        "Alan Turing",
        "alan@example.test",
        "enigma-correct-horse",
    )
    .await?;
    let alan_token = URL_SAFE_NO_PAD.encode([9_u8; 32]);
    let admin_invite = app
        .clone()
        .oneshot(json_request(
            &format!("/api/v1/spaces/{}/invites", created.id),
            Uuid::now_v7(),
            &serde_json::json!({
                "email": "alan@example.test",
                "invite_token": alan_token
            }),
            Some(&grace.cookie),
        )?)
        .await?;
    ensure!(admin_invite.status() == StatusCode::CREATED);

    let accepted = app
        .clone()
        .oneshot(json_request(
            &format!("/api/v1/invites/{alan_token}/accept"),
            Uuid::now_v7(),
            &serde_json::json!({}),
            Some(&alan.cookie),
        )?)
        .await?;
    ensure!(accepted.status() == StatusCode::OK);
    let alan_member: MemberResponse = decode_json(accepted).await?;

    let denied_channel = app
        .clone()
        .oneshot(json_request(
            &format!("/api/v1/spaces/{}/channels", created.id),
            Uuid::now_v7(),
            &serde_json::json!({
                "name": "Design",
                "slug": "design",
                "kind": "public",
                "topic": "Product and system design",
                "agent_member_ids": []
            }),
            Some(&alan.cookie),
        )?)
        .await?;
    ensure!(denied_channel.status() == StatusCode::FORBIDDEN);

    let grant = app
        .clone()
        .oneshot(json_request_with_method(
            "PATCH",
            &format!("/api/v1/spaces/{}/members/{}", created.id, alan_member.id),
            Uuid::now_v7(),
            &serde_json::json!({
                "permissions": ["channel:create", "agent:create"]
            }),
            Some(&grace.cookie),
        )?)
        .await?;
    ensure!(grant.status() == StatusCode::OK);
    let granted: MemberResponse = decode_json(grant).await?;
    ensure!(granted.permissions == ["agent:create", "channel:create"]);

    let design = app
        .clone()
        .oneshot(json_request(
            &format!("/api/v1/spaces/{}/channels", created.id),
            Uuid::now_v7(),
            &serde_json::json!({
                "name": "Design",
                "slug": "design",
                "kind": "public",
                "topic": "Product and system design",
                "agent_member_ids": []
            }),
            Some(&alan.cookie),
        )?)
        .await?;
    ensure!(design.status() == StatusCode::CREATED);
    let design: ChannelResponse = decode_json(design).await?;
    ensure!(design.joined);

    let private = app
        .clone()
        .oneshot(json_request(
            &format!("/api/v1/spaces/{}/channels", created.id),
            Uuid::now_v7(),
            &serde_json::json!({
                "name": "Roadmap",
                "slug": "roadmap",
                "kind": "private",
                "topic": null,
                "agent_member_ids": []
            }),
            Some(&alan.cookie),
        )?)
        .await?;
    ensure!(private.status() == StatusCode::CREATED);
    let private: ChannelResponse = decode_json(private).await?;

    let owner_channels = app
        .clone()
        .oneshot(
            Request::builder()
                .uri(format!("/api/v1/spaces/{}/channels", created.id))
                .header(header::COOKIE, &cookie)
                .body(Body::empty())?,
        )
        .await?;
    ensure!(owner_channels.status() == StatusCode::OK);
    let owner_channels: ChannelListResponse = decode_json(owner_channels).await?;
    ensure!(owner_channels.can_create);
    ensure!(
        owner_channels
            .channels
            .iter()
            .any(|channel| channel.id == design.id && !channel.joined)
    );
    ensure!(
        owner_channels
            .channels
            .iter()
            .all(|channel| channel.id != private.id)
    );

    let join_key = Uuid::now_v7();
    for _ in 0..2 {
        let joined = app
            .clone()
            .oneshot(json_request(
                &format!("/api/v1/channels/{}/members/me", design.id),
                join_key,
                &serde_json::json!({}),
                Some(&cookie),
            )?)
            .await?;
        ensure!(joined.status() == StatusCode::OK);
        let joined: ChannelResponse = decode_json(joined).await?;
        ensure!(joined.joined);
    }

    let first_message_key = Uuid::now_v7();
    let first_message = app
        .clone()
        .oneshot(json_request(
            &format!("/api/v1/channels/{}/messages", design.id),
            first_message_key,
            &serde_json::json!({
                "body_markdown": "Welcome to design.",
                "mentions": [created.owner_member_id]
            }),
            Some(&alan.cookie),
        )?)
        .await?;
    ensure!(first_message.status() == StatusCode::CREATED);
    let first_message: MessageResponse = decode_json(first_message).await?;
    ensure!(first_message.seq == 1);

    let message_retry = app
        .clone()
        .oneshot(json_request(
            &format!("/api/v1/channels/{}/messages", design.id),
            first_message_key,
            &serde_json::json!({
                "body_markdown": "Welcome to design.",
                "mentions": [created.owner_member_id]
            }),
            Some(&alan.cookie),
        )?)
        .await?;
    ensure!(message_retry.status() == StatusCode::CREATED);
    let message_retry: MessageResponse = decode_json(message_retry).await?;
    ensure!(message_retry.id == first_message.id);

    let second_message = app
        .clone()
        .oneshot(json_request(
            &format!("/api/v1/channels/{}/messages", design.id),
            Uuid::now_v7(),
            &serde_json::json!({
                "body_markdown": "Second note.",
                "mentions": []
            }),
            Some(&cookie),
        )?)
        .await?;
    ensure!(second_message.status() == StatusCode::CREATED);
    let second_message: MessageResponse = decode_json(second_message).await?;
    ensure!(second_message.seq == 2);

    let edited_message = app
        .clone()
        .oneshot(json_request_with_method(
            "PATCH",
            &format!("/api/v1/messages/{}", second_message.id),
            Uuid::now_v7(),
            &serde_json::json!({ "body_markdown": "Second note, clarified." }),
            Some(&cookie),
        )?)
        .await?;
    ensure!(edited_message.status() == StatusCode::OK);
    let edited_message: MessageResponse = decode_json(edited_message).await?;
    ensure!(edited_message.edited_at.is_some());

    let non_author_edit = app
        .clone()
        .oneshot(json_request_with_method(
            "PATCH",
            &format!("/api/v1/messages/{}", edited_message.id),
            Uuid::now_v7(),
            &serde_json::json!({ "body_markdown": "Not my Message." }),
            Some(&alan.cookie),
        )?)
        .await?;
    ensure!(non_author_edit.status() == StatusCode::FORBIDDEN);

    let deleted_message = app
        .clone()
        .oneshot(json_request_with_method(
            "DELETE",
            &format!("/api/v1/messages/{}", edited_message.id),
            Uuid::now_v7(),
            &serde_json::json!({}),
            Some(&cookie),
        )?)
        .await?;
    ensure!(deleted_message.status() == StatusCode::OK);
    let deleted_message: MessageResponse = decode_json(deleted_message).await?;
    ensure!(deleted_message.body_markdown == "Message 已删除");

    let attachment_bytes = b"Attachment content stays exact.";
    let attachment_sha = hex::encode(sha2::Sha256::digest(attachment_bytes));
    let upload = app
        .clone()
        .oneshot(json_request(
            "/api/v1/attachments/uploads",
            Uuid::now_v7(),
            &serde_json::json!({
                "space_id": created.id,
                "original_name": "design-note.txt",
                "media_type": "text/plain"
            }),
            Some(&cookie),
        )?)
        .await?;
    ensure!(upload.status() == StatusCode::CREATED);
    let uploading: AttachmentResponse = decode_json(upload).await?;
    ensure!(uploading.status == "uploading" && uploading.size.is_none());

    let premature_message = app
        .clone()
        .oneshot(json_request(
            &format!("/api/v1/channels/{}/messages", design.id),
            Uuid::now_v7(),
            &serde_json::json!({
                "body_markdown": "This must roll back.",
                "mentions": [],
                "attachment_ids": [uploading.id]
            }),
            Some(&cookie),
        )?)
        .await?;
    ensure!(premature_message.status() == StatusCode::BAD_REQUEST);

    let content = app
        .clone()
        .oneshot(
            Request::builder()
                .method("PUT")
                .uri(format!("/api/v1/attachments/{}/content", uploading.id))
                .header(header::COOKIE, &cookie)
                .body(Body::from(attachment_bytes.as_slice()))?,
        )
        .await?;
    ensure!(content.status() == StatusCode::NO_CONTENT);

    let mismatch = app
        .clone()
        .oneshot(json_request(
            &format!("/api/v1/attachments/{}/complete", uploading.id),
            Uuid::now_v7(),
            &serde_json::json!({ "size": attachment_bytes.len(), "sha256": "00".repeat(32) }),
            Some(&cookie),
        )?)
        .await?;
    ensure!(mismatch.status() == StatusCode::BAD_REQUEST);

    let complete_key = Uuid::now_v7();
    let complete = app
        .clone()
        .oneshot(json_request(
            &format!("/api/v1/attachments/{}/complete", uploading.id),
            complete_key,
            &serde_json::json!({ "size": attachment_bytes.len(), "sha256": attachment_sha }),
            Some(&cookie),
        )?)
        .await?;
    ensure!(complete.status() == StatusCode::OK);
    let ready: AttachmentResponse = decode_json(complete).await?;
    ensure!(ready.status == "ready" && ready.size == Some(attachment_bytes.len() as i64));
    let complete_retry = app
        .clone()
        .oneshot(json_request(
            &format!("/api/v1/attachments/{}/complete", uploading.id),
            Uuid::now_v7(),
            &serde_json::json!({ "size": attachment_bytes.len(), "sha256": attachment_sha }),
            Some(&cookie),
        )?)
        .await?;
    ensure!(complete_retry.status() == StatusCode::OK);

    let attachment_message = app
        .clone()
        .oneshot(json_request(
            &format!("/api/v1/channels/{}/messages", design.id),
            Uuid::now_v7(),
            &serde_json::json!({
                "body_markdown": "Design note attached.",
                "mentions": [],
                "attachment_ids": [ready.id]
            }),
            Some(&cookie),
        )?)
        .await?;
    ensure!(attachment_message.status() == StatusCode::CREATED);
    let attachment_message: MessageResponse = decode_json(attachment_message).await?;
    ensure!(attachment_message.attachments.len() == 1);

    let download = app
        .clone()
        .oneshot(
            Request::builder()
                .uri(format!("/api/v1/attachments/{}/download", ready.id))
                .header(header::COOKIE, &cookie)
                .body(Body::empty())?,
        )
        .await?;
    ensure!(download.status() == StatusCode::OK);
    ensure!(to_bytes(download.into_body(), 1024).await?.as_ref() == attachment_bytes);

    let private_attachment = app
        .clone()
        .oneshot(json_request(
            "/api/v1/attachments/uploads",
            Uuid::now_v7(),
            &serde_json::json!({
                "space_id": created.id,
                "original_name": "private.txt",
                "media_type": "text/plain"
            }),
            Some(&alan.cookie),
        )?)
        .await?;
    ensure!(private_attachment.status() == StatusCode::CREATED);
    let private_attachment: AttachmentResponse = decode_json(private_attachment).await?;
    let foreign_upload = app
        .clone()
        .oneshot(
            Request::builder()
                .method("PUT")
                .uri(format!(
                    "/api/v1/attachments/{}/content",
                    private_attachment.id
                ))
                .header(header::COOKIE, &cookie)
                .body(Body::from("not yours"))?,
        )
        .await?;
    ensure!(foreign_upload.status() == StatusCode::FORBIDDEN);

    let messages = app
        .clone()
        .oneshot(
            Request::builder()
                .uri(format!("/api/v1/channels/{}/messages?limit=1", design.id))
                .header(header::COOKIE, &cookie)
                .body(Body::empty())?,
        )
        .await?;
    ensure!(messages.status() == StatusCode::OK);
    let messages: MessagePageResponse = decode_json(messages).await?;
    ensure!(messages.snapshot_channel_seq == 3);
    ensure!(
        messages.messages.len() == 1
            && messages.messages[0].id == attachment_message.id
            && messages.messages[0].attachments.len() == 1
    );
    ensure!(messages.has_more_before);

    let private_read_denied = app
        .clone()
        .oneshot(
            Request::builder()
                .uri(format!("/api/v1/channels/{}/messages", private.id))
                .header(header::COOKIE, &cookie)
                .body(Body::empty())?,
        )
        .await?;
    ensure!(private_read_denied.status() == StatusCode::FORBIDDEN);

    let thread_key = Uuid::now_v7();
    let thread = app
        .clone()
        .oneshot(json_request(
            &format!("/api/v1/channels/{}/threads", design.id),
            thread_key,
            &serde_json::json!({ "root_message_id": first_message.id }),
            Some(&alan.cookie),
        )?)
        .await?;
    ensure!(thread.status() == StatusCode::CREATED);
    let thread: ThreadResponse = decode_json(thread).await?;
    ensure!(thread.thread_id == 1 && thread.root_message_id == first_message.id);

    let thread_retry = app
        .clone()
        .oneshot(json_request(
            &format!("/api/v1/channels/{}/threads", design.id),
            thread_key,
            &serde_json::json!({ "root_message_id": first_message.id }),
            Some(&alan.cookie),
        )?)
        .await?;
    ensure!(thread_retry.status() == StatusCode::CREATED);
    let thread_retry: ThreadResponse = decode_json(thread_retry).await?;
    ensure!(thread_retry.thread_id == thread.thread_id);

    let reply = app
        .clone()
        .oneshot(json_request(
            &format!(
                "/api/v1/channels/{}/threads/{}/messages",
                design.id, thread.thread_id
            ),
            Uuid::now_v7(),
            &serde_json::json!({
                "body_markdown": "Thread reply.",
                "mentions": [alan_member.id],
                "reply_to_message_id": first_message.id
            }),
            Some(&cookie),
        )?)
        .await?;
    ensure!(reply.status() == StatusCode::CREATED);
    let reply: MessageResponse = decode_json(reply).await?;
    ensure!(reply.seq == 4);

    let thread_read = app
        .clone()
        .oneshot(
            Request::builder()
                .uri(format!(
                    "/api/v1/channels/{}/threads/{}",
                    design.id, thread.thread_id
                ))
                .header(header::COOKIE, &cookie)
                .body(Body::empty())?,
        )
        .await?;
    ensure!(thread_read.status() == StatusCode::OK);
    let thread_read: ThreadReadResponse = decode_json(thread_read).await?;
    ensure!(thread_read.snapshot_channel_seq == 4);
    ensure!(thread_read.root.id == first_message.id);
    ensure!(thread_read.replies.len() == 1 && thread_read.replies[0].id == reply.id);

    let invalid_nested_root = app
        .clone()
        .oneshot(json_request(
            &format!("/api/v1/channels/{}/threads", design.id),
            Uuid::now_v7(),
            &serde_json::json!({ "root_message_id": reply.id }),
            Some(&cookie),
        )?)
        .await?;
    ensure!(invalid_nested_root.status() == StatusCode::BAD_REQUEST);

    let dm = app
        .clone()
        .oneshot(json_request(
            &format!("/api/v1/spaces/{}/dms", created.id),
            Uuid::now_v7(),
            &serde_json::json!({ "member_id": grace_member.id }),
            Some(&alan.cookie),
        )?)
        .await?;
    ensure!(dm.status() == StatusCode::CREATED);
    let dm: DirectMessageResponse = decode_json(dm).await?;
    ensure!(dm.other_member.id == grace_member.id);

    let reverse_dm = app
        .clone()
        .oneshot(json_request(
            &format!("/api/v1/spaces/{}/dms", created.id),
            Uuid::now_v7(),
            &serde_json::json!({ "member_id": alan_member.id }),
            Some(&grace.cookie),
        )?)
        .await?;
    ensure!(reverse_dm.status() == StatusCode::OK);
    let reverse_dm: DirectMessageResponse = decode_json(reverse_dm).await?;
    ensure!(reverse_dm.channel_id == dm.channel_id);

    let owner_dms = app
        .clone()
        .oneshot(
            Request::builder()
                .uri(format!("/api/v1/spaces/{}/dms", created.id))
                .header(header::COOKIE, &cookie)
                .body(Body::empty())?,
        )
        .await?;
    ensure!(owner_dms.status() == StatusCode::OK);
    let owner_dms: Vec<DirectMessageResponse> = decode_json(owner_dms).await?;
    ensure!(owner_dms.is_empty());

    let dm_message = app
        .clone()
        .oneshot(json_request(
            &format!("/api/v1/channels/{}/messages", dm.channel_id),
            Uuid::now_v7(),
            &serde_json::json!({
                "body_markdown": "@grace-hopper private note.",
                "mentions": [grace_member.id]
            }),
            Some(&alan.cookie),
        )?)
        .await?;
    ensure!(dm_message.status() == StatusCode::CREATED);
    let dm_message: MessageResponse = decode_json(dm_message).await?;

    let grace_inbox = app
        .clone()
        .oneshot(
            Request::builder()
                .uri(format!("/api/v1/members/{}/inbox", grace_member.id))
                .header(header::COOKIE, &grace.cookie)
                .body(Body::empty())?,
        )
        .await?;
    ensure!(grace_inbox.status() == StatusCode::OK);
    let grace_inbox: Vec<InboxItemResponse> = decode_json(grace_inbox).await?;
    let direct_item = grace_inbox
        .iter()
        .find(|item| item.message_id == Some(dm_message.id))
        .context("direct Inbox Item missing")?;
    ensure!(direct_item.kind == "direct" && direct_item.sender_member_id == Some(alan_member.id));

    let owner_cannot_read_grace_inbox = app
        .clone()
        .oneshot(
            Request::builder()
                .uri(format!("/api/v1/members/{}/inbox", grace_member.id))
                .header(header::COOKIE, &cookie)
                .body(Body::empty())?,
        )
        .await?;
    ensure!(owner_cannot_read_grace_inbox.status() == StatusCode::FORBIDDEN);

    let acked = app
        .clone()
        .oneshot(json_request(
            &format!("/api/v1/inbox/{}/ack", direct_item.id),
            Uuid::now_v7(),
            &serde_json::json!({}),
            Some(&grace.cookie),
        )?)
        .await?;
    ensure!(acked.status() == StatusCode::OK);
    let acked: InboxItemResponse = decode_json(acked).await?;
    ensure!(acked.status == "handled");

    let event_ids: Vec<Uuid> = sqlx::query_scalar(
        "SELECT id FROM outbox_events WHERE payload_json->>'space_id' = $1::text \
         ORDER BY id DESC LIMIT 2",
    )
    .bind(created.id)
    .fetch_all(&pool)
    .await?;
    ensure!(event_ids.len() == 2);
    let events = app
        .clone()
        .oneshot(
            Request::builder()
                .uri(format!("/api/v1/spaces/{}/events", created.id))
                .header(header::COOKIE, &grace.cookie)
                .header("last-event-id", event_ids[1].to_string())
                .body(Body::empty())?,
        )
        .await?;
    ensure!(events.status() == StatusCode::OK);
    let first_event = tokio::time::timeout(
        std::time::Duration::from_secs(2),
        events.into_body().into_data_stream().next(),
    )
    .await?
    .context("SSE replay produced no event")??;
    let first_event = String::from_utf8(first_event.to_vec())?;
    ensure!(first_event.contains(&format!("id: {}", event_ids[0])));

    let dm_thread = app
        .clone()
        .oneshot(json_request(
            &format!("/api/v1/channels/{}/threads", dm.channel_id),
            Uuid::now_v7(),
            &serde_json::json!({ "root_message_id": dm_message.id }),
            Some(&alan.cookie),
        )?)
        .await?;
    ensure!(dm_thread.status() == StatusCode::CREATED);
    let dm_thread: ThreadResponse = decode_json(dm_thread).await?;

    let dm_reply = app
        .clone()
        .oneshot(json_request(
            &format!(
                "/api/v1/channels/{}/threads/{}/messages",
                dm.channel_id, dm_thread.thread_id
            ),
            Uuid::now_v7(),
            &serde_json::json!({
                "body_markdown": "Reply inside the DM Thread.",
                "mentions": [alan_member.id],
                "reply_to_message_id": dm_message.id
            }),
            Some(&grace.cookie),
        )?)
        .await?;
    ensure!(dm_reply.status() == StatusCode::CREATED);
    let dm_reply: MessageResponse = decode_json(dm_reply).await?;
    let dm_reply_inbox_count: i64 = sqlx::query_scalar(
        "SELECT count(*) FROM inbox_items WHERE message_id = $1 \
         AND member_id = $2 AND kind = 'direct' AND priority = 'hard'",
    )
    .bind(dm_reply.id)
    .bind(alan_member.id)
    .fetch_one(&pool)
    .await?;
    ensure!(dm_reply_inbox_count == 1);

    let archive_key = Uuid::now_v7();
    let archived = app
        .clone()
        .oneshot(json_request(
            &format!("/api/v1/channels/{}/archive", design.id),
            archive_key,
            &serde_json::json!({}),
            Some(&alan.cookie),
        )?)
        .await?;
    ensure!(archived.status() == StatusCode::OK);
    let archived: ChannelResponse = decode_json(archived).await?;
    ensure!(archived.archived_at.is_some());

    let archive_retry = app
        .clone()
        .oneshot(json_request(
            &format!("/api/v1/channels/{}/archive", design.id),
            archive_key,
            &serde_json::json!({}),
            Some(&alan.cookie),
        )?)
        .await?;
    ensure!(archive_retry.status() == StatusCode::OK);

    let archived_read = app
        .clone()
        .oneshot(
            Request::builder()
                .uri(format!("/api/v1/channels/{}/messages", design.id))
                .header(header::COOKIE, &cookie)
                .body(Body::empty())?,
        )
        .await?;
    ensure!(archived_read.status() == StatusCode::OK);

    let archived_write = app
        .clone()
        .oneshot(json_request(
            &format!("/api/v1/channels/{}/messages", design.id),
            Uuid::now_v7(),
            &serde_json::json!({ "body_markdown": "too late", "mentions": [] }),
            Some(&cookie),
        )?)
        .await?;
    ensure!(archived_write.status() == StatusCode::CONFLICT);

    let general_archive = app
        .clone()
        .oneshot(json_request(
            &format!("/api/v1/channels/{}/archive", created.general_channel_id),
            Uuid::now_v7(),
            &serde_json::json!({}),
            Some(&cookie),
        )?)
        .await?;
    ensure!(general_archive.status() == StatusCode::BAD_REQUEST);

    let admin_cannot_change_owner = app
        .clone()
        .oneshot(json_request_with_method(
            "PATCH",
            &format!(
                "/api/v1/spaces/{}/members/{}",
                created.id, created.owner_member_id
            ),
            Uuid::now_v7(),
            &serde_json::json!({ "permissions": ["channel:create"] }),
            Some(&grace.cookie),
        )?)
        .await?;
    ensure!(admin_cannot_change_owner.status() == StatusCode::FORBIDDEN);

    let member_cannot_promote = app
        .clone()
        .oneshot(json_request_with_method(
            "PATCH",
            &format!("/api/v1/spaces/{}/members/{}", created.id, alan_member.id),
            Uuid::now_v7(),
            &serde_json::json!({ "access_level": "admin" }),
            Some(&alan.cookie),
        )?)
        .await?;
    ensure!(member_cannot_promote.status() == StatusCode::FORBIDDEN);

    let members = app
        .clone()
        .oneshot(
            Request::builder()
                .uri(format!("/api/v1/spaces/{}/members", created.id))
                .header(header::COOKIE, &alan.cookie)
                .body(Body::empty())?,
        )
        .await?;
    ensure!(members.status() == StatusCode::OK);
    let members: Vec<MemberResponse> = decode_json(members).await?;
    ensure!(members.len() == 3);
    ensure!(
        members
            .iter()
            .filter(|member| member.kind == "human")
            .count()
            == 3
    );

    let invited_invariants: (i64, i64, i64, i64, i64, i64, i64, i64, i64, i64, i64) =
        sqlx::query_as(
            "SELECT \
           (SELECT count(*) FROM human_invitations WHERE space_id = $1 \
              AND accepted_at IS NOT NULL), \
           (SELECT count(*) FROM channel_members WHERE space_id = $1), \
           (SELECT count(*) FROM member_permissions WHERE member_id = $2), \
           (SELECT count(*) FROM outbox_events WHERE topic = 'member.updated' \
              AND payload_json->>'space_id' = $1::text), \
           (SELECT count(*) FROM outbox_events WHERE topic IN \
              ('channel.created', 'channel.joined') \
              AND payload_json->>'space_id' = $1::text), \
           (SELECT count(*) FROM messages WHERE channel_id = $3), \
           (SELECT count(*) FROM inbox_items WHERE message_id = $4 \
              AND kind = 'mention' AND priority = 'hard'), \
           (SELECT count(*) FROM threads WHERE channel_id = $3), \
           (SELECT count(*) FROM thread_subscriptions WHERE channel_id = $3 \
              AND thread_id = 1), \
           (SELECT count(*) FROM channel_members WHERE channel_id = $5), \
           (SELECT count(*) FROM inbox_items WHERE message_id = $6 \
              AND kind = 'direct' AND priority = 'hard')",
        )
        .bind(created.id)
        .bind(alan_member.id)
        .bind(design.id)
        .bind(first_message.id)
        .bind(dm.channel_id)
        .bind(dm_message.id)
        .fetch_one(&pool)
        .await?;
    ensure!(invited_invariants == (2, 8, 2, 4, 5, 4, 1, 1, 2, 2, 1));

    pool.close().await;
    Ok(())
}
