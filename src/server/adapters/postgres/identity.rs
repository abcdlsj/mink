use super::*;
use crate::protocol::computer::{AgentSuspend, SuspendMode};

impl PostgresTransaction {
    pub(super) async fn computer_has_assigned_agents(
        &mut self,
        computer_id: ComputerId,
    ) -> Result<bool, ApplicationError> {
        sqlx::query("SELECT member_id FROM agents WHERE computer_id = $1 FOR UPDATE")
            .bind(computer_id.into_uuid())
            .fetch_all(&mut *self.connection)
            .await
            .map(|rows| !rows.is_empty())
            .map_err(map_sqlx)
    }

    pub(super) async fn can_operate_agent(
        &mut self,
        computer_id: ComputerId,
        agent_id: MemberId,
    ) -> Result<bool, ApplicationError> {
        sqlx::query_scalar::<_, bool>(
            "SELECT EXISTS(SELECT 1 FROM agents JOIN computers ON computers.id=agents.computer_id \
             WHERE agents.member_id=$1 AND agents.computer_id=$2 AND computers.deleted_at IS NULL)",
        )
        .bind(agent_id.into_uuid())
        .bind(computer_id.into_uuid())
        .fetch_one(&mut *self.connection)
        .await
        .map_err(map_sqlx)
    }

    pub(super) async fn pairing_by_code(
        &mut self,
        pairing_id: Uuid,
        code_hash: &str,
    ) -> Result<Option<Pairing>, ApplicationError> {
        let row = sqlx::query("SELECT * FROM computer_pairings WHERE id=$1 AND code_hash=$2")
            .bind(pairing_id)
            .bind(code_hash)
            .fetch_optional(&mut *self.connection)
            .await
            .map_err(map_sqlx)?;
        row.map(|row| pairing_from_row(&row)).transpose()
    }

    pub(super) async fn queue_agent_suspend(
        &mut self,
        agent_id: MemberId,
        computer_id: Option<ComputerId>,
        cancel_current_run: bool,
    ) -> Result<(), ApplicationError> {
        let Some(computer_id) = computer_id else {
            return Ok(());
        };
        self.queue_command(
            computer_id,
            Command::AgentSuspend(AgentSuspend {
                agent_id: AgentId::from_uuid(agent_id.into_uuid()),
                mode: if cancel_current_run {
                    SuspendMode::CancelCurrentRun
                } else {
                    SuspendMode::AfterCurrentRun
                },
            }),
        )
        .await
    }

    pub(super) async fn insert_invitation(
        &mut self,
        invitation_id: Uuid,
        invitation: &Invitation,
        now: OffsetDateTime,
    ) -> Result<(), ApplicationError> {
        sqlx::query(
            "INSERT INTO space_invitations\
             (id,space_id,email_normalized,token_hash,status,expires_at,created_by_member_id,created_at) \
             VALUES($1,$2,$3,$4,$5,$6,$7,$8)",
        )
        .bind(invitation_id)
        .bind(invitation.draft.space_id.into_uuid())
        .bind(&invitation.draft.email_normalized)
        .bind(&invitation.draft.token_hash)
        .bind(invitation.status.code())
        .bind(invitation.expires_at)
        .bind(invitation.draft.created_by_member_id.into_uuid())
        .bind(now)
        .execute(&mut *self.connection)
        .await
        .map_err(map_sqlx)?;
        Ok(())
    }

    pub(super) async fn space_of_agent(
        &mut self,
        agent_id: MemberId,
    ) -> Result<Option<SpaceId>, ApplicationError> {
        let space_id =
            sqlx::query_scalar::<_, Uuid>("SELECT space_id FROM agents WHERE member_id=$1")
                .bind(agent_id.into_uuid())
                .fetch_optional(&mut *self.connection)
                .await
                .map_err(map_sqlx)?;
        Ok(space_id.map(SpaceId::from_uuid))
    }

    pub(super) async fn save_invitation(
        &mut self,
        invitation_id: Uuid,
        invitation: &Invitation,
    ) -> Result<(), ApplicationError> {
        let changed = sqlx::query(
            "UPDATE space_invitations SET status=$2,accepted_by_member_id=$3,accepted_at=$4 \
             WHERE id=$1",
        )
        .bind(invitation_id)
        .bind(invitation.status.code())
        .bind(invitation.accepted_by_member_id.map(MemberId::into_uuid))
        .bind(invitation.accepted_at)
        .execute(&mut *self.connection)
        .await
        .map_err(map_sqlx)?;
        if changed.rows_affected() != 1 {
            return Err(ApplicationError::NotFound);
        }
        Ok(())
    }

    pub(super) async fn member_access_level(
        &mut self,
        member_id: MemberId,
        space_id: SpaceId,
    ) -> Result<AccessLevel, ApplicationError> {
        let value = sqlx::query_scalar::<_, String>(
            "SELECT access_level FROM members WHERE id=$1 AND space_id=$2 AND retired_at IS NULL",
        )
        .bind(member_id.into_uuid())
        .bind(space_id.into_uuid())
        .fetch_one(&mut *self.connection)
        .await
        .map_err(map_sqlx)?;
        access_level_from_str(&value)
    }

    pub(super) async fn pairing_by_token(
        &mut self,
        pairing_id: Uuid,
        token_hash: &str,
    ) -> Result<Option<Pairing>, ApplicationError> {
        let row = sqlx::query("SELECT * FROM computer_pairings WHERE id=$1 AND token_hash=$2")
            .bind(pairing_id)
            .bind(token_hash)
            .fetch_optional(&mut *self.connection)
            .await
            .map_err(map_sqlx)?;
        row.map(|row| pairing_from_row(&row)).transpose()
    }

