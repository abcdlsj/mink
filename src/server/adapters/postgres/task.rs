use super::*;

impl PostgresTransaction {
    pub(super) async fn insert_task(&mut self, task: Task) -> Result<(), ApplicationError> {
        let task_snapshot = task.snapshot();
        sqlx::query(
            "INSERT INTO tasks (id, space_id, title, status, source_thread_id, creator_member_id, \
             assignee_agent_member_id, result_message_id, close_reason_code, close_reason_note, \
             created_at, updated_at, finished_at) \
             VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13)",
        )
        .bind(task_snapshot.id.into_uuid())
        .bind(task_snapshot.space_id.into_uuid())
        .bind(&task_snapshot.title)
        .bind(task_status_str(task_snapshot.status))
        .bind(task_snapshot.source_thread_id.into_uuid())
        .bind(task_snapshot.creator_member_id.into_uuid())
        .bind(
            task_snapshot
                .assignee_agent_member_id
                .map(MemberId::into_uuid),
        )
        .bind(task_snapshot.result_message_id.map(MessageId::into_uuid))
        .bind(task_snapshot.close_reason.map(close_reason_str))
        .bind(&task_snapshot.close_reason_note)
        .bind(task_snapshot.created_at)
        .bind(task_snapshot.updated_at)
        .bind(task_snapshot.finished_at)
        .execute(&mut *self.connection)
        .await
        .map_err(map_sqlx)?;
        self.replace_task_links(&task).await
    }

    pub(super) async fn record_task_audit(
        &mut self,
        actor: MemberId,
        action: &str,
        task_id: TaskId,
        now: time::OffsetDateTime,
    ) -> Result<(), ApplicationError> {
        let space_id = self.space_for_task(task_id).await?;
        sqlx::query(
            "INSERT INTO audit_events (id,space_id,actor_member_id,action,subject_type,subject_id,metadata_json,created_at) \
             VALUES ($1,$2,$3,$4,'task',$5,'{}',$6)",
        )
        .bind(Uuid::now_v7())
        .bind(space_id.into_uuid())
        .bind(actor.into_uuid())
        .bind(action)
        .bind(task_id.into_uuid())
        .bind(now)
        .execute(&mut *self.connection)
        .await
        .map_err(map_sqlx)?;
        Ok(())
    }

    pub(super) async fn can_assign_agent(
        &mut self,
        agent: MemberId,
        source: &Thread,
    ) -> Result<bool, ApplicationError> {
        let active = sqlx::query_scalar::<_, bool>(
            "SELECT EXISTS(SELECT 1 FROM agents WHERE member_id = $1 \
             AND space_id = $2 AND lifecycle = 'active')",
        )
        .bind(agent.into_uuid())
        .bind(source.space_id.into_uuid())
        .fetch_one(&mut *self.connection)
        .await
        .map_err(map_sqlx)?;
        Ok(active && source.audience.contains(&agent))
    }

    pub(super) async fn task(&mut self, id: TaskId) -> Result<Task, ApplicationError> {
        let row = sqlx::query("SELECT * FROM tasks WHERE id = $1 FOR UPDATE")
            .bind(id.into_uuid())
            .fetch_one(&mut *self.connection)
            .await
            .map_err(map_sqlx)?;
        let link_rows = sqlx::query(
            "SELECT thread_id, linked_by_member_id, linked_at FROM task_threads \
             WHERE task_id = $1 ORDER BY linked_at, thread_id",
        )
        .bind(id.into_uuid())
        .fetch_all(&mut *self.connection)
        .await
        .map_err(map_sqlx)?;
        let related_threads = link_rows
            .into_iter()
            .map(|link| RelatedThreadSnapshot {
                thread_id: ThreadId::from_uuid(link.get("thread_id")),
                linked_by_member_id: MemberId::from_uuid(link.get("linked_by_member_id")),
                linked_at: link.get("linked_at"),
            })
            .collect();
        task_from_row(&row, related_threads)
    }

    pub(super) async fn task_for_source(
        &mut self,
        thread_id: ThreadId,
    ) -> Result<Option<TaskId>, ApplicationError> {
        optional_uuid(
            &mut self.connection,
            "SELECT id FROM tasks WHERE source_thread_id = $1",
            thread_id.into_uuid(),
        )
        .await
        .map(|value| value.map(TaskId::from_uuid))
    }

