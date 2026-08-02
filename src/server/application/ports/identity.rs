use super::*;

#[async_trait]
pub(in crate::server) trait IdentityTransaction {
    #[allow(clippy::too_many_arguments)]
    async fn create_space(
        &mut self,
        actor_user_id: uuid::Uuid,
        space_id: SpaceId,
        owner_id: MemberId,
        general_channel_id: ChannelId,
        name: &str,
        slug: &str,
        accent: &str,
        owner_display_name: &str,
        idempotency_key: IdempotencyKey,
        now: time::OffsetDateTime,
    ) -> Result<CreatedSpace, ApplicationError>;
    async fn insert_human(
        &mut self,
        user_id: uuid::Uuid,
        registration: &HumanRegistration,
        password_hash: &str,
        now: time::OffsetDateTime,
    ) -> Result<(), ApplicationError>;
    async fn human_credential(
        &mut self,
        email_normalized: &str,
    ) -> Result<Option<(AuthenticatedHuman, String)>, ApplicationError>;
    async fn insert_browser_session(
        &mut self,
        session_id: uuid::Uuid,
        user_id: uuid::Uuid,
        token_hash: &str,
        expires_at: time::OffsetDateTime,
        now: time::OffsetDateTime,
    ) -> Result<(), ApplicationError>;
    async fn human_for_session(
        &mut self,
        token_hash: &str,
        now: time::OffsetDateTime,
    ) -> Result<Option<AuthenticatedHuman>, ApplicationError>;
    async fn delete_browser_session(&mut self, token_hash: &str) -> Result<(), ApplicationError>;
    async fn space_access(
        &mut self,
        user_id: uuid::Uuid,
        space_id: SpaceId,
    ) -> Result<Option<SpaceAccess>, ApplicationError>;
    async fn space_of_agent(
        &mut self,
        agent_id: MemberId,
    ) -> Result<Option<SpaceId>, ApplicationError>;
    async fn space_of_computer(
        &mut self,
        computer_id: ComputerId,
    ) -> Result<Option<SpaceId>, ApplicationError>;
    async fn insert_pairing(
        &mut self,
        pairing_id: uuid::Uuid,
        pairing: &Pairing,
        code_hash: &str,
        now: time::OffsetDateTime,
    ) -> Result<(), ApplicationError>;
    async fn save_pairing(
        &mut self,
        pairing_id: uuid::Uuid,
        pairing: &Pairing,
        now: time::OffsetDateTime,
    ) -> Result<(), ApplicationError>;
    async fn pairing_by_code(
        &mut self,
        pairing_id: uuid::Uuid,
        code_hash: &str,
    ) -> Result<Option<Pairing>, ApplicationError>;
    async fn pairing_by_code_for_update(
        &mut self,
        pairing_id: uuid::Uuid,
        code_hash: &str,
    ) -> Result<Option<Pairing>, ApplicationError>;
    async fn pairing_by_token(
        &mut self,
        pairing_id: uuid::Uuid,
        token_hash: &str,
    ) -> Result<Option<Pairing>, ApplicationError>;
    async fn insert_invitation(
        &mut self,
        invitation_id: uuid::Uuid,
        invitation: &Invitation,
        now: time::OffsetDateTime,
    ) -> Result<(), ApplicationError>;
    async fn save_invitation(
        &mut self,
        invitation_id: uuid::Uuid,
        invitation: &Invitation,
    ) -> Result<(), ApplicationError>;
    async fn invitation_by_token(
        &mut self,
        token_hash: &str,
    ) -> Result<Option<(uuid::Uuid, Invitation)>, ApplicationError>;
    async fn invitation_by_token_for_update(
        &mut self,
        token_hash: &str,
    ) -> Result<Option<(uuid::Uuid, Invitation)>, ApplicationError>;
    async fn space_identity(
        &mut self,
        space_id: SpaceId,
    ) -> Result<Option<(String, String)>, ApplicationError>;
    async fn insert_human_member(
        &mut self,
        record: &HumanMemberRecord,
    ) -> Result<(), ApplicationError>;
    async fn space_human_member(
        &mut self,
        user_id: uuid::Uuid,
        space_id: SpaceId,
    ) -> Result<Option<SpaceHumanMember>, ApplicationError>;
    async fn space_of_member(
        &mut self,
        member_id: MemberId,
    ) -> Result<Option<SpaceId>, ApplicationError>;
    async fn member(&mut self, member_id: MemberId) -> Result<Member, ApplicationError>;
    async fn save_member(&mut self, member: Member) -> Result<(), ApplicationError>;
    async fn insert_computer(&mut self, record: &ComputerRecord) -> Result<(), ApplicationError>;
    async fn paired_computer(
        &mut self,
        computer_id: ComputerId,
    ) -> Result<Option<PairedComputer>, ApplicationError>;
    async fn space_computers(
        &mut self,
        space_id: SpaceId,
    ) -> Result<Vec<PairedComputer>, ApplicationError>;
    async fn computer_for_token(
        &mut self,
        computer_id: ComputerId,
        token_hash: &str,
    ) -> Result<Option<bool>, ApplicationError>;
    async fn agent(&mut self, id: MemberId) -> Result<Agent, ApplicationError>;
    async fn computer(&mut self, id: ComputerId) -> Result<Computer, ApplicationError>;
    async fn computer_has_assigned_agents(
        &mut self,
        computer_id: ComputerId,
    ) -> Result<bool, ApplicationError>;
    async fn has_permission(
        &mut self,
        actor: MemberId,
        action: PermissionAction,
    ) -> Result<bool, ApplicationError>;
    async fn can_manage_permissions(
        &mut self,
        actor: MemberId,
        target: MemberId,
    ) -> Result<bool, ApplicationError>;
    async fn can_operate_agent(
        &mut self,
        computer_id: ComputerId,
        agent_id: MemberId,
    ) -> Result<bool, ApplicationError>;
    async fn member_access_level(
        &mut self,
        member_id: MemberId,
        space_id: SpaceId,
    ) -> Result<AccessLevel, ApplicationError>;
    async fn computer_accepts_agent(
        &mut self,
        computer_id: ComputerId,
        space_id: SpaceId,
    ) -> Result<bool, ApplicationError>;
    async fn grant_permission(
        &mut self,
        target: MemberId,
        action: PermissionAction,
        granted_by: MemberId,
        now: time::OffsetDateTime,
    ) -> Result<(), ApplicationError>;
    async fn revoke_permission(
        &mut self,
        target: MemberId,
        action: PermissionAction,
    ) -> Result<(), ApplicationError>;
    async fn insert_agent(&mut self, member: Member, agent: Agent) -> Result<(), ApplicationError>;
    async fn save_agent(&mut self, agent: Agent) -> Result<(), ApplicationError>;
    async fn save_computer(&mut self, computer: Computer) -> Result<(), ApplicationError>;
}