    pub(super) async fn space_identity(
        &mut self,
        space_id: SpaceId,
    ) -> Result<Option<(String, String)>, ApplicationError> {
        let row = sqlx::query("SELECT name,slug FROM spaces WHERE id=$1")
            .bind(space_id.into_uuid())
            .fetch_optional(&mut *self.connection)
            .await
            .map_err(map_sqlx)?;
        Ok(row.map(|row| (row.get("name"), row.get("slug"))))
    }

    pub(super) async fn space_access(
        &mut self,
        user_id: Uuid,
        space_id: SpaceId,
    ) -> Result<Option<SpaceAccess>, ApplicationError> {
        let row = sqlx::query(
            "SELECT members.id,members.access_level FROM human_members \
             JOIN members ON members.id=human_members.member_id \
             AND members.space_id=human_members.space_id \
             WHERE human_members.space_id=$1 AND human_members.user_id=$2",
        )
        .bind(space_id.into_uuid())
        .bind(user_id)
        .fetch_optional(&mut *self.connection)
        .await
        .map_err(map_sqlx)?;
        row.map(|row| {
            Ok(SpaceAccess {
                member_id: MemberId::from_uuid(row.get("id")),
                space_id,
                access_level: access_level_from_str(row.get("access_level"))?,
            })
        })
        .transpose()
    }

    pub(super) async fn space_human_member(
        &mut self,
        user_id: Uuid,
        space_id: SpaceId,
    ) -> Result<Option<SpaceHumanMember>, ApplicationError> {
        let row = sqlx::query(
            "SELECT m.id,m.display_name,m.handle FROM human_members hm \
             JOIN members m ON m.id=hm.member_id \
             WHERE hm.user_id=$1 AND hm.space_id=$2",
        )
        .bind(user_id)
        .bind(space_id.into_uuid())
        .fetch_optional(&mut *self.connection)
        .await
        .map_err(map_sqlx)?;
        Ok(row.map(|row| SpaceHumanMember {
            member_id: MemberId::from_uuid(row.get("id")),
            space_id,
            display_name: row.get("display_name"),
            handle: row.get("handle"),
        }))
    }

    pub(super) async fn member(&mut self, id: MemberId) -> Result<Member, ApplicationError> {
        let row = sqlx::query(
            "SELECT id,space_id,display_name,handle,access_level,created_at FROM members \
             WHERE id=$1 AND retired_at IS NULL FOR UPDATE",
        )
        .bind(id.into_uuid())
        .fetch_one(&mut *self.connection)
        .await
        .map_err(map_sqlx)?;
        Ok(Member {
            id,
            space_id: SpaceId::from_uuid(row.get("space_id")),
            display_name: row.get("display_name"),
            handle: row.get("handle"),
            access_level: access_level_from_str(row.get("access_level"))?,
            created_at: row.get("created_at"),
        })
    }

    pub(super) async fn space_of_computer(
        &mut self,
        computer_id: ComputerId,
    ) -> Result<Option<SpaceId>, ApplicationError> {
        let space_id = sqlx::query_scalar::<_, Uuid>("SELECT space_id FROM computers WHERE id=$1")
            .bind(computer_id.into_uuid())
            .fetch_optional(&mut *self.connection)
            .await
            .map_err(map_sqlx)?;
        Ok(space_id.map(SpaceId::from_uuid))
    }

    pub(super) async fn insert_agent(
        &mut self,
        member: Member,
        agent: Agent,
    ) -> Result<(), ApplicationError> {
        sqlx::query(
            "INSERT INTO members (id,space_id,kind,display_name,handle,access_level,created_at) \
             VALUES ($1,$2,'agent',$3,$4,$5,$6)",
        )
        .bind(member.id.into_uuid())
        .bind(member.space_id.into_uuid())
        .bind(&member.display_name)
        .bind(&member.handle)
        .bind(access_level_str(member.access_level))
        .bind(member.created_at)
        .execute(&mut *self.connection)
        .await
        .map_err(map_sqlx)?;
        sqlx::query(
            "INSERT INTO agents (member_id,space_id,computer_id,role_text,role_revision,lifecycle, \
             driver_kind,driver_config_json,created_at) VALUES ($1,$2,$3,$4,$5,$6,$7,'{}',$8)",
        )
        .bind(agent.member_id.into_uuid())
        .bind(agent.space_id.into_uuid())
        .bind(agent.computer_id.map(ComputerId::into_uuid))
        .bind(&agent.role_text)
        .bind(i64::try_from(agent.role_revision).map_err(|_| ApplicationError::Conflict)?)
        .bind(agent_lifecycle_str(agent.lifecycle))
        .bind(driver_kind_str(agent.driver_kind))
        .bind(member.created_at)
        .execute(&mut *self.connection)
        .await
        .map_err(map_sqlx)?;
        Ok(())
    }

    pub(super) async fn save_member(&mut self, member: Member) -> Result<(), ApplicationError> {
        let changed = sqlx::query(
            "UPDATE members SET display_name=$2,access_level=$3 WHERE id=$1 AND space_id=$4",
        )
        .bind(member.id.into_uuid())
        .bind(&member.display_name)
        .bind(access_level_str(member.access_level))
        .bind(member.space_id.into_uuid())
        .execute(&mut *self.connection)
        .await
        .map_err(map_sqlx)?;
        if changed.rows_affected() != 1 {
            return Err(ApplicationError::NotFound);
        }
        Ok(())
    }

    pub(super) async fn channel_access(
        &mut self,
        user_id: Uuid,
        channel_id: ChannelId,
    ) -> Result<Option<MemberId>, ApplicationError> {
        let member_id = sqlx::query_scalar::<_, Uuid>(
            "SELECT human_members.member_id FROM channels \
             JOIN human_members ON human_members.space_id=channels.space_id \
             JOIN channel_members ON channel_members.channel_id=channels.id \
             AND channel_members.member_id=human_members.member_id \
             WHERE channels.id=$1 AND human_members.user_id=$2",
        )
        .bind(channel_id.into_uuid())
        .bind(user_id)
        .fetch_optional(&mut *self.connection)
        .await
        .map_err(map_sqlx)?;
        Ok(member_id.map(MemberId::from_uuid))
    }

