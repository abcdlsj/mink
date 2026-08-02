use super::*;

pub(super) async fn register(
    State(state): State<RuntimeState>,
    jar: CookieJar,
    Json(body): Json<RegisterBody>,
) -> Result<(CookieJar, (StatusCode, Json<RegisterResponse>)), ApiError> {
    let mut storage = state.storage.clone();
    let session = RegisterHuman::execute(
        &mut storage,
        &Argon2Passwords,
        &RandomSessionTokens,
        RegisterHumanInput {
            user_id: Uuid::now_v7(),
            session_id: Uuid::now_v7(),
            display_name: &body.display_name,
            email: &body.email,
            password: &body.password,
            lifetime: state.session_lifetime,
            now: OffsetDateTime::now_utc(),
        },
    )
    .await
    .map_err(application_error)?;
    Ok((
        session_cookie(jar, &session.token),
        (
            StatusCode::CREATED,
            Json(RegisterResponse {
                user: user_response(&session.human),
                next: "create_space".to_owned(),
            }),
        ),
    ))
}

pub(super) async fn login(
    State(state): State<RuntimeState>,
    jar: CookieJar,
    Json(body): Json<LoginBody>,
) -> Result<(CookieJar, Json<LoginResponse>), ApiError> {
    let mut storage = state.storage.clone();
    let session = AuthenticateHuman::execute(
        &mut storage,
        &Argon2Passwords,
        &RandomSessionTokens,
        AuthenticateHumanInput {
            session_id: Uuid::now_v7(),
            email: &body.email,
            password: &body.password,
            lifetime: state.session_lifetime,
            now: OffsetDateTime::now_utc(),
        },
    )
    .await
    .map_err(application_error)?;
    Ok((
        session_cookie(jar, &session.token),
        Json(LoginResponse {
            user: user_response(&session.human),
        }),
    ))
}

pub(super) async fn logout(
    State(state): State<RuntimeState>,
    jar: CookieJar,
) -> (CookieJar, StatusCode) {
    if let Some(token) = session_token(&jar) {
        let mut storage = state.storage.clone();
        let _ = CloseSession::execute(&mut storage, &token).await;
    }
    (
        jar.remove(Cookie::from(SESSION_COOKIE)),
        StatusCode::NO_CONTENT,
    )
}

pub(super) async fn current_user(
    State(state): State<RuntimeState>,
    jar: CookieJar,
) -> Result<Json<UserResponse>, ApiError> {
    let human = authenticate(&state, &jar).await?;
    Ok(Json(user_response(&human)))
}

pub(super) async fn begin_pairing(
    State(state): State<RuntimeState>,
    Json(body): Json<BeginPairingBody>,
) -> Result<(StatusCode, Json<Value>), ApiError> {
    let mut storage = state.storage.clone();
    let started = BeginPairing::execute(
        &mut storage,
        &NumericPairingCodes,
        BeginPairingInput {
            pairing_id: Uuid::now_v7(),
            token_hash: &body.token_hash,
            hostname: &body.hostname,
            os: &body.os,
            daemon_version: &body.daemon_version,
            now: OffsetDateTime::now_utc(),
        },
    )
    .await
    .map_err(application_error)?;
    Ok((
        StatusCode::CREATED,
        Json(json!({
            "pairing_id": started.pairing_id,
            "code": started.code,
            "expires_at": timestamp(started.expires_at)
        })),
    ))
}

pub(super) async fn pairing_details(
    State(state): State<RuntimeState>,
    jar: CookieJar,
    Path(pairing_id): Path<Uuid>,
    Query(query): Query<PairingCodeQuery>,
) -> Result<Json<Value>, ApiError> {
    authenticate(&state, &jar).await?;
    let mut storage = state.storage.clone();
    let pairing = ReadPairing::execute(
        &mut storage,
        pairing_id,
        &RawPairingCode::new(query.code).sha256_hash(),
        OffsetDateTime::now_utc(),
    )
    .await
    .map_err(application_error)?;
    Ok(Json(json!({
        "pairing_id": pairing.pairing_id,
        "hostname": pairing.hostname,
        "os": pairing.os.code(),
        "daemon_version": pairing.daemon_version,
        "token_fingerprint": pairing.token_fingerprint,
        "status": pairing.status.code(),
        "expires_at": timestamp(pairing.expires_at)
    })))
}

