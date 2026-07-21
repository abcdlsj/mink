-- +goose Up
DROP INDEX grants_subject_capability_scope;
ALTER TABLE grant_issue_requests RENAME TO grant_issue_requests_v14;
ALTER TABLE grant_revoke_requests RENAME TO grant_revoke_requests_v14;
ALTER TABLE grants RENAME TO grants_v14;

CREATE TABLE grants (
    id TEXT PRIMARY KEY CHECK (length(id) = 36),
    organization_id TEXT NOT NULL REFERENCES organizations(id) ON DELETE RESTRICT,
    subject_kind TEXT NOT NULL CHECK (subject_kind IN ('human', 'agent')),
    subject_id TEXT NOT NULL CHECK (length(subject_id) = 36),
    issuer_kind TEXT NOT NULL CHECK (issuer_kind IN ('system', 'human', 'agent')),
    issuer_id TEXT NOT NULL,
    capability TEXT NOT NULL,
    scope_kind TEXT NOT NULL CHECK (scope_kind IN ('organization', 'agent', 'computer', 'space', 'work')),
    scope_id TEXT NOT NULL CHECK (length(scope_id) = 36),
    parent_grant_id TEXT NOT NULL DEFAULT '',
    expires_at INTEGER,
    revoked_at INTEGER,
    created_at INTEGER NOT NULL,
    updated_at INTEGER NOT NULL,
    CHECK ((issuer_kind = 'system' AND issuer_id = '') OR (issuer_kind != 'system' AND length(issuer_id) = 36)),
    CHECK (expires_at IS NULL OR expires_at > created_at),
    CHECK (revoked_at IS NULL OR revoked_at >= created_at)
);

INSERT INTO grants(
    id, organization_id, subject_kind, subject_id, issuer_kind, issuer_id,
    capability, scope_kind, scope_id, parent_grant_id, expires_at, revoked_at,
    created_at, updated_at
)
SELECT
    id, organization_id, subject_kind, subject_id, issuer_kind, issuer_id,
    capability, scope_kind, scope_id, parent_grant_id, expires_at, revoked_at,
    created_at, updated_at
FROM grants_v14;

CREATE INDEX grants_subject_capability_scope
ON grants(organization_id, subject_kind, subject_id, capability, scope_kind, scope_id);

CREATE TABLE grant_issue_requests (
    request_id TEXT PRIMARY KEY CHECK (length(request_id) = 36),
    grant_id TEXT NOT NULL UNIQUE REFERENCES grants(id) ON DELETE RESTRICT,
    payload_fingerprint BLOB NOT NULL CHECK (length(payload_fingerprint) = 32)
);

INSERT INTO grant_issue_requests(request_id, grant_id, payload_fingerprint)
SELECT request_id, grant_id, payload_fingerprint
FROM grant_issue_requests_v14;

CREATE TABLE grant_revoke_requests (
    request_id TEXT PRIMARY KEY CHECK (length(request_id) = 36),
    grant_id TEXT NOT NULL REFERENCES grants(id) ON DELETE RESTRICT,
    payload_fingerprint BLOB NOT NULL CHECK (length(payload_fingerprint) = 32)
);

INSERT INTO grant_revoke_requests(request_id, grant_id, payload_fingerprint)
SELECT request_id, grant_id, payload_fingerprint
FROM grant_revoke_requests_v14;

DROP TABLE grant_revoke_requests_v14;
DROP TABLE grant_issue_requests_v14;
DROP TABLE grants_v14;

CREATE UNIQUE INDEX spaces_id_organization
ON spaces(id, organization_id);

CREATE UNIQUE INDEX spaces_id_organization_kind
ON spaces(id, organization_id, kind);