    #[allow(clippy::too_many_arguments)]
    pub(super) async fn create_space(
        &mut self,
        actor_user_id: Uuid,
        space_id: SpaceId,
        owner_id: MemberId,
        general_channel_id: ChannelId,
        name: &str,
        slug: &str,
        accent: &str,
        owner_handle: &str,
        owner_display_name: &str,
        idempotency_key: IdempotencyKey,
        now: OffsetDateTime,
    ) -> Result<CreatedSpace, ApplicationError> {
        let lock_key = format!(
            "{}:space.create:{}",
            actor_user_id,
            idempotency_key.into_uuid()
        );
        sqlx::query("SELECT pg_advisory_xact_lock(hashtextextended($1, 0))")
            .bind(lock_key)
            .execute(&mut *self.connection)
            .await
            .map_err(map_sqlx)?;
        if let Some(row) = sqlx::query(
            "SELECT s.id,s.owner_member_id,(SELECT id FROM channels WHERE space_id=s.id AND slug='general' LIMIT 1) AS general_channel_id \
             FROM idempotency_records records \
             JOIN human_members hm ON hm.member_id=records.actor_member_id \
             JOIN spaces s ON s.id=records.resource_id \
             WHERE hm.user_id=$1 AND records.action='space.create' AND records.idempotency_key=$2",
        )
        .bind(actor_user_id)
        .bind(idempotency_key.into_uuid())
        .fetch_optional(&mut *self.connection)
        .await
        .map_err(map_sqlx)?
        {
            return Ok(CreatedSpace {
                space_id: SpaceId::from_uuid(row.get("id")),
                owner_id: MemberId::from_uuid(row.get("owner_member_id")),
                general_channel_id: ChannelId::from_uuid(row.get("general_channel_id")),
            });
        }
        sqlx::query("SET CONSTRAINTS ALL DEFERRED")
            .execute(&mut *self.connection)
            .await
            .map_err(map_sqlx)?;
        sqlx::query(
            "INSERT INTO spaces(id,slug,name,accent,owner_member_id,created_at) VALUES($1,$2,$3,$4,$5,$6)",
        )
        .bind(space_id.into_uuid())
        .bind(slug)
        .bind(name)
        .bind(accent)
        .bind(owner_id.into_uuid())
        .bind(now)
        .execute(&mut *self.connection)
        .await
        .map_err(map_sqlx)?;
        sqlx::query("INSERT INTO members(id,space_id,kind,display_name,handle,access_level,created_at) VALUES($1,$2,'human',$3,$4,'owner',$5)")
            .bind(owner_id.into_uuid())
            .bind(space_id.into_uuid())
            .bind(owner_display_name)
            .bind(owner_handle)
            .bind(now)
            .execute(&mut *self.connection)
            .await
            .map_err(map_sqlx)?;
        sqlx::query("INSERT INTO human_members(member_id,space_id,user_id) VALUES($1,$2,$3)")
            .bind(owner_id.into_uuid())
            .bind(space_id.into_uuid())
            .bind(actor_user_id)
            .execute(&mut *self.connection)
            .await
            .map_err(map_sqlx)?;
        sqlx::query("INSERT INTO channels(id,space_id,kind,slug,topic,created_at) VALUES($1,$2,'public','general',NULL,$3)")
            .bind(general_channel_id.into_uuid())
            .bind(space_id.into_uuid())
            .bind(now)
            .execute(&mut *self.connection)
            .await
            .map_err(map_sqlx)?;
        sqlx::query("INSERT INTO channel_members(channel_id,space_id,member_id,joined_at) VALUES($1,$2,$3,$4)")
            .bind(general_channel_id.into_uuid())
            .bind(space_id.into_uuid())
            .bind(owner_id.into_uuid())
            .bind(now)
            .execute(&mut *self.connection)
            .await
            .map_err(map_sqlx)?;
        let result_hash = Sha256::digest(space_id.into_uuid().as_bytes());
        sqlx::query("INSERT INTO idempotency_records(actor_member_id,action,idempotency_key,response_code,resource_id,result_hash,created_at) VALUES($1,'space.create',$2,'ok',$3,$4,$5)")
            .bind(owner_id.into_uuid())
            .bind(idempotency_key.into_uuid())
            .bind(space_id.into_uuid())
            .bind(result_hash.as_slice())
            .bind(now)
            .execute(&mut *self.connection)
            .await
            .map_err(map_sqlx)?;
        sqlx::query("INSERT INTO audit_events(id,space_id,actor_member_id,action,subject_type,subject_id,created_at) VALUES($1,$2,$3,'space.created','space',$2,$4)")
            .bind(Uuid::now_v7())
            .bind(space_id.into_uuid())
            .bind(owner_id.into_uuid())
            .bind(now)
            .execute(&mut *self.connection)
            .await
            .map_err(map_sqlx)?;
        sqlx::query("INSERT INTO outbox_events(id,space_id,kind,payload_json,created_at) VALUES($1,$2,'channel.created',$3,$4)")
            .bind(Uuid::now_v7())
            .bind(space_id.into_uuid())
            .bind(json!({"resource_id": general_channel_id.into_uuid()}))
            .bind(now)
            .execute(&mut *self.connection)
            .await
            .map_err(map_sqlx)?;
        Ok(CreatedSpace {
            space_id,
            owner_id,
            general_channel_id,
        })
    }