pub(super) async fn confirm_pairing(
    State(state): State<RuntimeState>,
    jar: CookieJar,
    headers: HeaderMap,
    Path(pairing_id): Path<Uuid>,
    Json(body): Json<ConfirmPairingBody>,
) -> Result<(StatusCode, Json<ComputerResponse>), ApiError> {
    let actor_id = current_member(&state, &jar, body.space_id).await?;
    let key = idempotency_header(&headers)?;
    let mut storage = state.storage.clone();
    let computer = ConfirmPairing::execute(
        &mut storage,
        ConfirmPairingInput {
            actor_id: MemberId::from_uuid(actor_id),
            pairing_id,
            computer_id: ComputerId::from_uuid(Uuid::now_v7()),
            space_id: SpaceId::from_uuid(body.space_id),
            code_hash: &RawPairingCode::new(body.code).sha256_hash(),
            name: &body.name,
            idempotency_key: IdempotencyKey::from_uuid(key),
            now: OffsetDateTime::now_utc(),
        },
    )
    .await
    .map_err(application_error)?;
    Ok((StatusCode::CREATED, Json(computer_response(&computer))))
}

pub(super) async fn pairing_status(
    State(state): State<RuntimeState>,
    headers: HeaderMap,
    Path(pairing_id): Path<Uuid>,
) -> Result<Json<Value>, ApiError> {
    let raw = bearer_token(&headers)?;
    let mut storage = state.storage.clone();
    let progress = ReadPairingStatus::execute(
        &mut storage,
        pairing_id,
        &token_hash(raw),
        OffsetDateTime::now_utc(),
    )
    .await
    .map_err(application_error)?;
    Ok(Json(json!({
        "status": progress.status.code(),
        "computer_id": progress.computer_id.map(ComputerId::into_uuid),
        "space_id": progress.space_id.map(SpaceId::into_uuid)
    })))
}

pub(super) async fn invite_human(
    State(state): State<RuntimeState>,
    jar: CookieJar,
    headers: HeaderMap,
    Path(space_id): Path<Uuid>,
    Json(body): Json<InviteHumanBody>,
) -> Result<(StatusCode, Json<CreatedInvitationResponse>), ApiError> {
    let token = require_session_token(&jar)?;
    let key = idempotency_header(&headers)?;
    let mut storage = state.storage.clone();
    let access = AuthorizeSpaceGovernance::execute(
        &mut storage,
        &token,
        SpaceId::from_uuid(space_id),
        OffsetDateTime::now_utc(),
    )
    .await
    .map_err(application_error)?;
    let issued = InviteHuman::execute(
        &mut storage,
        &RandomInvitationTokens,
        InviteHumanInput {
            invitation_id: Uuid::now_v7(),
            space_id: SpaceId::from_uuid(space_id),
            actor_id: access.member_id,
            email: &body.email,
            idempotency_key: IdempotencyKey::from_uuid(key),
            now: OffsetDateTime::now_utc(),
        },
    )
    .await
    .map_err(|error| match error {
        ApplicationError::Conflict => ApiError {
            status: StatusCode::CONFLICT,
            code: "invitation_already_pending",
            message: "this email already has a pending invitation to the Space",
        },
        other => application_error(other),
    })?;
    let view = invitation_response(&issued.view);
    Ok((
        if issued.token.is_some() {
            StatusCode::CREATED
        } else {
            StatusCode::OK
        },
        Json(CreatedInvitationResponse {
            id: view.id,
            space_id: view.space_id,
            space_name: view.space_name,
            space_slug: view.space_slug,
            email: view.email,
            expires_at: view.expires_at,
            accepted_at: view.accepted_at,
            accepted_by_member_id: view.accepted_by_member_id,
            token: issued.token.as_ref().map(|token| token.expose().to_owned()),
        }),
    ))
}

pub(super) async fn invitation_details(
    State(state): State<RuntimeState>,
    Path(invite_token): Path<String>,
) -> Result<Json<InvitationResponse>, ApiError> {
    let mut storage = state.storage.clone();
    let invitation = ReadInvitation::execute(
        &mut storage,
        &RawInvitationToken::new(invite_token),
        OffsetDateTime::now_utc(),
    )
    .await
    .map_err(invitation_error)?;
    Ok(Json(invitation_response(&invitation)))
}

