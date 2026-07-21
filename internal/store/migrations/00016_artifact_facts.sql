-- +goose Up
CREATE UNIQUE INDEX runs_id_delivery_agent
ON runs(id, delivery_id, agent_id);

CREATE UNIQUE INDEX run_launches_execution_binding
ON run_launches(id, run_id, fence, agent_id, holder_computer_id, holder_placement_generation);

CREATE TABLE artifact_blobs (
    digest BLOB PRIMARY KEY CHECK (length(digest) = 32),
    size INTEGER NOT NULL CHECK (size BETWEEN 0 AND 67108864),
    integrity_state TEXT NOT NULL CHECK (integrity_state IN ('ready', 'missing', 'corrupt')),
    created_at INTEGER NOT NULL,
    checked_at INTEGER NOT NULL CHECK (checked_at >= created_at)
);

CREATE UNIQUE INDEX artifact_blobs_digest_size
ON artifact_blobs(digest, size);

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

CREATE INDEX artifacts_organization_work_created
ON artifacts(organization_id, owning_work_id, created_at, id);

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

CREATE INDEX artifact_versions_digest
ON artifact_versions(digest);

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

CREATE UNIQUE INDEX artifact_grants_one_active
ON artifact_grants(artifact_id, target_kind, target_id, capability)
WHERE revoked_at IS NULL;

CREATE INDEX artifact_grants_target_active
ON artifact_grants(organization_id, target_kind, target_id, artifact_id)
WHERE revoked_at IS NULL;

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

-- +goose StatementBegin
CREATE TRIGGER artifacts_immutable_update
BEFORE UPDATE ON artifacts BEGIN
    SELECT RAISE(ABORT, 'artifacts are immutable');
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER artifact_blobs_identity_immutable
BEFORE UPDATE ON artifact_blobs
WHEN NEW.digest != OLD.digest
  OR NEW.size != OLD.size
  OR NEW.created_at != OLD.created_at
  OR NEW.checked_at < OLD.checked_at
BEGIN
    SELECT RAISE(ABORT, 'artifact blob identity is immutable');
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER artifact_blobs_immutable_delete
BEFORE DELETE ON artifact_blobs BEGIN
    SELECT RAISE(ABORT, 'artifact blobs are immutable');
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER artifacts_immutable_delete
BEFORE DELETE ON artifacts BEGIN
    SELECT RAISE(ABORT, 'artifacts are immutable');
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER artifact_versions_immutable_update
BEFORE UPDATE ON artifact_versions BEGIN
    SELECT RAISE(ABORT, 'artifact versions are immutable');
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER artifact_versions_immutable_delete
BEFORE DELETE ON artifact_versions BEGIN
    SELECT RAISE(ABORT, 'artifact versions are immutable');
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER artifact_version_executions_immutable_update
BEFORE UPDATE ON artifact_version_executions BEGIN
    SELECT RAISE(ABORT, 'artifact execution provenance is immutable');
END;
-- +goose StatementEnd

-- +goose StatementBegin
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
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER artifact_version_executions_immutable_delete
BEFORE DELETE ON artifact_version_executions BEGIN
    SELECT RAISE(ABORT, 'artifact execution provenance is immutable');
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER artifact_version_sources_immutable_update
BEFORE UPDATE ON artifact_version_sources BEGIN
    SELECT RAISE(ABORT, 'artifact sources are immutable');
END;
-- +goose StatementEnd

-- +goose StatementBegin
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
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER artifact_version_sources_immutable_delete
BEFORE DELETE ON artifact_version_sources BEGIN
    SELECT RAISE(ABORT, 'artifact sources are immutable');
END;
-- +goose StatementEnd

-- +goose StatementBegin
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
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER artifact_grants_immutable_delete
BEFORE DELETE ON artifact_grants BEGIN
    SELECT RAISE(ABORT, 'artifact grants are immutable');
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER artifact_requests_immutable_update
BEFORE UPDATE ON artifact_requests BEGIN
    SELECT RAISE(ABORT, 'artifact requests are immutable');
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER artifact_requests_immutable_delete
BEFORE DELETE ON artifact_requests BEGIN
    SELECT RAISE(ABORT, 'artifact requests are immutable');
END;
-- +goose StatementEnd

-- +goose Down
DROP TRIGGER artifact_requests_immutable_delete;
DROP TRIGGER artifact_requests_immutable_update;
DROP TRIGGER artifact_grants_immutable_delete;
DROP TRIGGER artifact_grants_revoke_only;
DROP TRIGGER artifact_version_sources_committed_insert;
DROP TRIGGER artifact_version_sources_immutable_delete;
DROP TRIGGER artifact_version_sources_immutable_update;
DROP TRIGGER artifact_version_executions_committed_insert;
DROP TRIGGER artifact_version_executions_immutable_delete;
DROP TRIGGER artifact_version_executions_immutable_update;
DROP TRIGGER artifact_versions_immutable_delete;
DROP TRIGGER artifact_versions_immutable_update;
DROP TRIGGER artifacts_immutable_delete;
DROP TRIGGER artifacts_immutable_update;
DROP TRIGGER artifact_blobs_immutable_delete;
DROP TRIGGER artifact_blobs_identity_immutable;
DROP INDEX artifact_grants_target_active;
DROP INDEX artifact_grants_one_active;
DROP TABLE artifact_requests;
DROP TABLE artifact_grants;
DROP TABLE artifact_version_sources;
DROP TABLE artifact_version_executions;
DROP INDEX artifact_versions_digest;
DROP TABLE artifact_versions;
DROP INDEX artifacts_organization_work_created;
DROP TABLE artifacts;
DROP INDEX artifact_blobs_digest_size;
DROP TABLE artifact_blobs;
DROP INDEX run_launches_execution_binding;
DROP INDEX runs_id_delivery_agent;