    pub(super) async fn grant_permission(
        &mut self,
        target: MemberId,
        action: PermissionAction,
        granted_by: MemberId,
        now: OffsetDateTime,
    ) -> Result<(), ApplicationError> {
        sqlx::query(
            "INSERT INTO member_permissions(member_id,space_id,action_code,granted_by_member_id,created_at) \
             SELECT target.id,target.space_id,$2,$3,$4 FROM members target WHERE target.id=$1 \
             ON CONFLICT(member_id,action_code) DO NOTHING",
        )
        .bind(target.into_uuid())
        .bind(action.code())
        .bind(granted_by.into_uuid())
        .bind(now)
        .execute(&mut *self.connection)
        .await
        .map_err(map_sqlx)?;
        Ok(())
    }

    pub(super) async fn human_credential(
        &mut self,
        email_normalized: &str,
    ) -> Result<Option<(AuthenticatedHuman, String)>, ApplicationError> {
        let row = sqlx::query(
            "SELECT id,display_name,email_normalized,password_hash FROM users \
             WHERE email_normalized=$1 AND disabled_at IS NULL",
        )
        .bind(email_normalized)
        .fetch_optional(&mut *self.connection)
        .await
        .map_err(map_sqlx)?;
        Ok(row.map(|row| {
            (
                AuthenticatedHuman {
                    user_id: row.get("id"),
                    display_name: row.get("display_name"),
                    email_normalized: row.get("email_normalized"),
                },
                row.get("password_hash"),
            )
        }))
    }

    pub(super) async fn pairing_by_code_for_update(
        &mut self,
        pairing_id: Uuid,
        code_hash: &str,
    ) -> Result<Option<Pairing>, ApplicationError> {
        let row =
            sqlx::query("SELECT * FROM computer_pairings WHERE id=$1 AND code_hash=$2 FOR UPDATE")
                .bind(pairing_id)
                .bind(code_hash)
                .fetch_optional(&mut *self.connection)
                .await
                .map_err(map_sqlx)?;
        row.map(|row| pairing_from_row(&row)).transpose()
    }

    pub(super) async fn delete_browser_session(
        &mut self,
        token_hash: &str,
    ) -> Result<(), ApplicationError> {
        sqlx::query("DELETE FROM browser_sessions WHERE token_hash=$1")
            .bind(token_hash)
            .execute(&mut *self.connection)
            .await
            .map_err(map_sqlx)?;
        Ok(())
    }

    pub(super) async fn revoke_permission(
        &mut self,
        target: MemberId,
        action: PermissionAction,
    ) -> Result<(), ApplicationError> {
        sqlx::query("DELETE FROM member_permissions WHERE member_id=$1 AND action_code=$2")
            .bind(target.into_uuid())
            .bind(action.code())
            .execute(&mut *self.connection)
            .await
            .map_err(map_sqlx)?;
        Ok(())
    }

    pub(super) async fn computer(&mut self, id: ComputerId) -> Result<Computer, ApplicationError> {
        let row = sqlx::query(
            "SELECT id, space_id, connection_status, token_hash, deleted_at FROM computers WHERE id = $1 FOR UPDATE",
        )
        .bind(id.into_uuid())
        .fetch_one(&mut *self.connection)
        .await
        .map_err(map_sqlx)?;
        let deleted_at: Option<time::OffsetDateTime> = row.get("deleted_at");
        Ok(Computer {
            id,
            space_id: SpaceId::from_uuid(row.get("space_id")),
            lifecycle: if deleted_at.is_some() {
                ComputerLifecycle::Deleted
            } else if row.get::<&str, _>("connection_status") == "online" {
                ComputerLifecycle::Online
            } else {
                ComputerLifecycle::Offline
            },
            token_hash: row.get("token_hash"),
            deleted_at,
        })
    }

    pub(super) async fn paired_computer(
        &mut self,
        computer_id: ComputerId,
    ) -> Result<Option<PairedComputer>, ApplicationError> {
        let row = sqlx::query(
            "SELECT id,space_id,name,hostname,os,connection_status,daemon_version,\
             last_seen_at,created_at,deleted_at FROM computers WHERE id=$1",
        )
        .bind(computer_id.into_uuid())
        .fetch_optional(&mut *self.connection)
        .await
        .map_err(map_sqlx)?;
        row.map(|row| paired_computer_from_row(&row)).transpose()
    }

    pub(super) async fn agent(&mut self, id: MemberId) -> Result<Agent, ApplicationError> {
        let row = sqlx::query("SELECT * FROM agents WHERE member_id = $1 FOR UPDATE")
            .bind(id.into_uuid())
            .fetch_one(&mut *self.connection)
            .await
            .map_err(map_sqlx)?;
        Ok(Agent {
            member_id: id,
            space_id: SpaceId::from_uuid(row.get("space_id")),
            computer_id: row
                .get::<Option<Uuid>, _>("computer_id")
                .map(ComputerId::from_uuid),
            role_text: row.get("role_text"),
            role_revision: u64::try_from(row.get::<i64, _>("role_revision"))
                .map_err(|_| ApplicationError::Internal)?,
            lifecycle: agent_lifecycle_from_str(row.get("lifecycle"))?,
            driver_kind: driver_kind_from_str(row.get("driver_kind"))?,
            retired_at: row.get("retired_at"),
        })
    }