pub(super) async fn accept_invitation(
    State(state): State<RuntimeState>,
    jar: CookieJar,
    Path(invite_token): Path<String>,
) -> Result<(StatusCode, Json<MemberResponse>), ApiError> {
    let human = authenticate(&state, &jar).await?;
    let member_id = Uuid::now_v7();
    let mut storage = state.storage.clone();
    let member = AcceptInvitation::execute(
        &mut storage,
        AcceptInvitationInput {
            token: &RawInvitationToken::new(invite_token),
            member_id: MemberId::from_uuid(member_id),
            user_id: human.user_id,
            user_email: &human.email_normalized,
            display_name: &human.display_name,
            now: OffsetDateTime::now_utc(),
        },
    )
    .await
    .map_err(accept_invitation_error)?;
    Ok((
        StatusCode::CREATED,
        Json(MemberResponse {
            id: member.member_id.into_uuid(),
            kind: MemberKindCode::Human,
            display_name: member.display_name,
            access_level: AccessLevelCode::Member,
            permissions: Vec::new(),
        }),
    ))
}

pub(super) fn invitation_response(invitation: &InvitationView) -> InvitationResponse {
    InvitationResponse {
        id: invitation.id,
        space_id: invitation.space_id.into_uuid(),
        space_name: invitation.space_name.clone(),
        space_slug: invitation.space_slug.clone(),
        email: invitation.email.clone(),
        expires_at: timestamp(invitation.expires_at),
        accepted_at: invitation.accepted_at.map(timestamp),
        accepted_by_member_id: invitation.accepted_by_member_id.map(MemberId::into_uuid),
    }
}

pub(super) fn invitation_error(error: ApplicationError) -> ApiError {
    use crate::server::domain::DomainError;
    match error {
        ApplicationError::NotFound | ApplicationError::Domain(DomainError::InvitationLapsed) => {
            ApiError {
                status: StatusCode::NOT_FOUND,
                code: "invitation_unavailable",
                message: "invitation link is not usable",
            }
        }
        other => application_error(other),
    }
}

pub(super) fn accept_invitation_error(error: ApplicationError) -> ApiError {
    use crate::server::domain::DomainError;
    match error {
        ApplicationError::Domain(DomainError::InvitationEmailMismatch) => ApiError {
            status: StatusCode::FORBIDDEN,
            code: "invitation_email_mismatch",
            message: "invitation was issued to another email",
        },
        ApplicationError::Conflict => ApiError {
            status: StatusCode::CONFLICT,
            code: "already_member",
            message: "signed in Human is already a Member of this Space",
        },
        other => invitation_error(other),
    }
}

pub(super) async fn create_space(
    State(state): State<RuntimeState>,
    jar: CookieJar,
    headers: HeaderMap,
    Json(body): Json<CreateSpaceBody>,
) -> Result<(StatusCode, Json<SpaceResponse>), ApiError> {
    let user = authenticate(&state, &jar).await?;
    let key = idempotency_header(&headers)?;
    let name = body.name.trim();
    let slug = body.slug.trim().to_lowercase();
    if name.is_empty() || slug.is_empty() {
        return Err(ApiError::invalid("Space name and slug are required"));
    }
    let accent = normalize_space_accent(&body.accent)
        .ok_or_else(|| ApiError::invalid("Space accent must be one of the preset values"))?;
    let space_id = Uuid::now_v7();
    let owner_id = Uuid::now_v7();
    let general_id = Uuid::now_v7();
    let now = OffsetDateTime::now_utc();
    let mut storage = state.storage.clone();
    let created = CreateSpace::execute(
        &mut storage,
        CreateSpaceInput {
            actor_user_id: user.user_id,
            space_id: SpaceId::from_uuid(space_id),
            owner_id: MemberId::from_uuid(owner_id),
            general_channel_id: ChannelId::from_uuid(general_id),
            name,
            slug: &slug,
            accent: &accent,
            owner_display_name: &user.display_name,
            idempotency_key: IdempotencyKey::from_uuid(key),
            now,
        },
    )
    .await
    .map_err(application_error)?;
    Ok((
        if created.space_id.into_uuid() == space_id {
            StatusCode::CREATED
        } else {
            StatusCode::OK
        },
        Json(space_response(
            created.space_id.into_uuid(),
            name,
            &slug,
            &accent,
            created.owner_id.into_uuid(),
            created.owner_id.into_uuid(),
            created.general_channel_id.into_uuid(),
        )),
    ))
}

