CREATE TABLE agent_create_requests (
    request_id TEXT PRIMARY KEY CHECK (length(request_id) = 36),
    actor_kind TEXT NOT NULL CHECK (actor_kind IN ('system', 'human')),
    actor_id TEXT NOT NULL,
    agent_id TEXT NOT NULL UNIQUE REFERENCES agents(id) ON DELETE RESTRICT,
    payload_fingerprint BLOB NOT NULL CHECK (length(payload_fingerprint) = 32),
    CHECK (
        (actor_kind = 'system' AND actor_id = '')
        OR (actor_kind = 'human' AND length(actor_id) = 36)
    )
);
CREATE TABLE agent_held_draft_mentions (
    draft_id TEXT NOT NULL REFERENCES agent_held_drafts(id) ON DELETE RESTRICT,
    principal_kind TEXT NOT NULL CHECK (principal_kind IN ('human', 'agent')),
    principal_id TEXT NOT NULL CHECK (length(principal_id) = 36),
    ordinal INTEGER NOT NULL CHECK (ordinal BETWEEN 0 AND 63),
    PRIMARY KEY (draft_id, principal_kind, principal_id),
    UNIQUE (draft_id, ordinal)
);
CREATE TABLE agent_held_drafts (
    sequence INTEGER PRIMARY KEY AUTOINCREMENT,
    id TEXT NOT NULL UNIQUE CHECK (length(id) = 36),
    agent_id TEXT NOT NULL REFERENCES agents(id) ON DELETE RESTRICT,
    inbox_item_id TEXT NOT NULL,
    inbox_recipient_kind TEXT NOT NULL DEFAULT 'agent' CHECK (inbox_recipient_kind = 'agent'),
    predecessor_draft_id TEXT REFERENCES agent_held_drafts(id) ON DELETE RESTRICT,
    space_id TEXT NOT NULL REFERENCES spaces(id) ON DELETE RESTRICT,
    target_kind TEXT NOT NULL CHECK (target_kind IN ('space', 'thread')),
    target_id TEXT NOT NULL,
    basis_target_sequence INTEGER NOT NULL CHECK (basis_target_sequence >= 0),
    body TEXT NOT NULL CHECK (length(body) BETWEEN 1 AND 400000),
    held_reason TEXT NOT NULL CHECK (held_reason = 'target_advanced'),
    state TEXT NOT NULL CHECK (state IN ('held', 'sent', 'cancelled', 'superseded', 'retargeted')),
    resolution_action TEXT NOT NULL DEFAULT '' CHECK (resolution_action IN ('', 'retry', 'cancel', 'retarget')),
    result_kind TEXT NOT NULL DEFAULT '' CHECK (result_kind IN ('', 'message', 'held_draft')),
    result_id TEXT NOT NULL DEFAULT '',
    created_at INTEGER NOT NULL,
    updated_at INTEGER NOT NULL,
    FOREIGN KEY (inbox_item_id, inbox_recipient_kind, agent_id)
        REFERENCES inbox_items(id, recipient_kind, recipient_id) ON DELETE RESTRICT,
    CHECK (
        (target_kind = 'space' AND target_id = space_id)
        OR (target_kind = 'thread' AND length(target_id) = 36)
    ),
    CHECK (
        (state = 'held' AND resolution_action = '' AND result_kind = '' AND result_id = '')
        OR (state = 'cancelled' AND resolution_action = 'cancel' AND result_kind = '' AND result_id = '')
        OR (state = 'sent' AND resolution_action = 'retry' AND result_kind = 'message' AND length(result_id) = 36)
        OR (state = 'superseded' AND resolution_action = 'retry' AND result_kind = 'held_draft' AND length(result_id) = 36)
        OR (state = 'retargeted' AND resolution_action = 'retarget' AND result_kind IN ('message', 'held_draft') AND length(result_id) = 36)
    )
);
CREATE TABLE inbox_items (
    sequence INTEGER PRIMARY KEY AUTOINCREMENT,
    id TEXT NOT NULL UNIQUE CHECK (length(id) = 36),
    recipient_kind TEXT NOT NULL CHECK (recipient_kind IN ('human', 'agent')),
    recipient_id TEXT NOT NULL CHECK (length(recipient_id) = 36),
    space_id TEXT NOT NULL REFERENCES spaces(id) ON DELETE RESTRICT,
    target_kind TEXT NOT NULL CHECK (target_kind IN ('space', 'thread')),
    target_id TEXT NOT NULL,
    trigger_message_id TEXT NOT NULL REFERENCES messages(id) ON DELETE RESTRICT,
    trigger_target_sequence INTEGER NOT NULL CHECK (trigger_target_sequence >= 1),
    reason TEXT NOT NULL CHECK (reason IN ('dm', 'mention', 'thread_follow')),
    state TEXT NOT NULL CHECK (state IN ('unread', 'claimed', 'done')),
    claimed_at INTEGER,
    done_at INTEGER,
    completion TEXT NOT NULL DEFAULT '' CHECK (completion IN ('', 'sent', 'cancelled', 'silent', 'access_lost')),
    created_at INTEGER NOT NULL,
    UNIQUE (recipient_kind, recipient_id, trigger_message_id),
    FOREIGN KEY (trigger_message_id, space_id, target_kind, target_id, trigger_target_sequence)
        REFERENCES messages(id, space_id, target_kind, target_id, target_sequence) ON DELETE RESTRICT,
    CHECK (
        (target_kind = 'space' AND target_id = space_id)
        OR (target_kind = 'thread' AND length(target_id) = 36)
    ),
    CHECK (
        (state = 'unread' AND claimed_at IS NULL AND done_at IS NULL AND completion = '')
        OR (state = 'claimed' AND claimed_at IS NOT NULL AND done_at IS NULL AND completion = '')
        OR (state = 'done' AND done_at IS NOT NULL AND completion != '')
    )
);
CREATE TABLE agent_placement_requests (
    request_id TEXT PRIMARY KEY CHECK (length(request_id) = 36),
    actor_kind TEXT NOT NULL CHECK (actor_kind = 'human'),
    actor_id TEXT NOT NULL REFERENCES humans(id) ON DELETE RESTRICT CHECK (length(actor_id) = 36),
    payload_fingerprint BLOB NOT NULL CHECK (length(payload_fingerprint) = 32),
    agent_id TEXT NOT NULL REFERENCES agents(id) ON DELETE RESTRICT,
    computer_id TEXT NOT NULL REFERENCES computers(id) ON DELETE RESTRICT,
    generation INTEGER NOT NULL CHECK (generation > 0),
    state TEXT NOT NULL CHECK (state IN ('pending', 'active', 'failed')),
    error_code TEXT NOT NULL,
    created_at INTEGER NOT NULL,
    updated_at INTEGER NOT NULL,
    CHECK (
        (state = 'failed' AND length(error_code) BETWEEN 1 AND 64)
        OR (state IN ('pending', 'active') AND error_code = '')
    )
);
CREATE TABLE agent_placements (
			agent_id TEXT PRIMARY KEY REFERENCES agents(id) ON DELETE RESTRICT,
			computer_id TEXT NOT NULL REFERENCES computers(id) ON DELETE RESTRICT,
			generation INTEGER NOT NULL CHECK (generation > 0),
			state TEXT NOT NULL CHECK (state IN ('pending', 'active', 'failed')),
			error_code TEXT NOT NULL DEFAULT '',
			created_at INTEGER NOT NULL,
			updated_at INTEGER NOT NULL,
			CHECK (
				(state = 'failed' AND length(error_code) BETWEEN 1 AND 64)
				OR (state IN ('pending', 'active') AND error_code = '')
			)
		);