CREATE TABLE works (
    id TEXT NOT NULL UNIQUE CHECK (length(id) = 36),
    organization_id TEXT NOT NULL REFERENCES organizations(id) ON DELETE RESTRICT,
    root_work_id TEXT NOT NULL CHECK (length(root_work_id) = 36),
    parent_work_id TEXT CHECK (parent_work_id IS NULL OR length(parent_work_id) = 36),
    source_message_id TEXT NOT NULL CHECK (length(source_message_id) = 36),
    source_space_id TEXT NOT NULL CHECK (length(source_space_id) = 36),
    source_target_kind TEXT NOT NULL CHECK (source_target_kind IN ('space', 'thread')),
    source_target_id TEXT NOT NULL CHECK (length(source_target_id) = 36),
    source_target_sequence INTEGER NOT NULL CHECK (source_target_sequence >= 1),
    team_space_id TEXT NOT NULL UNIQUE CHECK (length(team_space_id) = 36),
    team_space_kind TEXT NOT NULL DEFAULT 'group' CHECK (team_space_kind = 'group'),
    goal TEXT NOT NULL CHECK (length(goal) BETWEEN 1 AND 20000),
    state TEXT NOT NULL CHECK (state IN ('open', 'blocked', 'waiting_approval', 'completed', 'failed', 'cancelled')),
    blocking_reason TEXT NOT NULL DEFAULT '' CHECK (length(blocking_reason) <= 20000),
    result TEXT NOT NULL DEFAULT '' CHECK (length(result) <= 400000),
    creator_kind TEXT NOT NULL CHECK (creator_kind IN ('human', 'agent')),
    creator_id TEXT NOT NULL CHECK (length(creator_id) = 36),
    created_at INTEGER NOT NULL,
    updated_at INTEGER NOT NULL,
    state_changed_at INTEGER NOT NULL,
    completed_at INTEGER,
    failed_at INTEGER,
    cancelled_at INTEGER,
    PRIMARY KEY (id, organization_id),
    UNIQUE (id, organization_id, root_work_id, source_space_id),
    FOREIGN KEY (root_work_id, organization_id)
        REFERENCES works(id, organization_id) ON DELETE RESTRICT DEFERRABLE INITIALLY DEFERRED,
    FOREIGN KEY (parent_work_id, organization_id, root_work_id, source_space_id)
        REFERENCES works(id, organization_id, root_work_id, source_space_id) ON DELETE RESTRICT,
    FOREIGN KEY (source_space_id, organization_id)
        REFERENCES spaces(id, organization_id) ON DELETE RESTRICT,
    FOREIGN KEY (source_message_id, source_space_id, source_target_kind, source_target_id, source_target_sequence)
        REFERENCES messages(id, space_id, target_kind, target_id, target_sequence) ON DELETE RESTRICT,
    FOREIGN KEY (team_space_id, organization_id, team_space_kind)
        REFERENCES spaces(id, organization_id, kind) ON DELETE RESTRICT,
    CHECK (
        (parent_work_id IS NULL AND root_work_id = id)
        OR (parent_work_id IS NOT NULL AND root_work_id != id)
    ),
    CHECK (
        (state = 'completed' AND completed_at IS NOT NULL AND failed_at IS NULL AND cancelled_at IS NULL AND length(result) >= 1 AND blocking_reason = '')
        OR (state = 'failed' AND completed_at IS NULL AND failed_at IS NOT NULL AND cancelled_at IS NULL AND length(result) >= 1)
        OR (state = 'cancelled' AND completed_at IS NULL AND failed_at IS NULL AND cancelled_at IS NOT NULL)
        OR (state = 'blocked' AND completed_at IS NULL AND failed_at IS NULL AND cancelled_at IS NULL AND length(blocking_reason) >= 1)
        OR (state IN ('open', 'waiting_approval') AND completed_at IS NULL AND failed_at IS NULL AND cancelled_at IS NULL AND blocking_reason = '')
    )
);

CREATE INDEX works_organization_root_parent
ON works(organization_id, root_work_id, parent_work_id, created_at, id);

CREATE INDEX works_organization_state
ON works(organization_id, state, updated_at, id);

CREATE TABLE work_constraints (
    id TEXT PRIMARY KEY CHECK (length(id) = 36),
    work_id TEXT NOT NULL,
    organization_id TEXT NOT NULL,
    ordinal INTEGER NOT NULL CHECK (ordinal >= 0),
    body TEXT NOT NULL CHECK (length(body) BETWEEN 1 AND 20000),
    created_at INTEGER NOT NULL,
    UNIQUE (work_id, ordinal),
    FOREIGN KEY (work_id, organization_id) REFERENCES works(id, organization_id) ON DELETE RESTRICT
);

CREATE TABLE work_acceptance_criteria (
    id TEXT PRIMARY KEY CHECK (length(id) = 36),
    work_id TEXT NOT NULL,
    organization_id TEXT NOT NULL,
    ordinal INTEGER NOT NULL CHECK (ordinal >= 0),
    body TEXT NOT NULL CHECK (length(body) BETWEEN 1 AND 20000),
    created_at INTEGER NOT NULL,
    UNIQUE (work_id, ordinal),
    UNIQUE (id, work_id, organization_id),
    FOREIGN KEY (work_id, organization_id) REFERENCES works(id, organization_id) ON DELETE RESTRICT
);