    pub(super) async fn save_task(&mut self, task: Task) -> Result<(), ApplicationError> {
        let task_snapshot = task.snapshot();
        let changed = sqlx::query(
            "UPDATE tasks SET title=$2,status=$3,assignee_agent_member_id=$4,result_message_id=$5, \
             close_reason_code=$6,close_reason_note=$7,updated_at=$8,finished_at=$9 \
             WHERE id=$1 AND source_thread_id=$10",
        )
        .bind(task_snapshot.id.into_uuid())
        .bind(&task_snapshot.title)
        .bind(task_status_str(task_snapshot.status))
        .bind(
            task_snapshot
                .assignee_agent_member_id
                .map(MemberId::into_uuid),
        )
        .bind(task_snapshot.result_message_id.map(MessageId::into_uuid))
        .bind(task_snapshot.close_reason.map(close_reason_str))
        .bind(&task_snapshot.close_reason_note)
        .bind(task_snapshot.updated_at)
        .bind(task_snapshot.finished_at)
        .bind(task_snapshot.source_thread_id.into_uuid())
        .execute(&mut *self.connection)
        .await
        .map_err(map_sqlx)?;
        if changed.rows_affected() != 1 {
            return Err(ApplicationError::NotFound);
        }
        self.replace_task_links(&task).await
    }

    pub(super) async fn task_for_idempotency(
        &mut self,
        actor: MemberId,
        action: &str,
        key: IdempotencyKey,
    ) -> Result<Option<TaskId>, ApplicationError> {
        sqlx::query("SELECT pg_advisory_xact_lock(hashtextextended($1, 0))")
            .bind(format!("{}:{action}:{}", actor, key))
            .execute(&mut *self.connection)
            .await
            .map_err(map_sqlx)?;
        let value = sqlx::query_scalar::<_, Uuid>(
            "SELECT resource_id FROM idempotency_records \
             WHERE actor_member_id = $1 AND action = $2 AND idempotency_key = $3",
        )
        .bind(actor.into_uuid())
        .bind(action)
        .bind(key.into_uuid())
        .fetch_optional(&mut *self.connection)
        .await
        .map_err(map_sqlx)?;
        Ok(value.map(TaskId::from_uuid))
    }

    pub(super) async fn record_task_idempotency(
        &mut self,
        actor: MemberId,
        action: &str,
        key: IdempotencyKey,
        task_id: TaskId,
    ) -> Result<(), ApplicationError> {
        let hash = Sha256::digest(task_id.into_uuid().as_bytes());
        let response_code = if action == "task.create" {
            "created"
        } else {
            "ok"
        };
        sqlx::query(
            "INSERT INTO idempotency_records (actor_member_id,action,idempotency_key,response_code, \
             resource_id,result_hash,created_at) VALUES ($1,$2,$3,$4,$5,$6,now())",
        )
        .bind(actor.into_uuid())
        .bind(action)
        .bind(key.into_uuid())
        .bind(response_code)
        .bind(task_id.into_uuid())
        .bind(hash.as_slice())
        .execute(&mut *self.connection)
        .await
        .map_err(map_sqlx)?;
        Ok(())
    }

    pub(super) async fn can_govern_task(
        &mut self,
        actor: MemberId,
        task: &Task,
    ) -> Result<bool, ApplicationError> {
        sqlx::query_scalar::<_, bool>(
            "SELECT EXISTS(SELECT 1 FROM members WHERE id = $1 AND space_id = $2 \
             AND kind = 'human' AND access_level IN ('owner', 'admin'))",
        )
        .bind(actor.into_uuid())
        .bind(task.view().space_id.into_uuid())
        .fetch_one(&mut *self.connection)
        .await
        .map_err(map_sqlx)
    }

    pub(super) async fn unfinished_task_for_thread(
        &mut self,
        thread_id: ThreadId,
    ) -> Result<Option<TaskId>, ApplicationError> {
        let value = sqlx::query_scalar::<_, Uuid>(
            "SELECT id FROM tasks WHERE status NOT IN ('done', 'closed') AND source_thread_id = $1 \
             UNION ALL \
             SELECT task_id FROM task_threads JOIN tasks ON tasks.id = task_threads.task_id \
             WHERE task_threads.thread_id = $1 AND tasks.status NOT IN ('done', 'closed') LIMIT 1",
        )
        .bind(thread_id.into_uuid())
        .fetch_optional(&mut *self.connection)
        .await
        .map_err(map_sqlx)?;
        Ok(value.map(TaskId::from_uuid))
    }

    pub(super) async fn can_link_thread(
        &mut self,
        actor: MemberId,
        task: &Task,
        target: &Thread,
    ) -> Result<bool, ApplicationError> {
        Ok(self
            .can_read_thread(actor, task.view().source_thread_id)
            .await?
            && target.audience.contains(&actor))
    }