CREATE TABLE inbox_requests (
    request_id TEXT PRIMARY KEY CHECK (length(request_id) = 36),
    actor_kind TEXT NOT NULL CHECK (actor_kind IN ('human', 'agent')),
    actor_id TEXT NOT NULL CHECK (length(actor_id) = 36),
    operation TEXT NOT NULL CHECK (length(operation) BETWEEN 1 AND 64),
    payload_fingerprint BLOB NOT NULL CHECK (length(payload_fingerprint) = 32),
    response_snapshot BLOB NOT NULL CHECK (length(response_snapshot) > 0),
    committed_at INTEGER NOT NULL
);
CREATE TABLE agent_run_fences (
    agent_id TEXT PRIMARY KEY REFERENCES agents(id) ON DELETE RESTRICT,
    current_fence INTEGER NOT NULL CHECK (current_fence >= 0)
);
CREATE TABLE agent_runtime_sessions (
    token_hash BLOB PRIMARY KEY CHECK (length(token_hash) = 32),
    agent_id TEXT NOT NULL REFERENCES agents(id) ON DELETE RESTRICT CHECK (length(agent_id) = 36),
    computer_id TEXT NOT NULL REFERENCES computers(id) ON DELETE RESTRICT CHECK (length(computer_id) = 36),
    placement_generation INTEGER NOT NULL CHECK (placement_generation > 0),
    created_at INTEGER NOT NULL,
    expires_at INTEGER NOT NULL,
    revoked_at INTEGER,
    CHECK (expires_at > created_at),
    CHECK (revoked_at IS NULL OR revoked_at >= created_at)
);
CREATE TABLE principal_space_mutes (
    principal_kind TEXT NOT NULL CHECK (principal_kind IN ('human', 'agent')),
    principal_id TEXT NOT NULL CHECK (length(principal_id) = 36),
    space_id TEXT NOT NULL REFERENCES spaces(id) ON DELETE RESTRICT,
    muted_at INTEGER NOT NULL,
    PRIMARY KEY (principal_kind, principal_id, space_id)
);
CREATE TABLE principal_target_cursors (
    principal_kind TEXT NOT NULL CHECK (principal_kind IN ('human', 'agent')),
    principal_id TEXT NOT NULL CHECK (length(principal_id) = 36),
    space_id TEXT NOT NULL REFERENCES spaces(id) ON DELETE RESTRICT,
    target_kind TEXT NOT NULL CHECK (target_kind IN ('space', 'thread')),
    target_id TEXT NOT NULL,
    seen_up_to_target_sequence INTEGER NOT NULL CHECK (seen_up_to_target_sequence >= 0),
    observed_at INTEGER NOT NULL,
    PRIMARY KEY (principal_kind, principal_id, target_kind, target_id),
    CHECK (
        (target_kind = 'space' AND target_id = space_id)
        OR (target_kind = 'thread' AND length(target_id) = 36)
    )
);
CREATE TABLE principal_thread_follows (
    principal_kind TEXT NOT NULL CHECK (principal_kind IN ('human', 'agent')),
    principal_id TEXT NOT NULL CHECK (length(principal_id) = 36),
    space_id TEXT NOT NULL REFERENCES spaces(id) ON DELETE RESTRICT,
    thread_root_message_id TEXT NOT NULL,
    followed_at INTEGER NOT NULL,
    source TEXT NOT NULL CHECK (source IN ('mention', 'reply', 'explicit')),
    PRIMARY KEY (principal_kind, principal_id, thread_root_message_id),
    FOREIGN KEY (thread_root_message_id, space_id) REFERENCES threads(id, space_id) ON DELETE RESTRICT
);
CREATE TABLE "agents" (
			id TEXT PRIMARY KEY CHECK (length(id) = 36),
			name TEXT NOT NULL UNIQUE CHECK (length(name) BETWEEN 1 AND 32),
			description TEXT NOT NULL CHECK (length(description) <= 1000),
			driver TEXT NOT NULL CHECK (driver IN ('native', 'codex', 'claude')),
			created_at INTEGER NOT NULL,
			updated_at INTEGER NOT NULL
		);