CREATE TABLE work_acceptance_results (
    sequence INTEGER PRIMARY KEY AUTOINCREMENT,
    id TEXT NOT NULL UNIQUE CHECK (length(id) = 36),
    work_id TEXT NOT NULL,
    organization_id TEXT NOT NULL,
    criterion_id TEXT NOT NULL,
    verdict TEXT NOT NULL CHECK (verdict IN ('passed', 'failed')),
    evidence TEXT NOT NULL CHECK (length(evidence) BETWEEN 1 AND 20000),
    actor_kind TEXT NOT NULL CHECK (actor_kind IN ('human', 'agent')),
    actor_id TEXT NOT NULL CHECK (length(actor_id) = 36),
    occurred_at INTEGER NOT NULL,
    FOREIGN KEY (criterion_id, work_id, organization_id)
        REFERENCES work_acceptance_criteria(id, work_id, organization_id) ON DELETE RESTRICT
);

CREATE INDEX work_acceptance_results_latest
ON work_acceptance_results(work_id, criterion_id, sequence DESC);

CREATE TABLE work_assignments (
    id TEXT PRIMARY KEY CHECK (length(id) = 36),
    work_id TEXT NOT NULL,
    organization_id TEXT NOT NULL,
    role TEXT NOT NULL CHECK (role IN ('coordinator', 'contributor')),
    agent_id TEXT NOT NULL REFERENCES agents(id) ON DELETE RESTRICT,
    holder_computer_id TEXT NOT NULL REFERENCES computers(id) ON DELETE RESTRICT,
    holder_placement_generation INTEGER NOT NULL CHECK (holder_placement_generation > 0),
    assigned_by_kind TEXT NOT NULL CHECK (assigned_by_kind IN ('human', 'agent')),
    assigned_by_id TEXT NOT NULL CHECK (length(assigned_by_id) = 36),
    assigned_at INTEGER NOT NULL,
    ended_at INTEGER,
    end_reason TEXT NOT NULL DEFAULT '' CHECK (end_reason IN ('', 'reassigned', 'completed', 'failed', 'cancelled')),
    FOREIGN KEY (work_id, organization_id) REFERENCES works(id, organization_id) ON DELETE RESTRICT,
    CHECK (
        (ended_at IS NULL AND end_reason = '')
        OR (ended_at IS NOT NULL AND end_reason != '' AND ended_at >= assigned_at)
    )
);

CREATE UNIQUE INDEX work_assignments_current_coordinator
ON work_assignments(work_id)
WHERE role = 'coordinator' AND ended_at IS NULL;

CREATE UNIQUE INDEX work_assignments_current_agent
ON work_assignments(work_id, agent_id)
WHERE ended_at IS NULL;

CREATE INDEX work_assignments_agent_history
ON work_assignments(agent_id, ended_at, assigned_at, id);

CREATE TABLE work_approvals (
    id TEXT PRIMARY KEY CHECK (length(id) = 36),
    work_id TEXT NOT NULL,
    organization_id TEXT NOT NULL,
    status TEXT NOT NULL CHECK (status IN ('pending', 'approved', 'rejected', 'cancelled')),
    question TEXT NOT NULL CHECK (length(question) BETWEEN 1 AND 20000),
    requested_by_kind TEXT NOT NULL CHECK (requested_by_kind IN ('human', 'agent')),
    requested_by_id TEXT NOT NULL CHECK (length(requested_by_id) = 36),
    requested_at INTEGER NOT NULL,
    decided_by_human_id TEXT REFERENCES humans(id) ON DELETE RESTRICT,
    decision_note TEXT NOT NULL DEFAULT '' CHECK (length(decision_note) <= 20000),
    decided_at INTEGER,
    FOREIGN KEY (work_id, organization_id) REFERENCES works(id, organization_id) ON DELETE RESTRICT,
    CHECK (
        (status = 'pending' AND decided_by_human_id IS NULL AND decision_note = '' AND decided_at IS NULL)
        OR (status IN ('approved', 'rejected') AND decided_by_human_id IS NOT NULL AND decided_at IS NOT NULL)
        OR (status = 'cancelled' AND decided_by_human_id IS NULL AND decided_at IS NOT NULL)
    )
);

CREATE UNIQUE INDEX work_approvals_one_pending
ON work_approvals(work_id)
WHERE status = 'pending';