    pub(super) async fn insert_human_member(
        &mut self,
        record: &HumanMemberRecord,
    ) -> Result<(), ApplicationError> {
        sqlx::query(
            "INSERT INTO members(id,space_id,kind,display_name,handle,access_level,created_at) \
             VALUES($1,$2,'human',$3,$4,'member',$5)",
        )
        .bind(record.member_id.into_uuid())
        .bind(record.space_id.into_uuid())
        .bind(&record.display_name)
        .bind(&record.handle)
        .bind(record.created_at)
        .execute(&mut *self.connection)
        .await
        .map_err(map_sqlx)?;
        sqlx::query("INSERT INTO human_members(member_id,space_id,user_id) VALUES($1,$2,$3)")
            .bind(record.member_id.into_uuid())
            .bind(record.space_id.into_uuid())
            .bind(record.user_id)
            .execute(&mut *self.connection)
            .await
            .map_err(map_sqlx)?;
        sqlx::query(
            "INSERT INTO channel_members(channel_id,space_id,member_id,joined_at) \
             SELECT id,space_id,$2,$3 FROM channels WHERE space_id=$1 AND slug='general'",
        )
        .bind(record.space_id.into_uuid())
        .bind(record.member_id.into_uuid())
        .bind(record.created_at)
        .execute(&mut *self.connection)
        .await
        .map_err(map_sqlx)?;
        sqlx::query(
            "INSERT INTO audit_events(id,space_id,actor_member_id,action,subject_type,subject_id,created_at) \
             VALUES($1,$2,$3,'space.member.joined','member',$3,$4)",
        )
        .bind(Uuid::now_v7())
        .bind(record.space_id.into_uuid())
        .bind(record.member_id.into_uuid())
        .bind(record.created_at)
        .execute(&mut *self.connection)
        .await
        .map_err(map_sqlx)?;
        Ok(())
    }

    pub(super) async fn queue_agent_configuration(
        &mut self,
        agent: &Agent,
    ) -> Result<(), ApplicationError> {
        let Some(computer_id) = agent.computer_id else {
            return Ok(());
        };
        let configuration = self.agent_configuration(agent.member_id).await?;
        self.queue_command(computer_id, Command::AgentConfigure(configuration))
            .await
    }

    pub(super) async fn has_permission(
        &mut self,
        actor: MemberId,
        action: PermissionAction,
    ) -> Result<bool, ApplicationError> {
        sqlx::query_scalar::<_, bool>(
            "SELECT EXISTS(SELECT 1 FROM member_permissions \
             WHERE member_id = $1 AND action_code = $2)",
        )
        .bind(actor.into_uuid())
        .bind(permission_str(action))
        .fetch_one(&mut *self.connection)
        .await
        .map_err(map_sqlx)
    }

    pub(super) async fn space_of_attachment(
        &mut self,
        attachment_id: AttachmentId,
    ) -> Result<Option<SpaceId>, ApplicationError> {
        let space_id =
            sqlx::query_scalar::<_, Uuid>("SELECT space_id FROM attachments WHERE id=$1")
                .bind(attachment_id.into_uuid())
                .fetch_optional(&mut *self.connection)
                .await
                .map_err(map_sqlx)?;
        Ok(space_id.map(SpaceId::from_uuid))
    }

    pub(super) async fn computer_accepts_agent(
        &mut self,
        computer_id: ComputerId,
        space_id: SpaceId,
    ) -> Result<bool, ApplicationError> {
        sqlx::query_scalar(
            "SELECT EXISTS(SELECT 1 FROM computers WHERE id=$1 AND space_id=$2 \
             AND deleted_at IS NULL AND connection_status='online')",
        )
        .bind(computer_id.into_uuid())
        .bind(space_id.into_uuid())
        .fetch_one(&mut *self.connection)
        .await
        .map_err(map_sqlx)
    }

    pub(super) async fn computer_for_token(
        &mut self,
        computer_id: ComputerId,
        token_hash: &str,
    ) -> Result<Option<bool>, ApplicationError> {
        let deleted = sqlx::query_scalar::<_, Option<OffsetDateTime>>(
            "SELECT deleted_at FROM computers WHERE id=$1 AND token_hash=$2",
        )
        .bind(computer_id.into_uuid())
        .bind(token_hash)
        .fetch_optional(&mut *self.connection)
        .await
        .map_err(map_sqlx)?;
        Ok(deleted.map(|deleted_at| deleted_at.is_some()))
    }

    pub(super) async fn insert_human(
        &mut self,
        user_id: Uuid,
        registration: &HumanRegistration,
        password_hash: &str,
        now: OffsetDateTime,
    ) -> Result<(), ApplicationError> {
        sqlx::query(
            "INSERT INTO users(id,email_normalized,password_hash,display_name,created_at) \
             VALUES($1,$2,$3,$4,$5)",
        )
        .bind(user_id)
        .bind(&registration.email_normalized)
        .bind(password_hash)
        .bind(&registration.display_name)
        .bind(now)
        .execute(&mut *self.connection)
        .await
        .map_err(map_sqlx)?;
        Ok(())
    }

    pub(super) async fn insert_computer(
        &mut self,
        record: &ComputerRecord,
    ) -> Result<(), ApplicationError> {
        sqlx::query(
            "INSERT INTO computers\
             (id,space_id,name,hostname,os,token_hash,connection_status,daemon_version,created_at) \
             VALUES($1,$2,$3,$4,$5,$6,'offline',$7,$8)",
        )
        .bind(record.id.into_uuid())
        .bind(record.space_id.into_uuid())
        .bind(&record.name)
        .bind(&record.hostname)
        .bind(record.os.code())
        .bind(&record.token_hash)
        .bind(&record.daemon_version)
        .bind(record.created_at)
        .execute(&mut *self.connection)
        .await
        .map_err(map_sqlx)?;
        Ok(())
    }