CREATE TABLE artifact_blobs (
    digest BLOB PRIMARY KEY CHECK (length(digest) = 32),
    size INTEGER NOT NULL CHECK (size BETWEEN 0 AND 67108864),
    integrity_state TEXT NOT NULL CHECK (integrity_state IN ('ready', 'missing', 'corrupt')),
    created_at INTEGER NOT NULL,
    checked_at INTEGER NOT NULL CHECK (checked_at >= created_at)
);
CREATE TABLE artifact_grants (
    id TEXT PRIMARY KEY CHECK (length(id) = 36),
    artifact_id TEXT NOT NULL,
    organization_id TEXT NOT NULL,
    target_kind TEXT NOT NULL CHECK (target_kind IN ('agent', 'space', 'work')),
    target_id TEXT NOT NULL CHECK (length(target_id) = 36),
    capability TEXT NOT NULL CHECK (capability IN ('read', 'manage')),
    granted_by_kind TEXT NOT NULL CHECK (granted_by_kind IN ('human', 'agent')),
    granted_by_id TEXT NOT NULL CHECK (length(granted_by_id) = 36),
    granted_at INTEGER NOT NULL,
    revoked_by_kind TEXT CHECK (revoked_by_kind IS NULL OR revoked_by_kind IN ('human', 'agent')),
    revoked_by_id TEXT,
    revoked_at INTEGER,
    FOREIGN KEY (artifact_id, organization_id) REFERENCES artifacts(id, organization_id) ON DELETE RESTRICT,
    CHECK (capability != 'manage' OR target_kind = 'agent'),
    CHECK (
        (revoked_by_kind IS NULL AND revoked_by_id IS NULL AND revoked_at IS NULL)
        OR
        (revoked_by_kind IS NOT NULL AND length(revoked_by_id) = 36 AND revoked_at >= granted_at)
    )
);
CREATE TABLE artifact_requests (
    request_id TEXT PRIMARY KEY CHECK (length(request_id) = 36),
    actor_kind TEXT NOT NULL CHECK (actor_kind IN ('human', 'agent')),
    actor_id TEXT NOT NULL CHECK (length(actor_id) = 36),
    operation TEXT NOT NULL CHECK (operation IN ('artifact.publish', 'artifact.grant', 'artifact.revoke')),
    payload_fingerprint BLOB NOT NULL CHECK (length(payload_fingerprint) = 32),
    result_kind TEXT NOT NULL CHECK (result_kind IN ('version', 'grant')),
    result_artifact_id TEXT,
    result_version INTEGER,
    result_grant_id TEXT,
    committed_at INTEGER NOT NULL,
    FOREIGN KEY (result_artifact_id, result_version)
        REFERENCES artifact_versions(artifact_id, version) ON DELETE RESTRICT,
    FOREIGN KEY (result_grant_id) REFERENCES artifact_grants(id) ON DELETE RESTRICT,
    CHECK (
        (result_kind = 'version' AND result_artifact_id IS NOT NULL AND result_version >= 1 AND result_grant_id IS NULL)
        OR
        (result_kind = 'grant' AND result_artifact_id IS NULL AND result_version IS NULL AND result_grant_id IS NOT NULL)
    )
);
CREATE TABLE artifact_version_executions (
    artifact_id TEXT NOT NULL,
    version INTEGER NOT NULL,
    organization_id TEXT NOT NULL,
    delivery_id TEXT NOT NULL,
    run_id TEXT NOT NULL,
    launch_id TEXT NOT NULL,
    agent_id TEXT NOT NULL,
    computer_id TEXT NOT NULL,
    placement_generation INTEGER NOT NULL CHECK (placement_generation > 0),
    fence INTEGER NOT NULL CHECK (fence > 0),
    PRIMARY KEY (artifact_id, version),
    FOREIGN KEY (artifact_id, version, organization_id)
        REFERENCES artifact_versions(artifact_id, version, organization_id) ON DELETE RESTRICT,
    FOREIGN KEY (run_id, delivery_id, agent_id)
        REFERENCES runs(id, delivery_id, agent_id) ON DELETE RESTRICT,
    FOREIGN KEY (delivery_id, agent_id)
        REFERENCES deliveries(id, agent_id) ON DELETE RESTRICT,
    FOREIGN KEY (launch_id, run_id, fence, agent_id, computer_id, placement_generation)
        REFERENCES run_launches(id, run_id, fence, agent_id, holder_computer_id, holder_placement_generation) ON DELETE RESTRICT
);
CREATE TABLE artifact_version_sources (
    id TEXT PRIMARY KEY CHECK (length(id) = 36),
    artifact_id TEXT NOT NULL,
    version INTEGER NOT NULL,
    organization_id TEXT NOT NULL,
    ordinal INTEGER NOT NULL CHECK (ordinal >= 0),
    source_kind TEXT NOT NULL CHECK (source_kind IN ('message', 'artifact_version')),
    source_message_id TEXT,
    source_artifact_id TEXT,
    source_artifact_version INTEGER,
    UNIQUE (artifact_id, version, ordinal),
    FOREIGN KEY (artifact_id, version, organization_id)
        REFERENCES artifact_versions(artifact_id, version, organization_id) ON DELETE RESTRICT,
    FOREIGN KEY (source_message_id) REFERENCES messages(id) ON DELETE RESTRICT,
    FOREIGN KEY (source_artifact_id, source_artifact_version, organization_id)
        REFERENCES artifact_versions(artifact_id, version, organization_id) ON DELETE RESTRICT,
    CHECK (
        (source_kind = 'message' AND source_message_id IS NOT NULL AND source_artifact_id IS NULL AND source_artifact_version IS NULL)
        OR
        (source_kind = 'artifact_version' AND source_message_id IS NULL AND source_artifact_id IS NOT NULL AND source_artifact_version >= 1)
    )
);
CREATE TABLE artifact_versions (
    artifact_id TEXT NOT NULL,
    organization_id TEXT NOT NULL,
    version INTEGER NOT NULL CHECK (version >= 1),
    digest BLOB NOT NULL,
    size INTEGER NOT NULL CHECK (size BETWEEN 0 AND 67108864),
    summary TEXT NOT NULL CHECK (length(summary) BETWEEN 1 AND 20000),
    author_kind TEXT NOT NULL CHECK (author_kind IN ('human', 'agent')),
    author_id TEXT NOT NULL CHECK (length(author_id) = 36),
    created_at INTEGER NOT NULL,
    PRIMARY KEY (artifact_id, version),
    UNIQUE (artifact_id, version, organization_id),
    FOREIGN KEY (artifact_id, organization_id) REFERENCES artifacts(id, organization_id) ON DELETE RESTRICT,
    FOREIGN KEY (digest, size) REFERENCES artifact_blobs(digest, size) ON DELETE RESTRICT
);
CREATE TABLE artifacts (
    id TEXT NOT NULL CHECK (length(id) = 36),
    organization_id TEXT NOT NULL REFERENCES organizations(id) ON DELETE RESTRICT,
    owning_work_id TEXT NOT NULL,
    name TEXT NOT NULL CHECK (length(name) BETWEEN 1 AND 255),
    media_type TEXT NOT NULL CHECK (length(media_type) BETWEEN 1 AND 255),
    creator_kind TEXT NOT NULL CHECK (creator_kind IN ('human', 'agent')),
    creator_id TEXT NOT NULL CHECK (length(creator_id) = 36),
    created_at INTEGER NOT NULL,
    PRIMARY KEY (id, organization_id),
    UNIQUE (id),
    FOREIGN KEY (owning_work_id, organization_id) REFERENCES works(id, organization_id) ON DELETE RESTRICT
);
CREATE TABLE audit_events (
    sequence INTEGER PRIMARY KEY AUTOINCREMENT,
    id TEXT NOT NULL UNIQUE CHECK (length(id) = 36),
    organization_id TEXT NOT NULL REFERENCES organizations(id) ON DELETE RESTRICT,
    actor_kind TEXT NOT NULL CHECK (actor_kind IN ('system', 'human', 'agent')),
    actor_id TEXT NOT NULL,
    action TEXT NOT NULL,
    target_kind TEXT NOT NULL,
    target_id TEXT NOT NULL,
    request_id TEXT NOT NULL,
    outcome TEXT NOT NULL CHECK (outcome IN ('committed', 'denied')),
    reason_code TEXT NOT NULL,
    occurred_at INTEGER NOT NULL,
    context_kind TEXT NOT NULL DEFAULT '' CHECK (context_kind IN ('', 'space', 'thread', 'computer')),
    context_id TEXT NOT NULL DEFAULT '',
    CHECK ((actor_kind = 'system' AND actor_id = '') OR (actor_kind != 'system' AND length(actor_id) = 36)),
    CHECK ((outcome = 'committed' AND reason_code = '') OR (outcome = 'denied' AND length(reason_code) BETWEEN 1 AND 64)),
    CHECK (
        (context_kind = '' AND context_id = '')
        OR (context_kind IN ('space', 'thread', 'computer') AND length(context_id) = 36)
    )
);
CREATE TABLE browser_handoffs (
    token_hash BLOB PRIMARY KEY CHECK (length(token_hash) = 32),
    human_id TEXT NOT NULL REFERENCES humans(id) ON DELETE RESTRICT,
    created_at INTEGER NOT NULL,
    expires_at INTEGER NOT NULL,
    consumed_at INTEGER,
    CHECK (expires_at > created_at),
    CHECK (consumed_at IS NULL OR (consumed_at >= created_at AND consumed_at <= expires_at))
);
CREATE TABLE browser_sessions (
    token_hash BLOB PRIMARY KEY CHECK (length(token_hash) = 32),
    human_id TEXT NOT NULL REFERENCES humans(id) ON DELETE RESTRICT,
    created_at INTEGER NOT NULL,
    expires_at INTEGER NOT NULL,
    revoked_at INTEGER,
    CHECK (expires_at > created_at),
    CHECK (revoked_at IS NULL OR revoked_at >= created_at)
);
CREATE TABLE auth_identities (
    id TEXT PRIMARY KEY CHECK (length(id) = 36),
    human_id TEXT NOT NULL REFERENCES humans(id) ON DELETE RESTRICT,
    provider TEXT NOT NULL CHECK (length(provider) BETWEEN 1 AND 32),
    subject TEXT NOT NULL CHECK (length(subject) BETWEEN 1 AND 255),
    created_at INTEGER NOT NULL,
    UNIQUE (provider, subject)
);
CREATE TABLE local_password_credentials (
    identity_id TEXT PRIMARY KEY REFERENCES auth_identities(id) ON DELETE RESTRICT,
    algorithm TEXT NOT NULL CHECK (algorithm = 'argon2id'),
    salt BLOB NOT NULL CHECK (length(salt) = 16),
    digest BLOB NOT NULL CHECK (length(digest) = 32),
    memory_kib INTEGER NOT NULL CHECK (memory_kib BETWEEN 8192 AND 262144),
    iterations INTEGER NOT NULL CHECK (iterations BETWEEN 1 AND 10),
    parallelism INTEGER NOT NULL CHECK (parallelism BETWEEN 1 AND 8),
    updated_at INTEGER NOT NULL
);
CREATE TABLE collaboration_requests (
    request_id TEXT PRIMARY KEY CHECK (length(request_id) = 36),
    operation TEXT NOT NULL CHECK (operation IN ('space.create.dm', 'space.create.group', 'space.member.add', 'space.member.remove', 'space.archive', 'space.unarchive', 'message.send')),
    payload_fingerprint BLOB NOT NULL CHECK (length(payload_fingerprint) = 32),
    result_id TEXT NOT NULL CHECK (length(result_id) = 36),
    committed_at INTEGER NOT NULL
);
CREATE TABLE computer_pairing_sandbox_receipts (
			pairing_id TEXT PRIMARY KEY REFERENCES computer_pairings(id) ON DELETE RESTRICT,
			sandbox_provider TEXT NOT NULL CHECK (sandbox_provider IN ('unknown', 'trusted_local')),
			sandbox_isolation TEXT NOT NULL CHECK (sandbox_isolation IN ('unknown', 'trusted_local')),
			sandbox_workspace_access TEXT NOT NULL CHECK (sandbox_workspace_access IN ('unknown', 'direct_read_write')),
			sandbox_process_control TEXT NOT NULL CHECK (sandbox_process_control IN ('unknown', 'context_process_group')),
			sandbox_filesystem_isolation TEXT NOT NULL CHECK (sandbox_filesystem_isolation IN ('unknown', 'none')),
			sandbox_network_isolation TEXT NOT NULL CHECK (sandbox_network_isolation IN ('unknown', 'none')),
			sandbox_secret_materialization TEXT NOT NULL CHECK (sandbox_secret_materialization IN ('unknown', 'ephemeral_environment')),
			sandbox_daemon_crash_cleanup TEXT NOT NULL CHECK (sandbox_daemon_crash_cleanup IN ('unknown', 'none')),
			sandbox_declaration_revision INTEGER NOT NULL CHECK (sandbox_declaration_revision >= 0)
		);