pub(super) async fn list_spaces(
    State(state): State<RuntimeState>,
    jar: CookieJar,
) -> Result<Json<Vec<SpaceResponse>>, ApiError> {
    let user = authenticate(&state, &jar).await?;
    let rows = sqlx::query(
        "SELECT s.id,s.name,s.slug,s.accent,s.owner_member_id,hm.member_id AS current_member_id, \
         (SELECT id FROM channels WHERE space_id=s.id AND slug='general' LIMIT 1) AS general_channel_id \
         FROM spaces s JOIN human_members hm ON hm.space_id=s.id \
         WHERE hm.user_id=$1 AND s.deleted_at IS NULL ORDER BY s.created_at",
    )
    .bind(user.user_id)
    .fetch_all(&state.pool)
    .await
    .map_err(map_sqlx)?;
    Ok(Json(rows.iter().map(space_row).collect()))
}

pub(super) async fn space_by_slug(
    State(state): State<RuntimeState>,
    jar: CookieJar,
    Path(slug): Path<String>,
) -> Result<Json<SpaceResponse>, ApiError> {
    let user = authenticate(&state, &jar).await?;
    let row = sqlx::query(
        "SELECT s.id,s.name,s.slug,s.accent,s.owner_member_id,hm.member_id AS current_member_id, \
         (SELECT id FROM channels WHERE space_id=s.id AND slug='general' LIMIT 1) AS general_channel_id \
         FROM spaces s JOIN human_members hm ON hm.space_id=s.id \
         WHERE hm.user_id=$1 AND lower(s.slug)=lower($2) AND s.deleted_at IS NULL",
    )
    .bind(user.user_id)
    .bind(slug)
    .fetch_optional(&state.pool)
    .await
    .map_err(map_sqlx)?
    .ok_or_else(ApiError::not_found)?;
    Ok(Json(space_row(&row)))
}

pub(super) async fn list_members(
    State(state): State<RuntimeState>,
    jar: CookieJar,
    Path(space_id): Path<Uuid>,
) -> Result<Json<Vec<MemberResponse>>, ApiError> {
    current_member(&state, &jar, space_id).await?;
    let rows = sqlx::query("SELECT id,kind,display_name,access_level FROM members WHERE space_id=$1 AND retired_at IS NULL ORDER BY created_at")
        .bind(space_id).fetch_all(&state.pool).await.map_err(map_sqlx)?;
    let mut values = Vec::with_capacity(rows.len());
    for row in rows {
        values.push(member_row(&state.pool, &row).await?);
    }
    Ok(Json(values))
}

pub(super) fn permission_action(action_code: &str) -> Result<PermissionAction, ApiError> {
    match action_code {
        "channel.create" => Ok(PermissionAction::ChannelCreate),
        "agent.create" => Ok(PermissionAction::AgentCreate),
        _ => Err(ApiError::invalid("Permission action code is not supported")),
    }
}

pub(super) async fn set_permission(
    state: &RuntimeState,
    jar: &CookieJar,
    headers: &HeaderMap,
    member_id: Uuid,
    action_code: &str,
    enabled: bool,
) -> Result<Json<MemberResponse>, ApiError> {
    let space_id = sqlx::query_scalar::<_, Uuid>("SELECT space_id FROM members WHERE id=$1")
        .bind(member_id)
        .fetch_optional(&state.pool)
        .await
        .map_err(map_sqlx)?
        .ok_or_else(ApiError::not_found)?;
    let actor = current_member(state, jar, space_id).await?;
    let mut storage = state.storage.clone();
    SetPermission::execute(
        &mut storage,
        MemberId::from_uuid(actor),
        MemberId::from_uuid(member_id),
        permission_action(action_code)?,
        enabled,
        IdempotencyKey::from_uuid(idempotency_header(headers)?),
        OffsetDateTime::now_utc(),
    )
    .await
    .map_err(application_error)?;
    let row = sqlx::query("SELECT id,kind,display_name,access_level FROM members WHERE id=$1")
        .bind(member_id)
        .fetch_one(&state.pool)
        .await
        .map_err(map_sqlx)?;
    Ok(Json(member_row(&state.pool, &row).await?))
}

