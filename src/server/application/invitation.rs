use time::OffsetDateTime;

use crate::ids::{IdempotencyKey, MemberId, SpaceId};

use crate::server::domain::invitation::{Invitation, InvitationDraft};

use super::ports::{
    ApplicationError, HumanMemberRecord, InvitationTokenPort, InvitationView, RawInvitationToken,
    ServerTransaction, SpaceHumanMember, TransactionPort,
};

const CREATE_ACTION: &str = "space.invitation.create";

/// 创建待接受邀请，并返回一次性 token。token 明文不落库。
pub(in crate::server) struct InviteHuman;

pub(in crate::server) struct InviteHumanInput<'a> {
    pub(in crate::server) invitation_id: uuid::Uuid,
    pub(in crate::server) space_id: SpaceId,
    pub(in crate::server) actor_id: MemberId,
    pub(in crate::server) email: &'a str,
    pub(in crate::server) idempotency_key: IdempotencyKey,
    pub(in crate::server) now: OffsetDateTime,
}

/// 创建结果。`token`只在首次创建时存在，重放不返回明文。
pub(in crate::server) struct IssuedInvitation {
    pub(in crate::server) view: InvitationView,
    pub(in crate::server) token: Option<RawInvitationToken>,
}

impl InviteHuman {
    pub(in crate::server) async fn execute<P: TransactionPort>(
        port: &mut P,
        tokens: &impl InvitationTokenPort,
        input: InviteHumanInput<'_>,
    ) -> Result<IssuedInvitation, ApplicationError> {
        let token = tokens.generate();
        let draft = InvitationDraft::new(
            input.space_id,
            input.email,
            &token.sha256_hash(),
            input.actor_id,
        )?;
        let invitation = Invitation::open(draft, input.now);
        port.transact(async |transaction| {
            // 并发创建在同一 actor 与 key 上串行化，避免签发两个可用链接。
            transaction
                .lock_idempotency(input.actor_id, CREATE_ACTION, input.idempotency_key)
                .await?;
            if let Some(invitation_id) = transaction
                .resource_for_idempotency(input.actor_id, CREATE_ACTION, input.idempotency_key)
                .await?
            {
                // 重放不返回明文 token：明文只在首次生成时存在。
                return Ok(IssuedInvitation {
                    view: read_view(transaction, invitation_id, &invitation).await?,
                    token: None,
                });
            }
            transaction
                .insert_invitation(input.invitation_id, &invitation, input.now)
                .await?;
            transaction
                .record_resource_idempotency(
                    input.actor_id,
                    CREATE_ACTION,
                    input.idempotency_key,
                    input.invitation_id,
                )
                .await?;
            Ok(IssuedInvitation {
                view: read_view(transaction, input.invitation_id, &invitation).await?,
                token: Some(token.clone()),
            })
        })
        .await
    }
}

/// 读取邀请投影。未命中、已过期和已接受返回同一个错误，
/// 使该端点不能用于探测 token 是否存在。
pub(in crate::server) struct ReadInvitation;

impl ReadInvitation {
    pub(in crate::server) async fn execute<P: TransactionPort>(
        port: &mut P,
        token: &RawInvitationToken,
        now: OffsetDateTime,
    ) -> Result<InvitationView, ApplicationError> {
        let token_hash = token.sha256_hash();
        port.transact(async |transaction| {
            let (invitation_id, mut invitation) = transaction
                .invitation_by_token(&token_hash)
                .await?
                .ok_or(ApplicationError::NotFound)?;
            // 过期状态先落库，后续读取不必重复判定。
            if invitation.has_lapsed(now) {
                invitation.lapse();
                transaction
                    .save_invitation(invitation_id, &invitation)
                    .await?;
            }
            read_view(transaction, invitation_id, &invitation).await
        })
        .await
    }
}

/// 接受邀请并建立 Human Member。邀请状态、Member 与幂等记录在同一事务成立。
pub(in crate::server) struct AcceptInvitation;

pub(in crate::server) struct AcceptInvitationInput<'a> {
    pub(in crate::server) token: &'a RawInvitationToken,
    pub(in crate::server) member_id: MemberId,
    pub(in crate::server) user_id: uuid::Uuid,
    pub(in crate::server) user_email: &'a str,
    pub(in crate::server) display_name: &'a str,
    pub(in crate::server) handle: &'a str,
    pub(in crate::server) now: OffsetDateTime,
}

impl AcceptInvitation {
    pub(in crate::server) async fn execute<P: TransactionPort>(
        port: &mut P,
        input: AcceptInvitationInput<'_>,
    ) -> Result<SpaceHumanMember, ApplicationError> {
        let token_hash = input.token.sha256_hash();
        let display_name = input.display_name.trim();
        if display_name.is_empty() || input.handle.is_empty() {
            return Err(crate::server::domain::DomainError::InvalidInvitation.into());
        }
        port.transact(async |transaction| {
            // 行锁使并发接受串行化。token 与 User 共同标识一次接受，
            // 因此重试不需要额外的 idempotency key。
            let (invitation_id, mut invitation) = transaction
                .invitation_by_token_for_update(&token_hash)
                .await?
                .ok_or(ApplicationError::NotFound)?;
            if let Some(existing) = transaction
                .space_human_member(input.user_id, invitation.draft.space_id)
                .await?
            {
                // 已接受且接受者就是本 User 时返回同一个 Member，重试成立。
                // 通过另一个链接重复加入同一 Space 属于冲突。
                if invitation.accepted_by_member_id == Some(existing.member_id) {
                    return Ok(existing);
                }
                return Err(ApplicationError::Conflict);
            }
            // 过期状态先落库，再拒绝接受。
            if invitation.has_lapsed(input.now) {
                invitation.lapse();
                transaction
                    .save_invitation(invitation_id, &invitation)
                    .await?;
            }
            invitation.accept(input.member_id, input.user_email, input.now)?;
            let record = HumanMemberRecord {
                member_id: input.member_id,
                space_id: invitation.draft.space_id,
                user_id: input.user_id,
                display_name: display_name.to_owned(),
                handle: input.handle.to_owned(),
                created_at: input.now,
            };
            transaction.insert_human_member(&record).await?;
            transaction
                .save_invitation(invitation_id, &invitation)
                .await?;
            Ok(SpaceHumanMember {
                member_id: record.member_id,
                space_id: record.space_id,
                display_name: record.display_name,
                handle: record.handle,
            })
        })
        .await
    }
}

async fn read_view<T: ServerTransaction>(
    transaction: &mut T,
    invitation_id: uuid::Uuid,
    invitation: &Invitation,
) -> Result<InvitationView, ApplicationError> {
    let (space_name, space_slug) = transaction
        .space_identity(invitation.draft.space_id)
        .await?
        .ok_or(ApplicationError::NotFound)?;
    Ok(InvitationView {
        id: invitation_id,
        space_id: invitation.draft.space_id,
        space_name,
        space_slug,
        email: invitation.draft.email_normalized.clone(),
        expires_at: invitation.expires_at,
        accepted_at: invitation.accepted_at,
        accepted_by_member_id: invitation.accepted_by_member_id,
    })
}