CREATE TABLE computer_pairings (
    id TEXT PRIMARY KEY CHECK (length(id) = 36),
    request_id TEXT NOT NULL UNIQUE CHECK (length(request_id) = 36),
    human_id TEXT NOT NULL REFERENCES humans(id) ON DELETE RESTRICT,
    token_hash BLOB NOT NULL UNIQUE CHECK (length(token_hash) = 32),
    payload_fingerprint BLOB NOT NULL CHECK (length(payload_fingerprint) = 32),
    created_at INTEGER NOT NULL,
    expires_at INTEGER NOT NULL,
    consumed_at INTEGER,
    consume_request_id TEXT UNIQUE CHECK (consume_request_id IS NULL OR length(consume_request_id) = 36),
    consume_fingerprint BLOB CHECK (consume_fingerprint IS NULL OR length(consume_fingerprint) = 32),
    computer_id TEXT UNIQUE REFERENCES computers(id) ON DELETE RESTRICT,
    CHECK (expires_at > created_at),
    CHECK (
        (consumed_at IS NULL AND consume_request_id IS NULL AND consume_fingerprint IS NULL AND computer_id IS NULL)
        OR (consumed_at IS NOT NULL AND consume_request_id IS NOT NULL AND consume_fingerprint IS NOT NULL AND computer_id IS NOT NULL)
    )
);
CREATE TABLE "computers" (
			id TEXT PRIMARY KEY CHECK (length(id) = 36),
			registration_key_hash BLOB NOT NULL UNIQUE CHECK (length(registration_key_hash) = 32),
			name TEXT NOT NULL CHECK (length(name) BETWEEN 1 AND 100),
			os TEXT NOT NULL CHECK (os IN ('macos', 'linux')),
			arch TEXT NOT NULL CHECK (arch IN ('arm64', 'amd64')),
			created_at INTEGER NOT NULL,
			last_seen_at INTEGER NOT NULL
		, sandbox_provider TEXT NOT NULL DEFAULT 'unknown'
			CHECK (sandbox_provider IN ('unknown', 'trusted_local')), sandbox_isolation TEXT NOT NULL DEFAULT 'unknown'
			CHECK (sandbox_isolation IN ('unknown', 'trusted_local')), sandbox_workspace_access TEXT NOT NULL DEFAULT 'unknown'
			CHECK (sandbox_workspace_access IN ('unknown', 'direct_read_write')), sandbox_process_control TEXT NOT NULL DEFAULT 'unknown'
			CHECK (sandbox_process_control IN ('unknown', 'context_process_group')), sandbox_filesystem_isolation TEXT NOT NULL DEFAULT 'unknown'
			CHECK (sandbox_filesystem_isolation IN ('unknown', 'none')), sandbox_network_isolation TEXT NOT NULL DEFAULT 'unknown'
			CHECK (sandbox_network_isolation IN ('unknown', 'none')), sandbox_secret_materialization TEXT NOT NULL DEFAULT 'unknown'
			CHECK (sandbox_secret_materialization IN ('unknown', 'ephemeral_environment')), sandbox_daemon_crash_cleanup TEXT NOT NULL DEFAULT 'unknown'
			CHECK (sandbox_daemon_crash_cleanup IN ('unknown', 'none')), sandbox_declaration_revision INTEGER NOT NULL DEFAULT 0
			CHECK (sandbox_declaration_revision >= 0));