    pub(super) async fn replace_task_links(&mut self, task: &Task) -> Result<(), ApplicationError> {
        sqlx::query("DELETE FROM task_threads WHERE task_id=$1")
            .bind(task.view().id.into_uuid())
            .execute(&mut *self.connection)
            .await
            .map_err(map_sqlx)?;
        for link in task.related_threads() {
            sqlx::query(
                "INSERT INTO task_threads (task_id,thread_id,space_id,linked_by_member_id,linked_at) \
                 VALUES ($1,$2,$3,$4,$5)",
            )
            .bind(task.view().id.into_uuid())
            .bind(link.thread_id.into_uuid())
            .bind(task.view().space_id.into_uuid())
            .bind(link.linked_by_member_id.into_uuid())
            .bind(link.linked_at)
            .execute(&mut *self.connection)
            .await
            .map_err(map_sqlx)?;
        }
        Ok(())
    }

    pub(super) async fn task_assignment(
        &mut self,
        task_id: TaskId,
    ) -> Result<Option<(MemberId, ComputerId)>, ApplicationError> {
        let row = sqlx::query(
            "SELECT agents.member_id,agents.computer_id FROM tasks \
             JOIN agents ON agents.member_id=tasks.assignee_agent_member_id WHERE tasks.id=$1",
        )
        .bind(task_id.into_uuid())
        .fetch_optional(&mut *self.connection)
        .await
        .map_err(map_sqlx)?;
        Ok(row.map(|row| {
            (
                MemberId::from_uuid(row.get("member_id")),
                ComputerId::from_uuid(row.get("computer_id")),
            )
        }))
    }

    pub(super) async fn task_snapshot(
        &mut self,
        task_id: TaskId,
    ) -> Result<TaskSnapshot, ApplicationError> {
        let row = sqlx::query(
            "SELECT title,status,source_thread_id,result_message_id FROM tasks WHERE id=$1",
        )
        .bind(task_id.into_uuid())
        .fetch_one(&mut *self.connection)
        .await
        .map_err(map_sqlx)?;
        let source_thread_id = ThreadId::from_uuid(row.get("source_thread_id"));
        let mut linked_thread_ids = vec![source_thread_id];
        linked_thread_ids.extend(
            sqlx::query_scalar::<_, Uuid>(
                "SELECT thread_id FROM task_threads WHERE task_id=$1 ORDER BY linked_at,thread_id",
            )
            .bind(task_id.into_uuid())
            .fetch_all(&mut *self.connection)
            .await
            .map_err(map_sqlx)?
            .into_iter()
            .map(ThreadId::from_uuid),
        );
        Ok(TaskSnapshot {
            task_id,
            title: row.get("title"),
            status: wire_task_status(row.get("status"))?,
            source_thread_id,
            linked_thread_ids,
            result_message_id: row
                .get::<Option<Uuid>, _>("result_message_id")
                .map(MessageId::from_uuid),
        })
    }
}
#[async_trait]
impl TaskTransaction for PostgresTransaction {
    async fn task(&mut self, id: TaskId) -> Result<Task, ApplicationError> {
        self.task(id).await
    }
    async fn task_for_source(
        &mut self,
        thread_id: ThreadId,
    ) -> Result<Option<TaskId>, ApplicationError> {
        self.task_for_source(thread_id).await
    }
    async fn unfinished_task_for_thread(
        &mut self,
        thread_id: ThreadId,
    ) -> Result<Option<TaskId>, ApplicationError> {
        self.unfinished_task_for_thread(thread_id).await
    }
    async fn task_for_idempotency(
        &mut self,
        actor: MemberId,
        action: &str,
        key: IdempotencyKey,
    ) -> Result<Option<TaskId>, ApplicationError> {
        self.task_for_idempotency(actor, action, key).await
    }
    async fn can_link_thread(
        &mut self,
        actor: MemberId,
        task: &Task,
        target: &Thread,
    ) -> Result<bool, ApplicationError> {
        self.can_link_thread(actor, task, target).await
    }
    async fn can_assign_agent(
        &mut self,
        agent: MemberId,
        source: &Thread,
    ) -> Result<bool, ApplicationError> {
        self.can_assign_agent(agent, source).await
    }
    async fn can_govern_task(
        &mut self,
        actor: MemberId,
        task: &Task,
    ) -> Result<bool, ApplicationError> {
        self.can_govern_task(actor, task).await
    }
    async fn insert_task(&mut self, task: Task) -> Result<(), ApplicationError> {
        self.insert_task(task).await
    }
    async fn save_task(&mut self, task: Task) -> Result<(), ApplicationError> {
        self.save_task(task).await
    }
}