    pub(super) async fn insert_pairing(
        &mut self,
        pairing_id: Uuid,
        pairing: &Pairing,
        code_hash: &str,
        now: OffsetDateTime,
    ) -> Result<(), ApplicationError> {
        sqlx::query(
            "INSERT INTO computer_pairings\
             (id,code_hash,token_hash,hostname,os,daemon_version,status,expires_at,created_at) \
             VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9)",
        )
        .bind(pairing_id)
        .bind(code_hash)
        .bind(&pairing.request.token_hash)
        .bind(&pairing.request.hostname)
        .bind(pairing.request.os.code())
        .bind(&pairing.request.daemon_version)
        .bind(pairing.status.code())
        .bind(pairing.expires_at)
        .bind(now)
        .execute(&mut *self.connection)
        .await
        .map_err(map_sqlx)?;
        Ok(())
    }

    pub(super) async fn save_computer(
        &mut self,
        computer: Computer,
    ) -> Result<(), ApplicationError> {
        let changed = sqlx::query(
            "UPDATE computers SET token_hash=$2,deleted_at=$3,connection_status='offline' WHERE id=$1",
        )
        .bind(computer.id.into_uuid())
        .bind(&computer.token_hash)
        .bind(computer.deleted_at)
        .execute(&mut *self.connection)
        .await
        .map_err(map_sqlx)?;
        if changed.rows_affected() == 1 {
            Ok(())
        } else {
            Err(ApplicationError::NotFound)
        }
    }

    pub(super) async fn save_pairing(
        &mut self,
        pairing_id: Uuid,
        pairing: &Pairing,
        now: OffsetDateTime,
    ) -> Result<(), ApplicationError> {
        sqlx::query(
            "UPDATE computer_pairings SET status=$2,computer_id=$3,space_id=$4,\
             confirmed_at=CASE WHEN $2='confirmed' THEN $5 ELSE confirmed_at END WHERE id=$1",
        )
        .bind(pairing_id)
        .bind(pairing.status.code())
        .bind(pairing.computer_id.map(ComputerId::into_uuid))
        .bind(pairing.space_id.map(SpaceId::into_uuid))
        .bind(now)
        .execute(&mut *self.connection)
        .await
        .map_err(map_sqlx)?;
        Ok(())
    }

    pub(super) async fn invitation_by_token(
        &mut self,
        token_hash: &str,
    ) -> Result<Option<(Uuid, Invitation)>, ApplicationError> {
        let row = sqlx::query("SELECT * FROM space_invitations WHERE token_hash=$1")
            .bind(token_hash)
            .fetch_optional(&mut *self.connection)
            .await
            .map_err(map_sqlx)?;
        row.map(|row| invitation_from_row(&row)).transpose()
    }

    pub(super) async fn save_agent(&mut self, agent: Agent) -> Result<(), ApplicationError> {
        let changed = sqlx::query(
            "UPDATE agents SET computer_id=$2,role_text=$3,role_revision=$4,lifecycle=$5,retired_at=$6 \
             WHERE member_id=$1",
        )
        .bind(agent.member_id.into_uuid())
        .bind(agent.computer_id.map(ComputerId::into_uuid))
        .bind(&agent.role_text)
        .bind(i64::try_from(agent.role_revision).map_err(|_| ApplicationError::Conflict)?)
        .bind(agent_lifecycle_str(agent.lifecycle))
        .bind(agent.retired_at)
        .execute(&mut *self.connection)
        .await
        .map_err(map_sqlx)?;
        if changed.rows_affected() == 1 {
            sqlx::query("UPDATE members SET retired_at=$2 WHERE id=$1")
                .bind(agent.member_id.into_uuid())
                .bind(agent.retired_at)
                .execute(&mut *self.connection)
                .await
                .map_err(map_sqlx)?;
            Ok(())
        } else {
            Err(ApplicationError::NotFound)
        }
    }

    pub(super) async fn human_for_session(
        &mut self,
        token_hash: &str,
        now: OffsetDateTime,
    ) -> Result<Option<AuthenticatedHuman>, ApplicationError> {
        let row = sqlx::query(
            "SELECT u.id,u.display_name,u.email_normalized \
             FROM browser_sessions s JOIN users u ON u.id=s.user_id \
             WHERE s.token_hash=$1 AND s.expires_at>$2 AND u.disabled_at IS NULL",
        )
        .bind(token_hash)
        .bind(now)
        .fetch_optional(&mut *self.connection)
        .await
        .map_err(map_sqlx)?;
        Ok(row.map(|row| AuthenticatedHuman {
            user_id: row.get("id"),
            display_name: row.get("display_name"),
            email_normalized: row.get("email_normalized"),
        }))
    }

    pub(super) async fn invitation_by_token_for_update(
        &mut self,
        token_hash: &str,
    ) -> Result<Option<(Uuid, Invitation)>, ApplicationError> {
        let row = sqlx::query("SELECT * FROM space_invitations WHERE token_hash=$1 FOR UPDATE")
            .bind(token_hash)
            .fetch_optional(&mut *self.connection)
            .await
            .map_err(map_sqlx)?;
        row.map(|row| invitation_from_row(&row)).transpose()
    }

    pub(super) async fn can_manage_permissions(
        &mut self,
        actor: MemberId,
        target: MemberId,
    ) -> Result<bool, ApplicationError> {
        sqlx::query_scalar(
            "SELECT EXISTS(\
               SELECT 1 FROM members actor JOIN members target ON target.space_id=actor.space_id \
               WHERE actor.id=$1 AND target.id=$2 AND actor.kind='human' \
                 AND actor.access_level IN ('owner','admin') AND actor.retired_at IS NULL \
                 AND target.retired_at IS NULL\
             )",
        )
        .bind(actor.into_uuid())
        .bind(target.into_uuid())
        .fetch_one(&mut *self.connection)
        .await
        .map_err(map_sqlx)
    }