CREATE TABLE deliveries (
    sequence INTEGER PRIMARY KEY AUTOINCREMENT,
    id TEXT NOT NULL UNIQUE CHECK (length(id) = 36),
    agent_id TEXT NOT NULL REFERENCES agents(id) ON DELETE RESTRICT,
    inbox_item_id TEXT NOT NULL UNIQUE,
    inbox_recipient_kind TEXT NOT NULL DEFAULT 'agent' CHECK (inbox_recipient_kind = 'agent'),
    trigger_message_id TEXT NOT NULL,
    space_id TEXT NOT NULL REFERENCES spaces(id) ON DELETE RESTRICT,
    target_kind TEXT NOT NULL CHECK (target_kind IN ('space', 'thread')),
    target_id TEXT NOT NULL,
    trigger_target_sequence INTEGER NOT NULL CHECK (trigger_target_sequence >= 1),
    state TEXT NOT NULL CHECK (state IN ('available', 'accepted', 'completed')),
    created_at INTEGER NOT NULL,
    accepted_at INTEGER,
    completed_at INTEGER,
    UNIQUE (agent_id, trigger_message_id),
    FOREIGN KEY (inbox_item_id, inbox_recipient_kind, agent_id)
        REFERENCES inbox_items(id, recipient_kind, recipient_id) ON DELETE RESTRICT,
    FOREIGN KEY (trigger_message_id, space_id, target_kind, target_id, trigger_target_sequence)
        REFERENCES messages(id, space_id, target_kind, target_id, target_sequence) ON DELETE RESTRICT,
    CHECK (
        (target_kind = 'space' AND target_id = space_id)
        OR (target_kind = 'thread' AND length(target_id) = 36)
    ),
    CHECK (
        (state = 'available' AND accepted_at IS NULL AND completed_at IS NULL)
        OR (state = 'accepted' AND accepted_at IS NOT NULL AND completed_at IS NULL)
        OR (state = 'completed' AND accepted_at IS NOT NULL AND completed_at IS NOT NULL)
    )
);
CREATE TABLE grant_issue_requests (
    request_id TEXT PRIMARY KEY CHECK (length(request_id) = 36),
    grant_id TEXT NOT NULL UNIQUE REFERENCES grants(id) ON DELETE RESTRICT,
    payload_fingerprint BLOB NOT NULL CHECK (length(payload_fingerprint) = 32)
);
CREATE TABLE grant_revoke_requests (
    request_id TEXT PRIMARY KEY CHECK (length(request_id) = 36),
    grant_id TEXT NOT NULL REFERENCES grants(id) ON DELETE RESTRICT,
    payload_fingerprint BLOB NOT NULL CHECK (length(payload_fingerprint) = 32)
);
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
CREATE TABLE human_create_requests (
    request_id TEXT PRIMARY KEY CHECK (length(request_id) = 36),
    human_id TEXT NOT NULL UNIQUE REFERENCES humans(id) ON DELETE RESTRICT,
    payload_fingerprint BLOB NOT NULL CHECK (length(payload_fingerprint) = 32)
);
CREATE TABLE human_status_requests (
    request_id TEXT PRIMARY KEY CHECK (length(request_id) = 36),
    human_id TEXT NOT NULL REFERENCES humans(id) ON DELETE RESTRICT,
    payload_fingerprint BLOB NOT NULL CHECK (length(payload_fingerprint) = 32)
);
CREATE TABLE humans (
    id TEXT PRIMARY KEY CHECK (length(id) = 36),
    organization_id TEXT NOT NULL REFERENCES organizations(id) ON DELETE RESTRICT,
    name TEXT NOT NULL CHECK (length(name) BETWEEN 1 AND 100),
    role TEXT NOT NULL CHECK (role IN ('owner', 'member')),
    status TEXT NOT NULL CHECK (status IN ('active', 'disabled')),
    credential_hash BLOB NOT NULL UNIQUE CHECK (length(credential_hash) = 32),
    created_at INTEGER NOT NULL,
    updated_at INTEGER NOT NULL,
    UNIQUE (organization_id, name)
);
CREATE TABLE work_cursor_keys (
    singleton INTEGER PRIMARY KEY CHECK (singleton = 1),
    key BLOB NOT NULL CHECK (length(key) = 32)
);
CREATE TABLE knowledge_dirty_sources (
    sequence INTEGER PRIMARY KEY AUTOINCREMENT,
    source_kind TEXT NOT NULL CHECK (source_kind IN ('message', 'work', 'artifact_version')),
    source_id TEXT NOT NULL CHECK (length(source_id) = 36),
    source_version INTEGER NOT NULL DEFAULT 0 CHECK (source_version >= 0),
    revision BLOB NOT NULL CHECK (length(revision) = 32),
    enqueued_at INTEGER NOT NULL,
    CHECK (
        (source_kind IN ('message', 'work') AND source_version = 0)
        OR (source_kind = 'artifact_version' AND source_version >= 1)
    )
);
CREATE VIRTUAL TABLE knowledge_fts USING fts5(
    source_kind UNINDEXED,
    source_id UNINDEXED,
    source_version UNINDEXED,
    revision UNINDEXED,
    body
);
CREATE TABLE knowledge_index_state (
    singleton INTEGER PRIMARY KEY CHECK (singleton = 1),
    applied_sequence INTEGER NOT NULL DEFAULT 0 CHECK (applied_sequence >= 0),
    status TEXT NOT NULL CHECK (status IN ('ready', 'degraded'))
);
INSERT INTO knowledge_index_state(singleton, status) VALUES(1, 'degraded');
CREATE TABLE knowledge_projection_rows (
    source_kind TEXT NOT NULL CHECK (source_kind IN ('message', 'work', 'artifact_version')),
    source_id TEXT NOT NULL CHECK (length(source_id) = 36),
    source_version INTEGER NOT NULL CHECK (source_version >= 0),
    revision BLOB NOT NULL CHECK (length(revision) = 32),
    fts_rowid INTEGER NOT NULL UNIQUE,
    PRIMARY KEY (source_kind, source_id, source_version),
    CHECK (
        (source_kind IN ('message', 'work') AND source_version = 0)
        OR (source_kind = 'artifact_version' AND source_version >= 1)
    )
);
CREATE TABLE message_mentions (
    message_id TEXT NOT NULL REFERENCES messages(id) ON DELETE RESTRICT,
    principal_kind TEXT NOT NULL CHECK (principal_kind IN ('human', 'agent')),
    principal_id TEXT NOT NULL CHECK (length(principal_id) = 36),
    ordinal INTEGER NOT NULL CHECK (ordinal BETWEEN 0 AND 63),
    PRIMARY KEY (message_id, principal_kind, principal_id),
    UNIQUE (message_id, ordinal)
);
CREATE TABLE message_target_sequences (
    target_kind TEXT NOT NULL CHECK (target_kind IN ('space', 'thread')),
    target_id TEXT NOT NULL CHECK (length(target_id) = 36),
    next_sequence INTEGER NOT NULL CHECK (next_sequence >= 1),
    PRIMARY KEY (target_kind, target_id)
);
CREATE TABLE messages (
    id TEXT NOT NULL UNIQUE CHECK (length(id) = 36),
    request_id TEXT NOT NULL UNIQUE CHECK (length(request_id) = 36),
    payload_fingerprint BLOB NOT NULL CHECK (length(payload_fingerprint) = 32),
    space_id TEXT NOT NULL REFERENCES spaces(id) ON DELETE RESTRICT,
    target_kind TEXT NOT NULL CHECK (target_kind IN ('space', 'thread')),
    target_id TEXT NOT NULL CHECK (length(target_id) = 36),
    target_sequence INTEGER NOT NULL CHECK (target_sequence >= 1),
    author_kind TEXT NOT NULL CHECK (author_kind IN ('human', 'agent')),
    author_id TEXT NOT NULL CHECK (length(author_id) = 36),
    body TEXT NOT NULL CHECK (length(body) BETWEEN 1 AND 400000),
    created_at INTEGER NOT NULL,
    PRIMARY KEY (target_kind, target_id, target_sequence),
    CHECK (
        (target_kind = 'space' AND target_id = space_id)
        OR target_kind = 'thread'
    )
);
CREATE TABLE organizations (
    singleton INTEGER PRIMARY KEY CHECK (singleton = 1),
    id TEXT NOT NULL UNIQUE CHECK (length(id) = 36),
    name TEXT NOT NULL CHECK (length(name) BETWEEN 1 AND 100),
    bootstrap_human_id TEXT NOT NULL CHECK (length(bootstrap_human_id) = 36),
    created_at INTEGER NOT NULL
);
CREATE TABLE run_completion_receipts (
    outbox_event_id TEXT PRIMARY KEY CHECK (length(outbox_event_id) = 36),
    request_id TEXT NOT NULL UNIQUE REFERENCES inbox_requests(request_id) ON DELETE RESTRICT,
    payload_fingerprint BLOB NOT NULL CHECK (length(payload_fingerprint) = 32),
    run_id TEXT NOT NULL UNIQUE REFERENCES runs(id) ON DELETE RESTRICT,
    launch_id TEXT NOT NULL REFERENCES run_launches(id) ON DELETE RESTRICT,
    fence INTEGER NOT NULL CHECK (fence > 0),
    result_kind TEXT NOT NULL CHECK (result_kind IN ('message', 'held_draft')),
    result_id TEXT NOT NULL CHECK (length(result_id) = 36),
    committed_at INTEGER NOT NULL,
    FOREIGN KEY (launch_id, run_id, fence) REFERENCES run_launches(id, run_id, fence) ON DELETE RESTRICT
);
CREATE TABLE run_launches (
    id TEXT PRIMARY KEY CHECK (length(id) = 36),
    run_id TEXT NOT NULL,
    agent_id TEXT NOT NULL REFERENCES agents(id) ON DELETE RESTRICT,
    holder_computer_id TEXT NOT NULL REFERENCES computers(id) ON DELETE RESTRICT,
    holder_placement_generation INTEGER NOT NULL CHECK (holder_placement_generation > 0),
    fence INTEGER NOT NULL CHECK (fence > 0),
    claimed_at INTEGER NOT NULL,
    expires_at INTEGER NOT NULL,
    closed_at INTEGER,
    close_reason TEXT NOT NULL DEFAULT '' CHECK (close_reason IN ('', 'replaced', 'completed')),
    FOREIGN KEY (run_id, agent_id) REFERENCES runs(id, agent_id) ON DELETE RESTRICT,
    UNIQUE (agent_id, fence),
    UNIQUE (id, run_id, fence),
    CHECK (expires_at > claimed_at),
    CHECK (
        (closed_at IS NULL AND close_reason = '')
        OR (closed_at IS NOT NULL AND close_reason = 'replaced' AND closed_at >= expires_at)
        OR (closed_at IS NOT NULL AND close_reason = 'completed' AND closed_at >= claimed_at)
    )
);
CREATE TABLE runs (
    id TEXT PRIMARY KEY CHECK (length(id) = 36),
    delivery_id TEXT NOT NULL UNIQUE,
    agent_id TEXT NOT NULL REFERENCES agents(id) ON DELETE RESTRICT,
    basis_target_sequence INTEGER NOT NULL CHECK (basis_target_sequence >= 1),
    state TEXT NOT NULL CHECK (state IN ('accepted', 'running', 'completed')),
    outcome TEXT NOT NULL DEFAULT '' CHECK (outcome IN ('', 'succeeded', 'failed')),
    result_kind TEXT NOT NULL DEFAULT '' CHECK (result_kind IN ('', 'message', 'held_draft')),
    result_id TEXT NOT NULL DEFAULT '',
    accepted_at INTEGER NOT NULL,
    started_at INTEGER,
    completed_at INTEGER,
    FOREIGN KEY (delivery_id, agent_id) REFERENCES deliveries(id, agent_id) ON DELETE RESTRICT,
    CHECK (
        (state = 'accepted' AND outcome = '' AND result_kind = '' AND result_id = '' AND started_at IS NULL AND completed_at IS NULL)
        OR (state = 'running' AND outcome = '' AND result_kind = '' AND result_id = '' AND started_at IS NOT NULL AND completed_at IS NULL)
        OR (state = 'completed' AND outcome != '' AND result_kind != '' AND length(result_id) = 36 AND started_at IS NOT NULL AND completed_at IS NOT NULL)
    )
);
CREATE TABLE space_memberships (
    space_id TEXT NOT NULL REFERENCES spaces(id) ON DELETE RESTRICT,
    principal_kind TEXT NOT NULL CHECK (principal_kind IN ('human', 'agent')),
    principal_id TEXT NOT NULL CHECK (length(principal_id) = 36),
    joined_at INTEGER NOT NULL,
    PRIMARY KEY (space_id, principal_kind, principal_id)
);
CREATE TABLE spaces (
    id TEXT PRIMARY KEY CHECK (length(id) = 36),
    organization_id TEXT NOT NULL REFERENCES organizations(id) ON DELETE RESTRICT,
    kind TEXT NOT NULL CHECK (kind IN ('dm', 'group')),
    name TEXT NOT NULL CHECK (length(name) <= 100),
    dm_key TEXT,
    created_at INTEGER NOT NULL,
    updated_at INTEGER NOT NULL,
    archived_at INTEGER,
    CHECK (
        (kind = 'dm' AND name = '' AND dm_key IS NOT NULL AND archived_at IS NULL)
        OR (kind = 'group' AND length(name) BETWEEN 1 AND 100 AND dm_key IS NULL)
    )
);
CREATE TABLE system_metadata (
    key TEXT PRIMARY KEY,
    value TEXT NOT NULL
);
INSERT INTO system_metadata(key, value) VALUES('schema_version', '3');
CREATE TABLE threads (
    id TEXT PRIMARY KEY REFERENCES messages(id) ON DELETE RESTRICT,
    space_id TEXT NOT NULL REFERENCES spaces(id) ON DELETE RESTRICT,
    created_at INTEGER NOT NULL
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
CREATE INDEX agent_held_drafts_agent_state_sequence
ON agent_held_drafts(agent_id, state, sequence);
CREATE UNIQUE INDEX agent_held_drafts_predecessor
ON agent_held_drafts(predecessor_draft_id)
WHERE predecessor_draft_id IS NOT NULL;
CREATE INDEX inbox_items_recipient_state_sequence
ON inbox_items(recipient_kind, recipient_id, state, sequence);
CREATE UNIQUE INDEX inbox_items_id_recipient
ON inbox_items(id, recipient_kind, recipient_id);
CREATE INDEX agent_placements_computer_state_agent
		ON agent_placements(computer_id, state, agent_id);
CREATE INDEX inbox_requests_actor_committed
ON inbox_requests(actor_kind, actor_id, committed_at);
CREATE UNIQUE INDEX agent_runtime_sessions_agent_current
ON agent_runtime_sessions(agent_id)
WHERE revoked_at IS NULL;
CREATE INDEX agent_runtime_sessions_binding_active
ON agent_runtime_sessions(agent_id, computer_id, placement_generation, expires_at)
WHERE revoked_at IS NULL;
CREATE INDEX principal_thread_follows_space
ON principal_thread_follows(principal_kind, principal_id, space_id, thread_root_message_id);
CREATE UNIQUE INDEX artifact_blobs_digest_size
ON artifact_blobs(digest, size);
CREATE UNIQUE INDEX artifact_grants_one_active
ON artifact_grants(artifact_id, target_kind, target_id, capability)
WHERE revoked_at IS NULL;
CREATE INDEX artifact_grants_target_active
ON artifact_grants(organization_id, target_kind, target_id, artifact_id)
WHERE revoked_at IS NULL;
CREATE INDEX artifact_versions_digest
ON artifact_versions(digest);
CREATE INDEX artifacts_organization_work_created
ON artifacts(organization_id, owning_work_id, created_at, id);
CREATE INDEX audit_events_organization_sequence
ON audit_events(organization_id, sequence);
CREATE INDEX browser_handoffs_expires_at
ON browser_handoffs(expires_at);
CREATE INDEX browser_sessions_human_active
ON browser_sessions(human_id, expires_at)
WHERE revoked_at IS NULL;
CREATE INDEX computer_pairings_human_created
ON computer_pairings(human_id, created_at);
CREATE INDEX deliveries_agent_state_sequence
ON deliveries(agent_id, state, sequence);
CREATE UNIQUE INDEX deliveries_id_agent
ON deliveries(id, agent_id);
CREATE INDEX grants_subject_capability_scope
ON grants(organization_id, subject_kind, subject_id, capability, scope_kind, scope_id);
CREATE INDEX knowledge_dirty_sources_sequence ON knowledge_dirty_sources(sequence);
CREATE INDEX knowledge_projection_rows_source
ON knowledge_projection_rows(source_kind, source_id, source_version);
CREATE INDEX message_mentions_principal_message
ON message_mentions(principal_kind, principal_id, message_id);
CREATE UNIQUE INDEX messages_inbox_trigger
ON messages(id, space_id, target_kind, target_id, target_sequence);
CREATE INDEX run_launches_agent_fence
ON run_launches(agent_id, fence);
CREATE UNIQUE INDEX run_launches_execution_binding
ON run_launches(id, run_id, fence, agent_id, holder_computer_id, holder_placement_generation);
CREATE UNIQUE INDEX run_launches_run_current
ON run_launches(run_id)
WHERE closed_at IS NULL;
CREATE UNIQUE INDEX runs_agent_one_active
ON runs(agent_id)
WHERE state IN ('accepted', 'running');
CREATE UNIQUE INDEX runs_id_agent
ON runs(id, agent_id);
CREATE UNIQUE INDEX runs_id_delivery_agent
ON runs(id, delivery_id, agent_id);
CREATE UNIQUE INDEX spaces_dm_key ON spaces(organization_id, dm_key) WHERE dm_key IS NOT NULL;
CREATE UNIQUE INDEX spaces_id_organization
ON spaces(id, organization_id);
CREATE UNIQUE INDEX spaces_id_organization_kind
ON spaces(id, organization_id, kind);
CREATE UNIQUE INDEX threads_id_space
ON threads(id, space_id);
CREATE INDEX work_acceptance_results_latest
ON work_acceptance_results(work_id, criterion_id, sequence DESC);
CREATE UNIQUE INDEX work_approvals_one_pending
ON work_approvals(work_id)
WHERE status = 'pending';
CREATE INDEX work_assignments_agent_history
ON work_assignments(agent_id, ended_at, assigned_at, id);
CREATE UNIQUE INDEX work_assignments_current_agent
ON work_assignments(work_id, agent_id)
WHERE ended_at IS NULL;
CREATE UNIQUE INDEX work_assignments_current_coordinator
ON work_assignments(work_id)
WHERE role = 'coordinator' AND ended_at IS NULL;
CREATE INDEX work_events_work_sequence
ON work_events(work_id, sequence);
CREATE INDEX works_organization_root_parent
ON works(organization_id, root_work_id, parent_work_id, created_at, id);
CREATE INDEX works_organization_state
ON works(organization_id, state, updated_at, id);
CREATE TRIGGER artifact_blobs_identity_immutable
BEFORE UPDATE ON artifact_blobs
WHEN NEW.digest != OLD.digest
  OR NEW.size != OLD.size
  OR NEW.created_at != OLD.created_at
  OR NEW.checked_at < OLD.checked_at
BEGIN
    SELECT RAISE(ABORT, 'artifact blob identity is immutable');
END;
CREATE TRIGGER artifact_blobs_immutable_delete
BEFORE DELETE ON artifact_blobs BEGIN
    SELECT RAISE(ABORT, 'artifact blobs are immutable');
END;
CREATE TRIGGER artifact_grants_immutable_delete
BEFORE DELETE ON artifact_grants BEGIN
    SELECT RAISE(ABORT, 'artifact grants are immutable');
END;
CREATE TRIGGER artifact_grants_revoke_only
BEFORE UPDATE ON artifact_grants
WHEN NEW.id != OLD.id
  OR NEW.artifact_id != OLD.artifact_id
  OR NEW.organization_id != OLD.organization_id
  OR NEW.target_kind != OLD.target_kind
  OR NEW.target_id != OLD.target_id
  OR NEW.capability != OLD.capability
  OR NEW.granted_by_kind != OLD.granted_by_kind
  OR NEW.granted_by_id != OLD.granted_by_id
  OR NEW.granted_at != OLD.granted_at
  OR OLD.revoked_at IS NOT NULL
  OR NEW.revoked_at IS NULL
BEGIN
    SELECT RAISE(ABORT, 'artifact grants only allow one revoke');
END;
CREATE TRIGGER artifact_requests_immutable_delete
BEFORE DELETE ON artifact_requests BEGIN
    SELECT RAISE(ABORT, 'artifact requests are immutable');
END;
CREATE TRIGGER artifact_requests_immutable_update
BEFORE UPDATE ON artifact_requests BEGIN
    SELECT RAISE(ABORT, 'artifact requests are immutable');
END;
CREATE TRIGGER artifact_version_executions_committed_insert
BEFORE INSERT ON artifact_version_executions
WHEN EXISTS (
    SELECT 1 FROM artifact_requests
    WHERE result_kind = 'version'
      AND result_artifact_id = NEW.artifact_id
      AND result_version = NEW.version
) BEGIN
    SELECT RAISE(ABORT, 'committed artifact execution provenance is sealed');
END;
CREATE TRIGGER artifact_version_executions_immutable_delete
BEFORE DELETE ON artifact_version_executions BEGIN
    SELECT RAISE(ABORT, 'artifact execution provenance is immutable');
END;
CREATE TRIGGER artifact_version_executions_immutable_update
BEFORE UPDATE ON artifact_version_executions BEGIN
    SELECT RAISE(ABORT, 'artifact execution provenance is immutable');
END;
CREATE TRIGGER artifact_version_sources_committed_insert
BEFORE INSERT ON artifact_version_sources
WHEN EXISTS (
    SELECT 1 FROM artifact_requests
    WHERE result_kind = 'version'
      AND result_artifact_id = NEW.artifact_id
      AND result_version = NEW.version
) BEGIN
    SELECT RAISE(ABORT, 'committed artifact sources are sealed');
END;
CREATE TRIGGER artifact_version_sources_immutable_delete
BEFORE DELETE ON artifact_version_sources BEGIN
    SELECT RAISE(ABORT, 'artifact sources are immutable');
END;
CREATE TRIGGER artifact_version_sources_immutable_update
BEFORE UPDATE ON artifact_version_sources BEGIN
    SELECT RAISE(ABORT, 'artifact sources are immutable');
END;
CREATE TRIGGER artifact_versions_immutable_delete
BEFORE DELETE ON artifact_versions BEGIN
    SELECT RAISE(ABORT, 'artifact versions are immutable');
END;
CREATE TRIGGER artifact_versions_immutable_update
BEFORE UPDATE ON artifact_versions BEGIN
    SELECT RAISE(ABORT, 'artifact versions are immutable');
END;
CREATE TRIGGER artifacts_immutable_delete
BEFORE DELETE ON artifacts BEGIN
    SELECT RAISE(ABORT, 'artifacts are immutable');
END;
CREATE TRIGGER artifacts_immutable_update
BEFORE UPDATE ON artifacts BEGIN
    SELECT RAISE(ABORT, 'artifacts are immutable');
END;
CREATE TRIGGER work_acceptance_criteria_immutable_delete
BEFORE DELETE ON work_acceptance_criteria
BEGIN
    SELECT RAISE(ABORT, 'work acceptance criteria are immutable');
END;
CREATE TRIGGER work_acceptance_criteria_immutable_update
BEFORE UPDATE ON work_acceptance_criteria
BEGIN
    SELECT RAISE(ABORT, 'work acceptance criteria are immutable');
END;
CREATE TRIGGER work_acceptance_results_append_only_delete
BEFORE DELETE ON work_acceptance_results
BEGIN
    SELECT RAISE(ABORT, 'work acceptance results are append-only');
END;
CREATE TRIGGER work_acceptance_results_append_only_update
BEFORE UPDATE ON work_acceptance_results
BEGIN
    SELECT RAISE(ABORT, 'work acceptance results are append-only');
END;
CREATE TRIGGER work_constraints_immutable_delete
BEFORE DELETE ON work_constraints
BEGIN
    SELECT RAISE(ABORT, 'work constraints are immutable');
END;
CREATE TRIGGER work_constraints_immutable_update
BEFORE UPDATE ON work_constraints
BEGIN
    SELECT RAISE(ABORT, 'work constraints are immutable');
END;
CREATE TRIGGER work_events_append_only_delete
BEFORE DELETE ON work_events
BEGIN
    SELECT RAISE(ABORT, 'work events are append-only');
END;
CREATE TRIGGER work_events_append_only_update
BEFORE UPDATE ON work_events
BEGIN
    SELECT RAISE(ABORT, 'work events are append-only');
END;
CREATE TRIGGER work_requests_immutable_delete
BEFORE DELETE ON work_requests
BEGIN
    SELECT RAISE(ABORT, 'work request receipts are immutable');
END;
CREATE TRIGGER work_requests_immutable_update
BEFORE UPDATE ON work_requests
BEGIN
    SELECT RAISE(ABORT, 'work request receipts are immutable');
END;