CREATE TABLE work_events (
    sequence INTEGER PRIMARY KEY AUTOINCREMENT,
    id TEXT NOT NULL UNIQUE CHECK (length(id) = 36),
    work_id TEXT NOT NULL,
    organization_id TEXT NOT NULL,
    event_kind TEXT NOT NULL CHECK (event_kind IN ('created', 'assignment.started', 'assignment.ended', 'acceptance.recorded', 'state.transitioned', 'approval.requested', 'approval.resolved')),
    actor_kind TEXT NOT NULL CHECK (actor_kind IN ('human', 'agent')),
    actor_id TEXT NOT NULL CHECK (length(actor_id) = 36),
    from_state TEXT NOT NULL DEFAULT '',
    to_state TEXT NOT NULL DEFAULT '',
    reference_kind TEXT NOT NULL DEFAULT '' CHECK (reference_kind IN ('', 'assignment', 'criterion_result', 'approval')),
    reference_id TEXT NOT NULL DEFAULT '',
    reason TEXT NOT NULL DEFAULT '' CHECK (length(reason) <= 20000),
    occurred_at INTEGER NOT NULL,
    FOREIGN KEY (work_id, organization_id) REFERENCES works(id, organization_id) ON DELETE RESTRICT,
    CHECK (
        (event_kind = 'state.transitioned' AND from_state != '' AND to_state != '')
        OR (event_kind != 'state.transitioned' AND from_state = '' AND to_state = '')
    ),
    CHECK (
        (reference_kind = '' AND reference_id = '')
        OR (reference_kind != '' AND length(reference_id) = 36)
    )
);

CREATE INDEX work_events_work_sequence
ON work_events(work_id, sequence);

CREATE TABLE work_requests (
    request_id TEXT PRIMARY KEY CHECK (length(request_id) = 36),
    actor_kind TEXT NOT NULL CHECK (actor_kind IN ('human', 'agent')),
    actor_id TEXT NOT NULL CHECK (length(actor_id) = 36),
    operation TEXT NOT NULL CHECK (operation IN ('work.create', 'work.assign', 'work.transition', 'work.approval.request', 'work.approval.resolve')),
    payload_fingerprint BLOB NOT NULL CHECK (length(payload_fingerprint) = 32),
    result_kind TEXT NOT NULL CHECK (result_kind IN ('work', 'assignment', 'approval')),
    result_id TEXT NOT NULL CHECK (length(result_id) = 36),
    committed_at INTEGER NOT NULL
);

-- +goose StatementBegin
CREATE TRIGGER work_constraints_immutable_update
BEFORE UPDATE ON work_constraints
BEGIN
    SELECT RAISE(ABORT, 'work constraints are immutable');
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER work_constraints_immutable_delete
BEFORE DELETE ON work_constraints
BEGIN
    SELECT RAISE(ABORT, 'work constraints are immutable');
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER work_acceptance_criteria_immutable_update
BEFORE UPDATE ON work_acceptance_criteria
BEGIN
    SELECT RAISE(ABORT, 'work acceptance criteria are immutable');
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER work_acceptance_criteria_immutable_delete
BEFORE DELETE ON work_acceptance_criteria
BEGIN
    SELECT RAISE(ABORT, 'work acceptance criteria are immutable');
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER work_acceptance_results_append_only_update
BEFORE UPDATE ON work_acceptance_results
BEGIN
    SELECT RAISE(ABORT, 'work acceptance results are append-only');
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER work_acceptance_results_append_only_delete
BEFORE DELETE ON work_acceptance_results
BEGIN
    SELECT RAISE(ABORT, 'work acceptance results are append-only');
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER work_events_append_only_update
BEFORE UPDATE ON work_events
BEGIN
    SELECT RAISE(ABORT, 'work events are append-only');
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER work_events_append_only_delete
BEFORE DELETE ON work_events
BEGIN
    SELECT RAISE(ABORT, 'work events are append-only');
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER work_requests_immutable_update
BEFORE UPDATE ON work_requests
BEGIN
    SELECT RAISE(ABORT, 'work request receipts are immutable');
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER work_requests_immutable_delete
BEFORE DELETE ON work_requests
BEGIN
    SELECT RAISE(ABORT, 'work request receipts are immutable');
END;
-- +goose StatementEnd

-- +goose Down
DROP TRIGGER work_requests_immutable_delete;
DROP TRIGGER work_requests_immutable_update;
DROP TRIGGER work_events_append_only_delete;
DROP TRIGGER work_events_append_only_update;
DROP TRIGGER work_acceptance_results_append_only_delete;
DROP TRIGGER work_acceptance_results_append_only_update;
DROP TRIGGER work_acceptance_criteria_immutable_delete;
DROP TRIGGER work_acceptance_criteria_immutable_update;
DROP TRIGGER work_constraints_immutable_delete;
DROP TRIGGER work_constraints_immutable_update;