    pub(super) async fn space_computers(
        &mut self,
        space_id: SpaceId,
    ) -> Result<Vec<PairedComputer>, ApplicationError> {
        let rows = sqlx::query(
            "SELECT id,space_id,name,hostname,os,connection_status,daemon_version,\
             last_seen_at,created_at,deleted_at FROM computers WHERE space_id=$1 ORDER BY created_at",
        )
        .bind(space_id.into_uuid())
        .fetch_all(&mut *self.connection)
        .await
        .map_err(map_sqlx)?;
        rows.iter().map(paired_computer_from_row).collect()
    }

    pub(super) async fn insert_browser_session(
        &mut self,
        session_id: Uuid,
        user_id: Uuid,
        token_hash: &str,
        expires_at: OffsetDateTime,
        now: OffsetDateTime,
    ) -> Result<(), ApplicationError> {
        sqlx::query(
            "INSERT INTO browser_sessions(id,user_id,token_hash,expires_at,last_seen_at,created_at) \
             VALUES($1,$2,$3,$4,$5,$5)",
        )
        .bind(session_id)
        .bind(user_id)
        .bind(token_hash)
        .bind(expires_at)
        .bind(now)
        .execute(&mut *self.connection)
        .await
        .map_err(map_sqlx)?;
        Ok(())
    }

    pub(super) async fn member_permissions(
        &mut self,
        member_id: Uuid,
    ) -> Result<Vec<PermissionAction>, ApplicationError> {
        sqlx::query_scalar::<_, String>(
            "SELECT action_code FROM member_permissions WHERE member_id=$1 ORDER BY action_code",
        )
        .bind(member_id)
        .fetch_all(&mut *self.connection)
        .await
        .map_err(map_sqlx)?
        .iter()
        .map(|code| permission_from_str(code))
        .collect()
    }