pub(super) async fn grant_permission(
    State(state): State<RuntimeState>,
    jar: CookieJar,
    headers: HeaderMap,
    Path((member_id, action_code)): Path<(Uuid, String)>,
) -> Result<Json<MemberResponse>, ApiError> {
    set_permission(&state, &jar, &headers, member_id, &action_code, true).await
}

pub(super) async fn revoke_permission(
    State(state): State<RuntimeState>,
    jar: CookieJar,
    headers: HeaderMap,
    Path((member_id, action_code)): Path<(Uuid, String)>,
) -> Result<Json<MemberResponse>, ApiError> {
    set_permission(&state, &jar, &headers, member_id, &action_code, false).await
}

pub(super) fn session_token(jar: &CookieJar) -> Option<RawSessionToken> {
    jar.get(SESSION_COOKIE)
        .map(|cookie| RawSessionToken::new(cookie.value().to_owned()))
}

pub(super) fn require_session_token(jar: &CookieJar) -> Result<RawSessionToken, ApiError> {
    session_token(jar).ok_or_else(ApiError::unauthenticated)
}

pub(super) fn session_cookie(jar: CookieJar, token: &RawSessionToken) -> CookieJar {
    let cookie = Cookie::build((SESSION_COOKIE, token.expose().to_owned()))
        .path("/")
        .http_only(true)
        .same_site(SameSite::Lax)
        .build();
    jar.add(cookie)
}

pub(super) async fn authenticate(
    state: &RuntimeState,
    jar: &CookieJar,
) -> Result<AuthenticatedHuman, ApiError> {
    let token = require_session_token(jar)?;
    let mut storage = state.storage.clone();
    AuthenticateSession::execute(&mut storage, &token, OffsetDateTime::now_utc())
        .await
        .map_err(application_error)
}

pub(in crate::server::adapters) async fn current_member(
    state: &RuntimeState,
    jar: &CookieJar,
    space_id: Uuid,
) -> Result<Uuid, ApiError> {
    let token = require_session_token(jar)?;
    let mut storage = state.storage.clone();
    let access = AuthorizeSpaceAccess::execute(
        &mut storage,
        &token,
        SpaceId::from_uuid(space_id),
        OffsetDateTime::now_utc(),
    )
    .await
    .map_err(application_error)?;
    Ok(access.member_id.into_uuid())
}

pub(super) fn user_response(human: &AuthenticatedHuman) -> UserResponse {
    UserResponse {
        id: human.user_id,
        display_name: human.display_name.clone(),
        email: human.email_normalized.clone(),
    }
}

pub(super) async fn update_space_member(
    State(state): State<RuntimeState>,
    jar: CookieJar,
    Path((space_id, member_id)): Path<(Uuid, Uuid)>,
    Json(body): Json<UpdateMemberBody>,
) -> Result<Json<MemberResponse>, ApiError> {
    let actor_id = current_member(&state, &jar, space_id).await?;
    let Some(requested) = body.access_level.as_deref() else {
        return Err(ApiError::invalid("access_level is required"));
    };
    let requested = match requested {
        "admin" => AccessLevel::Admin,
        "member" => AccessLevel::Member,
        _ => return Err(ApiError::invalid("access_level must be admin or member")),
    };
    let mut storage = state.storage.clone();
    UpdateMemberAccess::execute(
        &mut storage,
        MemberId::from_uuid(actor_id),
        MemberId::from_uuid(member_id),
        SpaceId::from_uuid(space_id),
        requested,
    )
    .await
    .map_err(application_error)?;
    let row = sqlx::query(
        "SELECT id,kind,display_name,access_level FROM members WHERE id=$1 AND space_id=$2",
    )
    .bind(member_id)
    .bind(space_id)
    .fetch_one(&state.pool)
    .await
    .map_err(map_sqlx)?;
    Ok(Json(member_row(&state.pool, &row).await?))
}