DROP TABLE work_requests;
DROP INDEX work_events_work_sequence;
DROP TABLE work_events;
DROP INDEX work_approvals_one_pending;
DROP TABLE work_approvals;
DROP INDEX work_assignments_agent_history;
DROP INDEX work_assignments_current_agent;
DROP INDEX work_assignments_current_coordinator;
DROP TABLE work_assignments;
DROP INDEX work_acceptance_results_latest;
DROP TABLE work_acceptance_results;
DROP TABLE work_acceptance_criteria;
DROP TABLE work_constraints;
DROP INDEX works_organization_state;
DROP INDEX works_organization_root_parent;
DROP TABLE works;
DROP INDEX spaces_id_organization_kind;
DROP INDEX spaces_id_organization;

ALTER TABLE grant_issue_requests RENAME TO grant_issue_requests_v15;
ALTER TABLE grant_revoke_requests RENAME TO grant_revoke_requests_v15;
DROP INDEX grants_subject_capability_scope;
ALTER TABLE grants RENAME TO grants_v15;

CREATE TABLE grants (
    id TEXT PRIMARY KEY CHECK (length(id) = 36),
    organization_id TEXT NOT NULL REFERENCES organizations(id) ON DELETE RESTRICT,
    subject_kind TEXT NOT NULL CHECK (subject_kind IN ('human', 'agent')),
    subject_id TEXT NOT NULL CHECK (length(subject_id) = 36),
    issuer_kind TEXT NOT NULL CHECK (issuer_kind IN ('system', 'human', 'agent')),
    issuer_id TEXT NOT NULL,
    capability TEXT NOT NULL,
    scope_kind TEXT NOT NULL CHECK (scope_kind IN ('organization', 'agent', 'computer', 'space')),
    scope_id TEXT NOT NULL CHECK (length(scope_id) = 36),
    parent_grant_id TEXT NOT NULL DEFAULT '',
    expires_at INTEGER,
    revoked_at INTEGER,
    created_at INTEGER NOT NULL,
    updated_at INTEGER NOT NULL,
    CHECK ((issuer_kind = 'system' AND issuer_id = '') OR (issuer_kind != 'system' AND length(issuer_id) = 36)),
    CHECK (expires_at IS NULL OR expires_at > created_at),
    CHECK (revoked_at IS NULL OR revoked_at >= created_at)
);

INSERT INTO grants(
    id, organization_id, subject_kind, subject_id, issuer_kind, issuer_id,
    capability, scope_kind, scope_id, parent_grant_id, expires_at, revoked_at,
    created_at, updated_at
)
SELECT
    id, organization_id, subject_kind, subject_id, issuer_kind, issuer_id,
    capability, scope_kind, scope_id, parent_grant_id, expires_at, revoked_at,
    created_at, updated_at
FROM grants_v15
WHERE scope_kind != 'work';

CREATE INDEX grants_subject_capability_scope
ON grants(organization_id, subject_kind, subject_id, capability, scope_kind, scope_id);

CREATE TABLE grant_issue_requests (
    request_id TEXT PRIMARY KEY CHECK (length(request_id) = 36),
    grant_id TEXT NOT NULL UNIQUE REFERENCES grants(id) ON DELETE RESTRICT,
    payload_fingerprint BLOB NOT NULL CHECK (length(payload_fingerprint) = 32)
);

INSERT INTO grant_issue_requests(request_id, grant_id, payload_fingerprint)
SELECT request_id, grant_id, payload_fingerprint
FROM grant_issue_requests_v15
WHERE grant_id IN (SELECT id FROM grants);

CREATE TABLE grant_revoke_requests (
    request_id TEXT PRIMARY KEY CHECK (length(request_id) = 36),
    grant_id TEXT NOT NULL REFERENCES grants(id) ON DELETE RESTRICT,
    payload_fingerprint BLOB NOT NULL CHECK (length(payload_fingerprint) = 32)
);

INSERT INTO grant_revoke_requests(request_id, grant_id, payload_fingerprint)
SELECT request_id, grant_id, payload_fingerprint
FROM grant_revoke_requests_v15
WHERE grant_id IN (SELECT id FROM grants);

DROP TABLE grant_revoke_requests_v15;
DROP TABLE grant_issue_requests_v15;
DROP TABLE grants_v15;