    pub(super) async fn space_member(
        &mut self,
        member_id: MemberId,
        space_id: SpaceId,
    ) -> Result<Option<SpaceMemberView>, ApplicationError> {
        let row = sqlx::query(
            "SELECT id,kind,display_name,handle,access_level FROM members \
             WHERE id=$1 AND space_id=$2 AND retired_at IS NULL",
        )
        .bind(member_id.into_uuid())
        .bind(space_id.into_uuid())
        .fetch_optional(&mut *self.connection)
        .await
        .map_err(map_sqlx)?;
        match row {
            Some(row) => {
                let permissions = self.member_permissions(row.get("id")).await?;
                Ok(Some(space_member_from_row(&row, space_id, permissions)?))
            }
            None => Ok(None),
        }
    }
}
#[async_trait]
impl IdentityTransaction for PostgresTransaction {
    async fn create_space(
        &mut self,
        actor_user_id: uuid::Uuid,
        space_id: SpaceId,
        owner_id: MemberId,
        general_channel_id: ChannelId,
        name: &str,
        slug: &str,
        accent: &str,
        owner_handle: &str,
        owner_display_name: &str,
        idempotency_key: IdempotencyKey,
        now: time::OffsetDateTime,
    ) -> Result<CreatedSpace, ApplicationError> {
        self.create_space(
            actor_user_id,
            space_id,
            owner_id,
            general_channel_id,
            name,
            slug,
            accent,
            owner_handle,
            owner_display_name,
            idempotency_key,
            now,
        )
        .await
    }
    async fn insert_human(
        &mut self,
        user_id: uuid::Uuid,
        registration: &HumanRegistration,
        password_hash: &str,
        now: time::OffsetDateTime,
    ) -> Result<(), ApplicationError> {
        self.insert_human(user_id, registration, password_hash, now)
            .await
    }
    async fn human_credential(
        &mut self,
        email_normalized: &str,
    ) -> Result<Option<(AuthenticatedHuman, String)>, ApplicationError> {
        self.human_credential(email_normalized).await
    }
    async fn insert_browser_session(
        &mut self,
        session_id: uuid::Uuid,
        user_id: uuid::Uuid,
        token_hash: &str,
        expires_at: time::OffsetDateTime,
        now: time::OffsetDateTime,
    ) -> Result<(), ApplicationError> {
        self.insert_browser_session(session_id, user_id, token_hash, expires_at, now)
            .await
    }
    async fn human_for_session(
        &mut self,
        token_hash: &str,
        now: time::OffsetDateTime,
    ) -> Result<Option<AuthenticatedHuman>, ApplicationError> {
        self.human_for_session(token_hash, now).await
    }
    async fn delete_browser_session(&mut self, token_hash: &str) -> Result<(), ApplicationError> {
        self.delete_browser_session(token_hash).await
    }
    async fn space_access(
        &mut self,
        user_id: uuid::Uuid,
        space_id: SpaceId,
    ) -> Result<Option<SpaceAccess>, ApplicationError> {
        self.space_access(user_id, space_id).await
    }
    async fn space_of_agent(
        &mut self,
        agent_id: MemberId,
    ) -> Result<Option<SpaceId>, ApplicationError> {
        self.space_of_agent(agent_id).await
    }
    async fn space_of_computer(
        &mut self,
        computer_id: ComputerId,
    ) -> Result<Option<SpaceId>, ApplicationError> {
        self.space_of_computer(computer_id).await
    }
    async fn insert_pairing(
        &mut self,
        pairing_id: uuid::Uuid,
        pairing: &Pairing,
        code_hash: &str,
        now: time::OffsetDateTime,
    ) -> Result<(), ApplicationError> {
        self.insert_pairing(pairing_id, pairing, code_hash, now)
            .await
    }
    async fn save_pairing(
        &mut self,
        pairing_id: uuid::Uuid,
        pairing: &Pairing,
        now: time::OffsetDateTime,
    ) -> Result<(), ApplicationError> {
        self.save_pairing(pairing_id, pairing, now).await
    }
    async fn pairing_by_code(
        &mut self,
        pairing_id: uuid::Uuid,
        code_hash: &str,
    ) -> Result<Option<Pairing>, ApplicationError> {
        self.pairing_by_code(pairing_id, code_hash).await
    }
    async fn pairing_by_code_for_update(
        &mut self,
        pairing_id: uuid::Uuid,
        code_hash: &str,
    ) -> Result<Option<Pairing>, ApplicationError> {
        self.pairing_by_code_for_update(pairing_id, code_hash).await
    }
    async fn pairing_by_token(
        &mut self,
        pairing_id: uuid::Uuid,
        token_hash: &str,
    ) -> Result<Option<Pairing>, ApplicationError> {
        self.pairing_by_token(pairing_id, token_hash).await
    }
    async fn insert_invitation(
        &mut self,
        invitation_id: uuid::Uuid,
        invitation: &Invitation,
        now: time::OffsetDateTime,
    ) -> Result<(), ApplicationError> {
        self.insert_invitation(invitation_id, invitation, now).await
    }
    async fn save_invitation(
        &mut self,
        invitation_id: uuid::Uuid,
        invitation: &Invitation,
    ) -> Result<(), ApplicationError> {
        self.save_invitation(invitation_id, invitation).await
    }
    async fn invitation_by_token(
        &mut self,
        token_hash: &str,
    ) -> Result<Option<(uuid::Uuid, Invitation)>, ApplicationError> {
        self.invitation_by_token(token_hash).await
    }
    async fn invitation_by_token_for_update(
        &mut self,
        token_hash: &str,
    ) -> Result<Option<(uuid::Uuid, Invitation)>, ApplicationError> {
        self.invitation_by_token_for_update(token_hash).await
    }
    async fn space_identity(
        &mut self,
        space_id: SpaceId,
    ) -> Result<Option<(String, String)>, ApplicationError> {
        self.space_identity(space_id).await
    }
    async fn insert_human_member(
        &mut self,
        record: &HumanMemberRecord,
    ) -> Result<(), ApplicationError> {
        self.insert_human_member(record).await
    }
    async fn space_human_member(
        &mut self,
        user_id: uuid::Uuid,
        space_id: SpaceId,
    ) -> Result<Option<SpaceHumanMember>, ApplicationError> {
        self.space_human_member(user_id, space_id).await
    }
    async fn space_of_member(
        &mut self,
        member_id: MemberId,
    ) -> Result<Option<SpaceId>, ApplicationError> {
        self.space_of_member(member_id).await
    }
    async fn member(&mut self, member_id: MemberId) -> Result<Member, ApplicationError> {
        self.member(member_id).await
    }
    async fn save_member(&mut self, member: Member) -> Result<(), ApplicationError> {
        self.save_member(member).await
    }
    async fn insert_computer(&mut self, record: &ComputerRecord) -> Result<(), ApplicationError> {
        self.insert_computer(record).await
    }
    async fn paired_computer(
        &mut self,
        computer_id: ComputerId,
    ) -> Result<Option<PairedComputer>, ApplicationError> {
        self.paired_computer(computer_id).await
    }
    async fn space_computers(
        &mut self,
        space_id: SpaceId,
    ) -> Result<Vec<PairedComputer>, ApplicationError> {
        self.space_computers(space_id).await
    }
    async fn computer_for_token(
        &mut self,
        computer_id: ComputerId,
        token_hash: &str,
    ) -> Result<Option<bool>, ApplicationError> {
        self.computer_for_token(computer_id, token_hash).await
    }
    async fn agent(&mut self, id: MemberId) -> Result<Agent, ApplicationError> {
        self.agent(id).await
    }
    async fn computer(&mut self, id: ComputerId) -> Result<Computer, ApplicationError> {
        self.computer(id).await
    }
    async fn computer_has_assigned_agents(
        &mut self,
        computer_id: ComputerId,
    ) -> Result<bool, ApplicationError> {
        self.computer_has_assigned_agents(computer_id).await
    }
    async fn has_permission(
        &mut self,
        actor: MemberId,
        action: PermissionAction,
    ) -> Result<bool, ApplicationError> {
        self.has_permission(actor, action).await
    }
    async fn can_manage_permissions(
        &mut self,
        actor: MemberId,
        target: MemberId,
    ) -> Result<bool, ApplicationError> {
        self.can_manage_permissions(actor, target).await
    }
    async fn can_operate_agent(
        &mut self,
        computer_id: ComputerId,
        agent_id: MemberId,
    ) -> Result<bool, ApplicationError> {
        self.can_operate_agent(computer_id, agent_id).await
    }
    async fn member_access_level(
        &mut self,
        member_id: MemberId,
        space_id: SpaceId,
    ) -> Result<AccessLevel, ApplicationError> {
        self.member_access_level(member_id, space_id).await
    }
    async fn computer_accepts_agent(
        &mut self,
        computer_id: ComputerId,
        space_id: SpaceId,
    ) -> Result<bool, ApplicationError> {
        self.computer_accepts_agent(computer_id, space_id).await
    }
    async fn grant_permission(
        &mut self,
        target: MemberId,
        action: PermissionAction,
        granted_by: MemberId,
        now: time::OffsetDateTime,
    ) -> Result<(), ApplicationError> {
        self.grant_permission(target, action, granted_by, now).await
    }
    async fn revoke_permission(
        &mut self,
        target: MemberId,
        action: PermissionAction,
    ) -> Result<(), ApplicationError> {
        self.revoke_permission(target, action).await
    }
    async fn insert_agent(&mut self, member: Member, agent: Agent) -> Result<(), ApplicationError> {
        self.insert_agent(member, agent).await
    }
    async fn save_agent(&mut self, agent: Agent) -> Result<(), ApplicationError> {
        self.save_agent(agent).await
    }
    async fn save_computer(&mut self, computer: Computer) -> Result<(), ApplicationError> {
        self.save_computer(computer).await
    }
}
